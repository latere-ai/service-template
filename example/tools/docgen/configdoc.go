package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/example/reference-service/internal/config"
)

// fileSuffix is the suffix of the variable that names a file holding a secret.
// The loader reads it, so the reference states it.
const fileSuffix = "_FILE"

// setting is one configuration field as the reference reports it. It is read
// from the struct tags of the configuration type, which are the same tags the
// loader binds from, so a renamed field renames the row.
type setting struct {
	Env      string
	Flag     string
	Type     string
	Default  string
	Required bool
	Secret   bool
	Doc      string
}

// settingsOf reads the declared configuration from the struct tags in
// declaration order.
func settingsOf(target any) ([]setting, error) {
	t := reflect.TypeOf(target)
	if t == nil || t.Kind() != reflect.Pointer || t.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("configuration target must be a pointer to a struct, got %T", target)
	}
	t = t.Elem()

	secretType := reflect.TypeFor[config.Secret]()
	var out []setting
	for f := range t.Fields() {
		env, ok := f.Tag.Lookup("env")
		if !ok || env == "" || !f.IsExported() {
			continue
		}
		s := setting{
			Env:      env,
			Flag:     flagName(env),
			Type:     typeName(f.Type),
			Default:  f.Tag.Get("default"),
			Required: f.Tag.Get("required") == "true",
			Secret:   f.Type == secretType,
			Doc:      f.Tag.Get("doc"),
		}
		if name, ok := f.Tag.Lookup("flag"); ok {
			s.Flag = name
		}
		if s.Flag == "-" {
			s.Flag = ""
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the configuration struct declares no env-tagged field")
	}
	return out, nil
}

// flagName derives the flag spelling from the variable name, the same rule the
// loader applies: DRAIN_DELAY becomes drain-delay.
func flagName(env string) string {
	return strings.ToLower(strings.ReplaceAll(env, "_", "-"))
}

// typeName renders a field type as the value an operator writes, not as the Go
// type. A duration is "30s", not an integer count of nanoseconds.
func typeName(t reflect.Type) string {
	switch t {
	case reflect.TypeFor[time.Duration]():
		return "duration"
	case reflect.TypeFor[slog.Level]():
		return "log level"
	case reflect.TypeFor[config.Secret]():
		return "secret"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	default:
		return t.String()
	}
}

// configurationIntro is the prose around the generated tables. It addresses one
// audience, the operator running the service, and answers one question: which
// values exist, and what each one changes.
const configurationIntro = `# Configuration

Every input the service reads is listed here. The service reads its
configuration once at start-up, so a changed value takes effect at the next
restart.

## Where a value comes from

The first source that supplies a value wins:

1. A command-line flag.
2. An environment variable.
3. The file named by the ` + "`<NAME>_FILE`" + ` variable, with surrounding
   whitespace removed. This is how a mounted secret is read.
4. The default declared below.

A missing required value, an unparsable value, or a value outside its allowed
range stops start-up, and the failure names every problem it found rather than
the first.

At start-up the service logs the effective values with the source of each one,
so an operator can see which values were defaulted. Secrets are redacted in
that record and in every other record.
`

// RenderConfiguration produces the configuration reference from the
// configuration struct. The document cannot drift from the code because it is
// not written by hand.
func RenderConfiguration() ([]byte, error) {
	settings, err := settingsOf(&config.Config{})
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	b.WriteString(configurationIntro)
	b.WriteString("\n")
	b.WriteString(generatedNotice)

	b.WriteString("\n## Settings\n\n")
	b.WriteString("| Variable | Flag | Type | Default | Effect |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, s := range settings {
		flag := "none"
		if s.Flag != "" {
			flag = "`-" + s.Flag + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n",
			s.Env, flag, s.Type, defaultCell(s), cell(s.Doc))
	}

	writeList(&b, "Required values", "The service does not start without these.",
		"Every value has a default or is optional, so the service starts with no configuration at all.",
		settings, func(s setting) bool { return s.Required })

	writeList(&b, "Secrets",
		"These carry credentials. Mount each one as a file and set `<NAME>"+fileSuffix+"` to its path, "+
			"so the value is not visible in the process environment. Their values are redacted in every log record.",
		"The service reads no secret value.",
		settings, func(s setting) bool { return s.Secret })

	b.WriteString("\n## The example file\n\n")
	b.WriteString("`" + config.EnvExampleName + "` holds the same set with the defaults filled in.\n" +
		"Copy it to `.env` and edit the values. It is generated from the same struct as this\n" +
		"reference, so the two cannot disagree.\n")
	return b.Bytes(), nil
}

// defaultCell renders the default of a setting, distinguishing a declared
// empty default from a value that turns a feature off when unset.
func defaultCell(s setting) string {
	if s.Default == "" {
		if s.Required {
			return "none, required"
		}
		return "empty"
	}
	return "`" + s.Default + "`"
}

// writeList renders the settings a rule selects, and states the rule as
// satisfied when none match, because a missing section reads as an omission.
func writeList(b *bytes.Buffer, title, intro, empty string, settings []setting, match func(setting) bool) {
	fmt.Fprintf(b, "\n## %s\n\n", title)
	var selected []setting
	for _, s := range settings {
		if match(s) {
			selected = append(selected, s)
		}
	}
	if len(selected) == 0 {
		fmt.Fprintf(b, "%s\n", empty)
		return
	}
	fmt.Fprintf(b, "%s\n\n", intro)
	for _, s := range selected {
		fmt.Fprintf(b, "- `%s`\n", s.Env)
	}
}
