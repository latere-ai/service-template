package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// fixture is a small declaration used to exercise the loader without tying the
// tests to the fields the service happens to declare today.
type fixture struct {
	Addr     string        `env:"FX_ADDR" default:":8080" doc:"Listen address."`
	Level    slog.Level    `env:"FX_LEVEL" default:"info" doc:"Log level."`
	Timeout  time.Duration `env:"FX_TIMEOUT" default:"30s" doc:"Request timeout."`
	Retries  int           `env:"FX_RETRIES" default:"3" doc:"Retry budget."`
	Ratio    float64       `env:"FX_RATIO" default:"0.5" doc:"Sample ratio."`
	Verbose  bool          `env:"FX_VERBOSE" default:"false" doc:"Verbose output."`
	Password Secret        `env:"FX_PASSWORD" doc:"Database password."`
	Renamed  string        `env:"FX_RENAMED" flag:"short" default:"x" doc:"Field with an explicit flag."`
	NoFlag   string        `env:"FX_NO_FLAG" flag:"-" default:"y" doc:"Field with no flag."`
	Ignored  string        `doc:"Not configurable."`
}

// required is a declaration whose two mandatory fields prove the loader
// reports every missing value rather than the first.
type required struct {
	First  string `env:"RQ_FIRST" required:"true"`
	Second Secret `env:"RQ_SECOND" required:"true"`
	Third  string `env:"RQ_THIRD" default:"present"`
}

func envOf(pairs map[string]string) lookupFunc {
	return func(key string) (string, bool) {
		v, ok := pairs[key]
		return v, ok
	}
}

func filesOf(files map[string]string) readFileFunc {
	return func(path string) ([]byte, error) {
		v, ok := files[path]
		if !ok {
			return nil, fmt.Errorf("open %s: %w", path, os.ErrNotExist)
		}
		return []byte(v), nil
	}
}

func bindFixture(t *testing.T, args []string, env, files map[string]string) (*fixture, map[string]Source) {
	t.Helper()
	var f fixture
	sources, err := bind(&f, args, envOf(env), filesOf(files))
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	return &f, sources
}

// TestPrecedenceCoversEverySource walks the four levels for one field. Each
// case removes the level above it, so the winner changes one step at a time.
func TestPrecedenceCoversEverySource(t *testing.T) {
	const path = "/run/secrets/addr"
	cases := []struct {
		name   string
		args   []string
		env    map[string]string
		want   string
		source Source
	}{
		{
			name:   "flag beats everything",
			args:   []string{"-fx-addr", ":1111"},
			env:    map[string]string{"FX_ADDR": ":2222", "FX_ADDR_FILE": path},
			want:   ":1111",
			source: SourceFlag,
		},
		{
			name:   "environment beats the file and the default",
			env:    map[string]string{"FX_ADDR": ":2222", "FX_ADDR_FILE": path},
			want:   ":2222",
			source: SourceEnv,
		},
		{
			name:   "file beats the default",
			env:    map[string]string{"FX_ADDR_FILE": path},
			want:   ":3333",
			source: SourceFile,
		},
		{
			name:   "default is the floor",
			want:   ":8080",
			source: SourceDefault,
		},
		{
			name:   "an empty environment value falls through to the default",
			env:    map[string]string{"FX_ADDR": ""},
			want:   ":8080",
			source: SourceDefault,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, sources := bindFixture(t, c.args, c.env, map[string]string{path: ":3333"})
			if f.Addr != c.want {
				t.Errorf("Addr = %q, want %q", f.Addr, c.want)
			}
			if sources["FX_ADDR"] != c.source {
				t.Errorf("source = %q, want %q", sources["FX_ADDR"], c.source)
			}
		})
	}
}

// TestFileSourceTrimsTheTrailingNewline covers the shape a mounted secret
// actually has: a file written with a final newline the value does not carry.
func TestFileSourceTrimsTheTrailingNewline(t *testing.T) {
	const path = "/run/secrets/password"
	f, sources := bindFixture(t,
		nil,
		map[string]string{"FX_PASSWORD_FILE": path},
		map[string]string{path: "s3cret\r\n"})
	if got := f.Password.Reveal(); got != "s3cret" {
		t.Fatalf("Password = %q, want the value without the trailing newline", got)
	}
	if sources["FX_PASSWORD"] != SourceFile {
		t.Fatalf("source = %q, want %q", sources["FX_PASSWORD"], SourceFile)
	}
}

func TestFileSourceReportsAnUnreadableFile(t *testing.T) {
	var f fixture
	_, err := bind(&f, nil,
		envOf(map[string]string{"FX_ADDR_FILE": "/run/secrets/missing"}),
		filesOf(nil))
	if err == nil {
		t.Fatal("an unreadable secret file did not fail the load")
	}
	if !strings.Contains(err.Error(), "FX_ADDR_FILE") {
		t.Fatalf("error %q does not name the variable", err)
	}
}

