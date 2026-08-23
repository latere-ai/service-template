package config

import (
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Source names the level of the precedence order a value came from.
type Source string

const (
	// SourceFlag is a command-line flag, the highest precedence.
	SourceFlag Source = "flag"
	// SourceEnv is an environment variable.
	SourceEnv Source = "env"
	// SourceFile is the file named by <NAME>_FILE. Orchestrators mount secrets
	// as files, and reading the value from one keeps it out of the process
	// environment, where any child process and any crash dump can see it.
	SourceFile Source = "file"
	// SourceDefault is the value declared in the struct tag.
	SourceDefault Source = "default"
	// SourceUnset means nothing supplied a value and the field holds its zero.
	SourceUnset Source = "unset"
)

// fileSuffix is appended to a variable name to name the file that holds the
// value.
const fileSuffix = "_FILE"

type (
	lookupFunc   func(string) (string, bool)
	readFileFunc func(string) ([]byte, error)
)

// spec is one declared configuration field, read from its struct tags.
type spec struct {
	// Env is the environment variable name and the identity of the field in
	// error messages, in the log line, and in the example file.
	Env string
	// Flag is the command-line flag name, derived from Env unless the flag tag
	// overrides it. It is empty when the field takes no flag.
	Flag string
	// Default is the declared default, used when nothing else supplies a value.
	Default string
	// HasDefault distinguishes a declared empty default from no default.
	HasDefault bool
	// Doc is the explanation written into the example file.
	Doc string
	// Required marks a field the process cannot start without.
	Required bool

	index    int
	typ      reflect.Type
	isBool   bool
	isSecret bool
}

var (
	durationType         = reflect.TypeFor[time.Duration]()
	secretType           = reflect.TypeFor[Secret]()
	textUnmarshalerType  = reflect.TypeFor[encoding.TextUnmarshaler]()
	errNotStructPointer  = errors.New("configuration target must be a pointer to a struct")
	errNoDeclaredFields  = errors.New("configuration struct declares no env-tagged field")
	errFlagNameCollision = errors.New("two fields declare the same flag name")
)

// specsOf reads the declaration out of a pointer to a configuration struct.
// Fields are returned in declaration order, which fixes the order of the
// example file and of the start-up log line.
func specsOf(target any) ([]spec, error) {
	v := reflect.ValueOf(target)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return nil, errNotStructPointer
	}
	t := v.Elem().Type()

	var specs []spec
	seenEnv := map[string]bool{}
	seenFlag := map[string]bool{}
	for i := range t.NumField() {
		f := t.Field(i)
		env, ok := f.Tag.Lookup("env")
		if !ok || env == "" {
			continue
		}
		if !f.IsExported() {
			return nil, fmt.Errorf("field %s is unexported but carries an env tag", f.Name)
		}
		if seenEnv[env] {
			return nil, fmt.Errorf("two fields declare the environment variable %s", env)
		}
		seenEnv[env] = true

		s := spec{
			Env:      env,
			Doc:      f.Tag.Get("doc"),
			Required: f.Tag.Get("required") == "true",
			index:    i,
			typ:      f.Type,
			isBool:   f.Type.Kind() == reflect.Bool,
			isSecret: f.Type == secretType,
		}
		s.Default, s.HasDefault = f.Tag.Lookup("default")

		s.Flag = flagName(env)
		if name, ok := f.Tag.Lookup("flag"); ok {
			s.Flag = name
		}
		if s.Flag == "-" {
			s.Flag = ""
		}
		if s.Flag != "" {
			if seenFlag[s.Flag] {
				return nil, fmt.Errorf("%w: %s", errFlagNameCollision, s.Flag)
			}
			seenFlag[s.Flag] = true
		}
		specs = append(specs, s)
	}
	if len(specs) == 0 {
		return nil, errNoDeclaredFields
	}
	return specs, nil
}

// flagName derives the flag spelling from the variable name: DRAIN_DELAY
// becomes drain-delay. One spelling is derived from the other so a new field
// cannot introduce a flag the example file does not mention.
func flagName(env string) string {
	return strings.ToLower(strings.ReplaceAll(env, "_", "-"))
}

