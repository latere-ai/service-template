// Command speccheck validates the spec directory and generates its index.
//
// It enforces the rules that keep a spec directory honest: required
// frontmatter, a status from the allowed set, dependency references that
// resolve, no dependency cycle, no spec dispatched ahead of its dependencies,
// no complete spec without an Outcome, and an index that matches the files.
// A spec directory nobody validates becomes an archive of intentions that no
// longer describes the code.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "speccheck:", err)
		os.Exit(1)
	}
}

// run holds the body so a test drives the command without starting a process.
func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("speccheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "specs", "path of the spec directory")
	write := fs.Bool("write-index", false, "write the index instead of verifying it")
	if err := fs.Parse(args); err != nil {
		return err
	}

	specs, err := Load(*dir)
	if err != nil {
		return err
	}
	if problems := Validate(specs); len(problems) > 0 {
		return fmt.Errorf("%d problems in %s:\n  %s",
			len(problems), *dir, strings.Join(problems, "\n  "))
	}

	if *write {
		if err := WriteIndex(*dir, specs); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "wrote %s/%s\n", *dir, IndexName)
		return err
	}
	if err := CheckIndex(*dir, specs); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s: %d specs, index current\n", *dir, len(specs))
	return err
}
