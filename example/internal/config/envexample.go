package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
)

// EnvExampleName is the file the example environment is written to.
const EnvExampleName = ".env.example"

// envExampleHeader introduces the generated file. It carries no timestamp and
// no version, so the output is a pure function of the configuration struct and
// two runs produce identical bytes.
const envExampleHeader = `# Environment configuration.
#
# Copy this file to .env and edit the values. Every variable below is read once
# at start-up. Precedence, highest first: a command-line flag, this variable,
# the file named by <NAME>_FILE, then the default shown here.
#
# Generated from the configuration struct. Run "make env-example" after adding
# or changing a field; "make env-example-check" proves the committed copy is
# current.
`

// ErrEnvExampleStale reports a committed example file that no longer matches
// the configuration struct.
var ErrEnvExampleStale = errors.New("the example environment file is out of date")

// EnvExample renders the example environment file from the configuration
// struct. The file cannot drift from the code because it is not hand-written.
func EnvExample() ([]byte, error) {
	specs, err := specsOf(&Config{})
	if err != nil {
		return nil, err
	}
	return renderEnvExample(specs), nil
}

// renderEnvExample writes one stanza per declared field, in declaration order.
func renderEnvExample(specs []spec) []byte {
	var b bytes.Buffer
	b.WriteString(envExampleHeader)
	for _, s := range specs {
		b.WriteString("\n")
		if s.Doc != "" {
			fmt.Fprintf(&b, "# %s\n", s.Doc)
		}
		if s.Flag != "" {
			fmt.Fprintf(&b, "# flag: -%s\n", s.Flag)
		}
		if s.isSecret {
			fmt.Fprintf(&b, "# secret: mount it as a file and set %s%s instead.\n", s.Env, fileSuffix)
		}
		switch {
		case s.Required:
			b.WriteString("# required: the service does not start without it.\n")
		case !s.HasDefault:
			b.WriteString("# optional: unset leaves the feature off.\n")
		}
		fmt.Fprintf(&b, "%s=%s\n", s.Env, s.Default)
	}
	return b.Bytes()
}

// WriteEnvExample renders the example file and writes it to path.
func WriteEnvExample(path string) error {
	data, err := EnvExample()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// CheckEnvExample reports whether the file at path matches what the current
// configuration struct renders. A missing file fails the same way a stale one
// does, because both mean the committed repository does not describe what the
// service reads.
func CheckEnvExample(path string) error {
	want, err := EnvExample()
	if err != nil {
		return err
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: read %s: %w", ErrEnvExampleStale, path, err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%w: %s differs from the configuration struct\n%s\nrun: make env-example",
			ErrEnvExampleStale, path, firstDifference(got, want))
	}
	return nil
}

// firstDifference names the first line that differs, so the failure says what
// changed instead of only that something did.
func firstDifference(got, want []byte) string {
	gotLines := strings.Split(string(got), "\n")
	wantLines := strings.Split(string(want), "\n")
	for i := range max(len(gotLines), len(wantLines)) {
		g, w := lineAt(gotLines, i), lineAt(wantLines, i)
		if g != w {
			return fmt.Sprintf("  line %d\n    committed: %q\n    expected:  %q", i+1, g, w)
		}
	}
	return "  the files differ in trailing content"
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}