// bind fills target from the four sources in precedence order and reports the
// source of every field. Every parse failure and every missing required value
// is collected, so one start-up reports the full list.
func bind(target any, args []string, lookupEnv lookupFunc, readFile readFileFunc) (map[string]Source, error) {
	specs, err := specsOf(target)
	if err != nil {
		return nil, err
	}
	flags := scanFlags(args, specs)
	elem := reflect.ValueOf(target).Elem()

	sources := make(map[string]Source, len(specs))
	var problems []error
	for _, s := range specs {
		raw, src, err := resolve(s, flags, lookupEnv, readFile)
		if err != nil {
			problems = append(problems, err)
			sources[s.Env] = SourceUnset
			continue
		}
		sources[s.Env] = src
		if src == SourceUnset {
			if s.Required {
				problems = append(problems, fmt.Errorf(
					"%s: required value is not set (set %s, %s%s, or -%s)",
					s.Env, s.Env, s.Env, fileSuffix, s.Flag))
			}
			continue
		}
		if err := assign(elem.Field(s.index), raw); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w (from %s)", s.Env, err, src))
		}
	}
	return sources, errors.Join(problems...)
}

// resolve applies the precedence order to one field. An environment variable
// set to the empty string counts as unset, because a container runtime passes
// an undefined variable through as empty and a value that vanished should fall
// back to the default rather than blank the field.
func resolve(s spec, flags map[string]string, lookupEnv lookupFunc, readFile readFileFunc) (string, Source, error) {
	if s.Flag != "" {
		if v, ok := flags[s.Flag]; ok {
			return v, SourceFlag, nil
		}
	}
	if v, ok := lookupEnv(s.Env); ok && v != "" {
		return v, SourceEnv, nil
	}
	if path, ok := lookupEnv(s.Env + fileSuffix); ok && path != "" {
		data, err := readFile(path)
		if err != nil {
			return "", SourceUnset, fmt.Errorf("%s%s: read %s: %w", s.Env, fileSuffix, path, err)
		}
		// A secret mount routinely ends with a newline the value does not
		// include, and an untrimmed connection string fails at connect time
		// rather than at boot.
		return strings.TrimRight(string(data), "\r\n"), SourceFile, nil
	}
	if s.HasDefault {
		return s.Default, SourceDefault, nil
	}
	return "", SourceUnset, nil
}

// scanFlags extracts the declared flags from the argument list.
//
// The service entry point owns flag.CommandLine and registers flags of its
// own, so this scan tolerates arguments it does not know instead of failing on
// them. It accepts -name=value, --name=value, -name value, and a bare -name
// for a boolean field, and stops at the -- terminator.
func scanFlags(args []string, specs []spec) map[string]string {
	known := make(map[string]bool, len(specs))
	for _, s := range specs {
		if s.Flag != "" {
			known[s.Flag] = s.isBool
		}
	}
	out := map[string]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if len(arg) < 2 || arg[0] != '-' {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-")
		value, hasValue := "", false
		if k := strings.IndexByte(name, '='); k >= 0 {
			value, hasValue = name[k+1:], true
			name = name[:k]
		}
		isBool, ok := known[name]
		if !ok {
			continue
		}
		switch {
		case hasValue:
			out[name] = value
		case isBool:
			out[name] = "true"
		case i+1 < len(args):
			out[name] = args[i+1]
			i++
		}
	}
	return out
}

// assign parses raw text into one field.
//
// time.Duration is handled before the text unmarshaler check because it is an
// integer kind with no UnmarshalText method, and after it every type that
// implements the interface, slog.Level and Secret among them, parses itself.
func assign(v reflect.Value, raw string) error {
	if v.Type() == durationType {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("%q is not a duration", raw)
		}
		v.SetInt(int64(d))
		return nil
	}
	if v.CanAddr() && v.Addr().Type().Implements(textUnmarshalerType) {
		u, ok := reflect.TypeAssert[encoding.TextUnmarshaler](v.Addr())
		if !ok {
			return fmt.Errorf("type %s does not unmarshal text", v.Type())
		}
		if err := u.UnmarshalText([]byte(raw)); err != nil {
			return fmt.Errorf("%q is not a valid %s: %w", raw, v.Type(), err)
		}
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%q is not a boolean", raw)
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not an integer", raw)
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not an unsigned integer", raw)
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a number", raw)
		}
		v.SetFloat(f)
	default:
		return fmt.Errorf("type %s is not a supported configuration type", v.Type())
	}
	return nil
}

// displayValue renders one field for the start-up log line. A Secret renders
// through its own String method, so redaction happens in the type rather than
// here. An unset secret renders empty, because the placeholder would otherwise
// read as a configured value.
func displayValue(target any, s spec) string {
	v := reflect.ValueOf(target).Elem().Field(s.index)
	if s.isSecret && v.String() == "" {
		return ""
	}
	return fmt.Sprint(v.Interface())
}