// TestMissingRequiredValuesAreAllReported is the acceptance criterion that a
// restart reports every missing value, not one per deployment cycle.
func TestMissingRequiredValuesAreAllReported(t *testing.T) {
	var r required
	_, err := bind(&r, nil, envOf(nil), filesOf(nil))
	if err == nil {
		t.Fatal("a missing required value did not fail the load")
	}
	msg := err.Error()
	for _, want := range []string{"RQ_FIRST", "RQ_SECOND"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not name %s:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "RQ_THIRD") {
		t.Errorf("error names a field that has a default:\n%s", msg)
	}
	if got := strings.Count(msg, "required value is not set"); got != 2 {
		t.Errorf("reported %d missing values, want 2:\n%s", got, msg)
	}
}

func TestRequiredValueSatisfiedByAnySource(t *testing.T) {
	const path = "/run/secrets/second"
	var r required
	sources, err := bind(&r,
		[]string{"-rq-first=one"},
		envOf(map[string]string{"RQ_SECOND_FILE": path}),
		filesOf(map[string]string{path: "two\n"}))
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if r.First != "one" || r.Second.Reveal() != "two" {
		t.Fatalf("First = %q, Second = %q", r.First, r.Second.Reveal())
	}
	if sources["RQ_FIRST"] != SourceFlag || sources["RQ_SECOND"] != SourceFile {
		t.Fatalf("sources = %v", sources)
	}
}

// TestParseFailuresAreAllReported asserts the loader keeps going after the
// first bad value, for the same reason it keeps going after the first missing
// one.
func TestParseFailuresAreAllReported(t *testing.T) {
	var f fixture
	_, err := bind(&f, nil, envOf(map[string]string{
		"FX_TIMEOUT": "soon",
		"FX_RETRIES": "many",
		"FX_RATIO":   "half",
		"FX_VERBOSE": "perhaps",
		"FX_LEVEL":   "loud",
	}), filesOf(nil))
	if err == nil {
		t.Fatal("invalid values did not fail the load")
	}
	msg := err.Error()
	for _, want := range []string{"FX_TIMEOUT", "FX_RETRIES", "FX_RATIO", "FX_VERBOSE", "FX_LEVEL"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not name %s:\n%s", want, msg)
		}
	}
}

func TestTypedValuesParse(t *testing.T) {
	f, _ := bindFixture(t, nil, map[string]string{
		"FX_LEVEL":   "warn",
		"FX_TIMEOUT": "1m30s",
		"FX_RETRIES": "7",
		"FX_RATIO":   "0.25",
		"FX_VERBOSE": "true",
	}, nil)
	if f.Level != slog.LevelWarn {
		t.Errorf("Level = %v, want warn", f.Level)
	}
	if f.Timeout != 90*time.Second {
		t.Errorf("Timeout = %v, want 1m30s", f.Timeout)
	}
	if f.Retries != 7 || f.Ratio != 0.25 || !f.Verbose {
		t.Errorf("Retries = %d, Ratio = %v, Verbose = %v", f.Retries, f.Ratio, f.Verbose)
	}
}

func TestUnsetOptionalFieldKeepsItsZeroValue(t *testing.T) {
	f, sources := bindFixture(t, nil, nil, nil)
	if !f.Password.IsZero() {
		t.Errorf("Password = %q, want the zero value", f.Password.Reveal())
	}
	if sources["FX_PASSWORD"] != SourceUnset {
		t.Errorf("source = %q, want %q", sources["FX_PASSWORD"], SourceUnset)
	}
}

// TestFlagFormsAndUnknownArguments covers the spellings the scan accepts and
// the arguments it must ignore, because the entry point registers flags of its
// own on the standard flag set.
func TestFlagFormsAndUnknownArguments(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want fixture
	}{
		{
			name: "single dash with a separate value",
			args: []string{"-fx-addr", ":1"},
			want: fixture{Addr: ":1"},
		},
		{
			name: "double dash with an equals sign",
			args: []string{"--fx-addr=:2"},
			want: fixture{Addr: ":2"},
		},
		{
			name: "single dash with an equals sign",
			args: []string{"-fx-addr=:3"},
			want: fixture{Addr: ":3"},
		},
		{
			name: "a bare boolean flag means true",
			args: []string{"-fx-verbose", "-fx-addr=:4"},
			want: fixture{Addr: ":4", Verbose: true},
		},
		{
			name: "an explicit flag name is honoured",
			args: []string{"-short=renamed", "-fx-addr=:5"},
			want: fixture{Addr: ":5", Renamed: "renamed"},
		},
		{
			name: "an unknown flag and its value are ignored",
			args: []string{"-version", "-config", "prod.yaml", "-fx-addr=:6"},
			want: fixture{Addr: ":6"},
		},
		{
			name: "arguments after the terminator are not flags",
			args: []string{"--", "-fx-addr=:7"},
			want: fixture{Addr: ":8080"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, _ := bindFixture(t, c.args, nil, nil)
			if c.want.Addr != "" && f.Addr != c.want.Addr {
				t.Errorf("Addr = %q, want %q", f.Addr, c.want.Addr)
			}
			if f.Verbose != c.want.Verbose {
				t.Errorf("Verbose = %v, want %v", f.Verbose, c.want.Verbose)
			}
			if c.want.Renamed != "" && f.Renamed != c.want.Renamed {
				t.Errorf("Renamed = %q, want %q", f.Renamed, c.want.Renamed)
			}
		})
	}
}

