// Command envexample writes the example environment file from the
// configuration struct, or verifies that the committed copy still matches it.
//
// It exists so the example file is derived from the code rather than
// maintained beside it, which is the only way the two cannot drift.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/example/reference-service/internal/config"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "envexample:", err)
		os.Exit(1)
	}
}

// run holds the body so a test drives the command without starting a process.
// It owns its own flag set for the same reason.
func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("envexample", flag.ContinueOnError)
	fs.SetOutput(out)
	path := fs.String("out", config.EnvExampleName, "path of the example environment file")
	check := fs.Bool("check", false, "verify the file matches the configuration struct instead of writing it")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *check {
		if err := config.CheckEnvExample(*path); err != nil {
			return err
		}
		_, err := fmt.Fprintf(out, "%s: current\n", *path)
		return err
	}
	if err := config.WriteEnvExample(*path); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "wrote %s\n", *path)
	return err
}
