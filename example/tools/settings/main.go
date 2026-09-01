// Command settings applies the repository configuration this repository
// declares in .github/settings.yml.
//
// Branch protection, review requirements, the merge method, and the security
// features are what make the pipeline binding, and none of them live in a file
// the pipeline can read. Set by hand they differ per repository, and a
// repository where protection was never enabled looks exactly like one where it
// was. This command turns that configuration into a declaration, an idempotent
// apply, and a drift report.
//
// The scheduled report states drift and never reverts it. A maintainer may have
// changed a setting deliberately during an incident, and a job that silently
// puts it back is worse than one that says what changed.
package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// Exit codes. Drift is distinct from failure because the scheduled report and
// the template drift check act on it differently.
const (
	exitOK    = 0
	exitError = 1
	exitDrift = 2
)

// A reporting command writes to a stream it does not own, and it cannot act on
// a failed write: the message it would print about the failure goes to the
// same stream. say and sayf state that once, so a report reads as a report
// rather than as an error path repeated thirty times.
func say(w io.Writer, args ...any) { _, _ = fmt.Fprintln(w, args...) }

func sayf(w io.Writer, format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }

func main() {
	code := run(os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("settings", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "plan",
		"plan, apply, report, verify, or required-check")
	file := fs.String("file", ".github/settings.yml", "the settings declaration")
	owners := fs.String("codeowners", ".github/CODEOWNERS", "the ownership file")
	repo := fs.String("repo", os.Getenv("GITHUB_REPOSITORY"), "the repository, as owner/name")
	apiURL := fs.String("api-url", cmp.Or(os.Getenv("GITHUB_API_URL"), "https://api.github.com"),
		"the repository API endpoint")
	token := fs.String("token", os.Getenv("GITHUB_TOKEN"), "an authenticated token")
	failOnDrift := fs.Bool("fail-on-drift", false, "exit non-zero when the live configuration differs")

	if err := fs.Parse(args); err != nil {
		return exitError
	}

	declaration, err := LoadDeclaration(*file)
	if err != nil {
		say(stderr, "settings:", err)
		return exitError
	}

	if *mode == "verify" {
		return verify(declaration, *owners, stdout, stderr)
	}

	if *repo == "" {
		say(stderr, "settings: -repo is required, as owner/name")
		return exitError
	}
	if *token == "" {
		say(stderr, "settings: no token; set GITHUB_TOKEN or pass -token")
		return exitError
	}

	client := NewClient(*apiURL, *token, *repo)
	client.DryRun = *mode != "apply"

	ctx := context.Background()
	live, err := Fetch(ctx, client, declaration)
	if err != nil {
		say(stderr, "settings:", err)
		return exitError
	}
	changes := Diff(declaration, live)

	switch *mode {
	case "plan":
		if err := WriteChanges(stdout, changes); err != nil {
			say(stderr, "settings:", err)
			return exitError
		}
		sayf(stdout, "settings: %d settings differ, and a plan changes nothing\n", len(changes))
		if len(changes) > 0 && *failOnDrift {
			return exitDrift
		}
		return exitOK

	case "report":
		return report(declaration, live, changes, stdout, stderr)

	case "required-check":
		missing := MissingContexts(declaration, live)
		if len(missing) == 0 {
			sayf(stdout, "settings: %s requires %s\n",
				declaration.Protection.Branch,
				strings.Join(declaration.Protection.RequiredStatusChecks.Contexts, ", "))
			return exitOK
		}
		sayf(stderr, "settings: %s does not require %s\n",
			declaration.Protection.Branch, strings.Join(missing, ", "))
		say(stderr, "the pipeline is advisory until the gate is a required status check.")
		say(stderr, "run: make settings-apply")
		return exitDrift

	case "apply":
		if err := Apply(ctx, client, declaration, live, changes, stdout); err != nil {
			say(stderr, "settings:", err)
			return exitError
		}
		sayf(stdout, "settings: %d requests written\n", len(client.Writes))
		return exitOK

	default:
		sayf(stderr, "settings: unknown mode %q\n", *mode)
		return exitError
	}
}

// verify runs the checks that need no credentials, so a pull request can run
// them. Ownership coverage is one of them: it is a property of a file.
func verify(d Declaration, ownersPath string, stdout, stderr io.Writer) int {
	rules, err := LoadCodeOwners(ownersPath)
	if err != nil {
		say(stderr, "settings:", err)
		return exitError
	}
	if err := VerifyCoverage(rules, d.CodeOwners.RequiredPaths); err != nil {
		say(stderr, "settings:", err)
		return exitError
	}
	// A placeholder owner is an owner GitHub cannot resolve, so owner review
	// matches nothing and the repository is open while looking protected. It
	// is raised as an annotation rather than printed, because a line in a
	// green job is not what tells anybody.
	for _, rule := range PlaceholderOwners(rules) {
		sayf(stdout, "::warning file=%s,line=%d,title=Ownership placeholder::"+
			"%s still names the scaffold placeholder owner, so owner review matches nobody\n",
			ownersPath, rule.Line, rule.Pattern)
	}
	sayf(stdout, "settings: every required path has an owner (%s)\n",
		strings.Join(d.CodeOwners.RequiredPaths, ", "))
	return exitOK
}

// report is the scheduled drift report. It states what differs, writes the
// same table into the run summary, and reverts nothing.
func report(d Declaration, live Live, changes []Change, stdout, stderr io.Writer) int {
	if err := WriteChanges(stdout, changes); err != nil {
		say(stderr, "settings:", err)
		return exitError
	}
	for _, c := range changes {
		sayf(stdout, "::warning title=Repository settings drift::%s.%s is %q, the declaration says %q\n",
			c.Area, c.Setting, c.Live, c.Declared)
	}
	if missing := MissingContexts(d, live); len(missing) > 0 {
		sayf(stdout, "::warning title=Gate not required::%s does not require %s\n",
			d.Protection.Branch, strings.Join(missing, ", "))
	}

	summaryPath := os.Getenv("GITHUB_STEP_SUMMARY")
	if summaryPath == "" {
		return exitOK
	}
	summary, err := os.OpenFile(summaryPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		say(stderr, "settings:", err)
		return exitError
	}
	// The summary is appended to and then closed. A failed close would lose
	// the last buffered write, and the report it would carry is advisory.
	defer func() { _ = summary.Close() }()

	var body strings.Builder
	body.WriteString("### Repository settings\n\n")
	if len(changes) == 0 {
		body.WriteString("The live configuration matches the declaration.\n\n")
	} else {
		body.WriteString("Drift is reported and not reverted. A setting changed deliberately " +
			"during an incident stays until somebody decides otherwise.\n\n")
		body.WriteString("| Area | Setting | Live | Declared |\n| --- | --- | --- | --- |\n")
		for _, c := range changes {
			sayf(&body, "| %s | `%s` | %s | %s |\n", c.Area, c.Setting, c.Live, c.Declared)
		}
		body.WriteString("\n")
	}
	if _, err := io.WriteString(summary, body.String()); err != nil {
		say(stderr, "settings:", err)
		return exitError
	}
	return exitOK
}