func TestFieldWithNoFlagIgnoresTheArgument(t *testing.T) {
	f, sources := bindFixture(t, []string{"-fx-no-flag=set"}, nil, nil)
	if f.NoFlag != "y" {
		t.Fatalf("NoFlag = %q, want the default", f.NoFlag)
	}
	if sources["FX_NO_FLAG"] != SourceDefault {
		t.Fatalf("source = %q, want %q", sources["FX_NO_FLAG"], SourceDefault)
	}
}

func TestFieldWithoutAnEnvTagIsNotConfigurable(t *testing.T) {
	specs, err := specsOf(&fixture{})
	if err != nil {
		t.Fatalf("specsOf: %v", err)
	}
	for _, s := range specs {
		if s.Env == "" {
			t.Fatal("a field with no env tag produced a spec")
		}
	}
	if len(specs) != 9 {
		t.Fatalf("specsOf returned %d fields, want the 9 env-tagged ones", len(specs))
	}
}

func TestSpecsOfRejectsBadDeclarations(t *testing.T) {
	cases := []struct {
		name   string
		target any
		want   string
	}{
		{"not a pointer", fixture{}, "pointer to a struct"},
		{"pointer to a non-struct", new(int), "pointer to a struct"},
		{"nil", nil, "pointer to a struct"},
		{"no declared field", &struct{ A string }{}, "no env-tagged field"},
		{
			"duplicate variable",
			&struct {
				A string `env:"DUP"`
				B string `env:"DUP"`
			}{},
			"two fields declare the environment variable DUP",
		},
		{
			"duplicate flag",
			&struct {
				A string `env:"ONE" flag:"same"`
				B string `env:"TWO" flag:"same"`
			}{},
			"same flag name",
		},
		{
			"unexported field",
			&struct {
				a string `env:"HIDDEN"`
			}{},
			"unexported",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := specsOf(c.target)
			if err == nil {
				t.Fatal("a bad declaration produced no error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func TestBindRejectsAnUnsupportedType(t *testing.T) {
	target := &struct {
		Ports []int `env:"PORTS" default:"1,2"`
	}{}
	_, err := bind(target, nil, envOf(nil), filesOf(nil))
	if err == nil {
		t.Fatal("an unsupported field type did not fail the load")
	}
	if !strings.Contains(err.Error(), "not a supported configuration type") {
		t.Fatalf("error %q does not name the unsupported type", err)
	}
}

func TestFlagNameIsDerivedFromTheVariable(t *testing.T) {
	if got := flagName("OTEL_TRACES_SAMPLE_RATIO"); got != "otel-traces-sample-ratio" {
		t.Fatalf("flagName = %q", got)
	}
}

func TestBindReportsADeclarationError(t *testing.T) {
	_, err := bind(fixture{}, nil, envOf(nil), filesOf(nil))
	if !errors.Is(err, errNotStructPointer) {
		t.Fatalf("err = %v, want the declaration error", err)
	}
}

// TestAssignCoversTheNumericKinds walks the kinds the switch handles beyond the
// ones the service struct happens to use, including the range check a parse
// gives for free.
func TestAssignCoversTheNumericKinds(t *testing.T) {
	type numbers struct {
		Small  int8    `env:"NUM_SMALL" default:"7"`
		Count  uint16  `env:"NUM_COUNT" default:"9"`
		Weight float32 `env:"NUM_WEIGHT" default:"0.5"`
	}
	n := &numbers{}
	if _, err := bind(n, nil, envOf(nil), filesOf(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if n.Small != 7 || n.Count != 9 || n.Weight != 0.5 {
		t.Fatalf("numbers = %+v", n)
	}

	_, err := bind(&numbers{}, nil, envOf(map[string]string{
		"NUM_SMALL":  "300",
		"NUM_COUNT":  "-1",
		"NUM_WEIGHT": "wide",
	}), filesOf(nil))
	if err == nil {
		t.Fatal("out-of-range values did not fail the load")
	}
	for _, want := range []string{"NUM_SMALL", "NUM_COUNT", "NUM_WEIGHT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %s:\n%s", want, err)
		}
	}
}
