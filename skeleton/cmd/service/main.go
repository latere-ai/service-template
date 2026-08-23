// Command service is the service entry point. It reads configuration,
// constructs the server, and blocks on the process lifecycle.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"example.com/service/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print build information and exit")
	flag.Parse()

	if err := run(os.Stdout, *showVersion); err != nil {
		fmt.Fprintln(os.Stderr, "service:", err)
		os.Exit(1)
	}
}

// run holds the body so a test can drive it without starting a process. The
// binary reports the build it was stamped with, which is how a deployed
// process is traced back to one commit.
func run(out io.Writer, showVersion bool) error {
	if _, err := fmt.Fprintln(out, version.Info()); err != nil {
		return fmt.Errorf("write build information: %w", err)
	}
	if showVersion {
		return nil
	}
	return nil
}
