// Command docgen generates the documents that are derived from the code and
// verifies the claims the documentation set makes.
//
// Two failure modes are addressed here. A document written by hand beside the
// code goes stale, so the configuration reference and the interface reference
// are generated from the configuration struct and the route table, and a
// committed copy that differs from the code fails the build. A link that no
// longer resolves and a diagram with a syntax error are invisible in review,
// because a broken diagram renders as an empty space rather than as an error,
// so both are checked here as well.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// usage lists the subcommands. Each one is a separate concern with its own
// flags, and the check subcommand runs the set the build runs.
const usage = `usage: docgen <command> [flags]

commands:
  generate    write the derived documents
  check       run every check below and verify the derived documents
  docs-check  verify the derived documents against the code
  links       resolve every link
  diagrams    validate every mermaid diagram
  refs        report internal references in the published documents
  images      verify the dependency images are pinned
`

// mermaidCommands are the names the mermaid command line is installed under.
var mermaidCommands = []string{"mmdc", "mermaid"}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "docgen:", err)
		os.Exit(1)
	}
}

// run holds the body so a test drives the command without starting a process.
func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		write(stderr, "%s", usage)
		return fmt.Errorf("no command given")
	}
	command, rest := args[0], args[1:]

	fs := flag.NewFlagSet("docgen "+command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "path of the repository root")
	docs := fs.String("docs", "docs", "path of the documentation directory")
	compose := fs.String("compose", "docker-compose.yml", "path of the dependency stack definition")
	external := fs.Bool("external", false, "check external links as well, which needs the network")
	resolve := fs.Bool("resolve", false, "compare every image pin against the registry, which needs the network")
	mermaid := fs.String("mermaid", "",
		`path of the mermaid command line; empty finds it on PATH, "none" checks structure only`)
	if err := fs.Parse(rest); err != nil {
		return err
	}

	switch command {
	case "generate":
		written, err := WriteDocs(*docs)
		if err != nil {
			return err
		}
		for _, path := range written {
			write(stdout, "wrote %s\n", path)
		}
		return nil
	case "docs-check":
		if err := CheckDocs(*docs); err != nil {
			return err
		}
		write(stdout, "%s: the derived documents match the code\n", *docs)
		return nil
	case "links":
		return report(stdout, "links", CheckLinks(*root, *external, nil))
	case "diagrams":
		return report(stdout, "diagrams", CheckDiagrams(*root, renderer(*mermaid, stdout)))
	case "refs":
		return report(stdout, "references", CheckReferences(*root))
	case "images":
		return report(stdout, "images", checkCompose(*compose, *resolve))
	case "check":
		return checkAll(stdout, *root, *docs, *compose, *mermaid, *external, *resolve)
	default:
		write(stderr, "%s", usage)
		return fmt.Errorf("unknown command %q", command)
	}
}

// checkAll runs every check and reports every failure, because a run that
// stops at the first one turns a review into a queue of single-fix runs.
func checkAll(stdout io.Writer, root, docs, compose, mermaid string, external, resolve bool) error {
	var failures []string
	if err := CheckDocs(docs); err != nil {
		failures = append(failures, err.Error())
	} else {
		write(stdout, "%s: the derived documents match the code\n", docs)
	}
	for _, check := range []struct {
		name     string
		problems []string
	}{
		{"links", CheckLinks(root, external, nil)},
		{"diagrams", CheckDiagrams(root, renderer(mermaid, stdout))},
		{"references", CheckReferences(root)},
		{"images", checkCompose(compose, resolve)},
	} {
		if err := report(stdout, check.name, check.problems); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "\n"))
	}
	return nil
}

// checkCompose applies the image rules to the dependency stack.
func checkCompose(path string, resolve bool) []string {
	text, err := readFile(path)
	if err != nil {
		return []string{fmt.Sprintf("read %s: %v", path, err)}
	}
	problems := CheckImages(path, text)
	if resolve {
		problems = append(problems, ResolveImages(path, text, &Registry{})...)
	}
	return problems
}

// renderer resolves the mermaid command line. An absent renderer leaves the
// structural rules running, and the run says so, because a check that reports
// nothing about a missing tool reads as a check that passed.
func renderer(configured string, stdout io.Writer) string {
	switch configured {
	case "none":
		write(stdout, "diagrams: rendering is off, checking structure only\n")
		return ""
	case "":
	default:
		return configured
	}
	for _, name := range mermaidCommands {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	write(stdout, "diagrams: mermaid is not on PATH, checking structure only "+
		"(install it with: bun add -g @mermaid-js/mermaid-cli)\n")
	return ""
}

// write prints progress. A failed write to a terminal or a pipe has no remedy
// inside a check, and the exit status still carries the verdict.
func write(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// report prints the outcome of one check and turns its problems into an error.
func report(stdout io.Writer, name string, problems []string) error {
	if len(problems) == 0 {
		write(stdout, "%s: clean\n", name)
		return nil
	}
	return fmt.Errorf("%d %s problems:\n  %s", len(problems), name, strings.Join(problems, "\n  "))
}
