// Command release is the release pipeline's logic and the maintainer's release
// command in one place.
//
// The pipeline is wiring: every decision it makes, from the tag gate through
// the rollout to the evidence block, is a subcommand here, so each one is
// covered by a test that can fail. A rule that lives only in a workflow file
// is a rule nobody can prove.
//
// Subcommands:
//
//	release    prove the gate, push the tag, watch the pipeline, report live
//	version    derive the next version from the commits since the last tag
//	gate       assert a passing verify run for one exact commit
//	preflight  verify every credential before the first mutating step
//	rollout    apply a target overlay, wait, and assert the rollout landed
//	evidence   validate and render the release evidence block
//	check      assert the build files and the deploy tree hold their contract
//	pins       assert every workflow action is pinned by commit digest
//	secrets    report the declared secret set and what this run is missing
//	namespace  print the namespace a target overlay applies into
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
)

// EnvStepSummary is the run summary the pipeline publishes.
const EnvStepSummary = "GITHUB_STEP_SUMMARY"

// EnvJobOutput is the file a job writes its outputs to.
const EnvJobOutput = "GITHUB_OUTPUT"

// EnvRepo is the owner/name of the repository the run belongs to.
const EnvRepo = "GITHUB_REPOSITORY"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := dispatch(ctx, ExecRunner{}, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "release: %v\n", err)
		os.Exit(1)
	}
}

// dispatch routes one invocation. It takes its dependencies as parameters so
// every subcommand is exercised by a test without a cluster or a network.
func dispatch(ctx context.Context, r Runner, args []string, getenv func(string) string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: release <subcommand> [flags]\nsubcommands: release, version, gate, preflight, rollout, evidence, check, pins, secrets")
	}
	name, rest := args[0], args[1:]
	commands := map[string]func(context.Context, Runner, []string, func(string) string, io.Writer) error{
		"release":   cmdRelease,
		"version":   cmdVersion,
		"gate":      cmdGate,
		"preflight": cmdPreflight,
		"rollout":   cmdRollout,
		"evidence":  cmdEvidence,
		"check":     cmdCheck,
		"pins":      cmdPins,
		"secrets":   cmdSecrets,
		"namespace": cmdNamespace,
	}
	cmd, ok := commands[name]
	if !ok {
		return fmt.Errorf("unknown subcommand %q", name)
	}
	return cmd(ctx, r, rest, getenv, stdout)
}

// flags builds a flag set that reports its errors instead of exiting, so a
// test reads the failure.
func flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// cmdVersion derives the version the next release would take. It runs in dry
// mode on every default-branch build, so the number is never a surprise.
func cmdVersion(ctx context.Context, r Runner, args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flags("version")
	ref := fs.String("ref", "HEAD", "commit to derive the version for")
	if err := fs.Parse(args); err != nil {
		return err
	}

	commit, err := ResolveCommit(ctx, r, *ref)
	if err != nil {
		return err
	}
	previous, err := PreviousRelease(ctx, r, commit)
	if err != nil {
		return err
	}
	commits, err := CommitsSince(ctx, r, previous, commit)
	if err != nil {
		return err
	}
	next, bump, err := NextVersion(previous, commits)
	if err != nil {
		return err
	}

	printf(stdout, "previous %s\nnext     %s (%s)\n\n", describePrevious(previous), next, bump)
	printf(stdout, "%s", ChangeSummary(previous, commits))
	return writeOutputs(getenv, map[string]string{
		"version":  next.String(),
		"bump":     bump.String(),
		"previous": previous,
		"commit":   commit,
	})
}

// cmdGate asserts the exact commit has a passing verify run. Nothing is built
// before this holds.
func cmdGate(ctx context.Context, r Runner, args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flags("gate")
	repo := fs.String("repo", getenv(EnvRepo), "owner/name of the repository")
	commit := fs.String("commit", "", "the exact commit the tag points at")
	workflow := fs.String("workflow", DefaultVerifyWorkflow, "the verify workflow file")
	branch := fs.String("branch", "", "restrict the proving run to one branch")
	event := fs.String("event", DefaultGateEvent, "restrict the proving run to one trigger")
	if err := fs.Parse(args); err != nil {
		return err
	}

	run, err := FindVerifyRun(ctx, r, GateOptions{
		Repo: *repo, Workflow: *workflow, Commit: *commit, Branch: *branch, Event: *event,
	})
	if err != nil {
		return err
	}
	printf(stdout, "commit %s verified by run %d: %s\n", *commit, run.ID, run.URL)
	return writeOutputs(getenv, map[string]string{
		"verify_run":    run.URL,
		"verify_run_id": fmt.Sprint(run.ID),
	})
}

// cmdRelease is the maintainer command.
func cmdRelease(ctx context.Context, r Runner, args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flags("release")
	repo := fs.String("repo", getenv(EnvRepo), "owner/name of the repository")
	ref := fs.String("ref", "HEAD", "commit to release")
	remote := fs.String("remote", "origin", "remote to push the tag to")
	branch := fs.String("branch", "", "restrict the proving run to one branch")
	dryRun := fs.Bool("dry-run", false, "prove the gate and print the version without tagging")
	watch := fs.Bool("watch", true, "follow the pipeline the tag starts")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return Release(ctx, r, ReleaseOptions{
		Repo:   *repo,
		Ref:    *ref,
		Remote: *remote,
		Gate:   GateOptions{Repo: *repo, Branch: *branch},
		DryRun: *dryRun,
		Watch:  *watch,
		Out:    stdout,
	})
}

// cmdPreflight verifies the credentials before anything is published.
func cmdPreflight(ctx context.Context, r Runner, args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flags("preflight")
	namespace := fs.String("namespace", "", "the namespace the deploy credential is scoped to")
	secrets := fs.String("secrets", SecretsFile, "the declared secret set")
	if err := fs.Parse(args); err != nil {
		return err
	}

	summary, closeSummary, err := summaryWriter(getenv, stdout)
	if err != nil {
		return err
	}
	defer closeSummary()

	return Preflight(ctx, r, PreflightOptions{
		SecretsPath: *secrets,
		Namespace:   *namespace,
		Now:         time.Now().UTC(),
		Getenv:      getenv,
		Out:         summary,
	})
}

// cmdRollout deploys one target and proves the rollout landed.
func cmdRollout(ctx context.Context, r Runner, args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flags("rollout")
	root := fs.String("root", DefaultDeployRoot, "the deploy tree")
	target := fs.String("target", "", "the target overlay")
	service := fs.String("service", "", "the workload and container name, derived from cmd/ when empty")
	image := fs.String("image", "", "the registry reference without a tag")
	digest := fs.String("digest", "", "the image digest")
	timeout := fs.Duration("timeout", DefaultRolloutTimeout, "how long to wait for the rollout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	name := *service
	if name == "" {
		derived, err := ServiceName(".")
		if err != nil {
			return err
		}
		name = derived
	}

	summary, closeSummary, err := summaryWriter(getenv, stdout)
	if err != nil {
		return err
	}
	defer closeSummary()

	status, err := Rollout(ctx, r, RolloutOptions{
		Root: *root, Target: *target, Service: name,
		Image: *image, Digest: *digest, Timeout: *timeout, Summary: summary,
	})
	if err != nil {
		return err
	}

	completed := status.Completed.Format(time.RFC3339)
	printf(summary, "\n%s rolled out to %s: %d of %d replicas ready at %s\n",
		status.Image, status.Namespace, status.ReadyReplicas, status.DesiredReplicas, completed)
	printf(stdout, "%s rolled out to %s: %d of %d replicas ready\n",
		status.Image, status.Namespace, status.ReadyReplicas, status.DesiredReplicas)
	return writeOutputs(getenv, map[string]string{
		"namespace": status.Namespace,
		"replicas":  fmt.Sprint(status.ReadyReplicas),
		"completed": completed,
		"image":     status.Image,
	})
}

// cmdEvidence validates the block and renders it. A field that cannot be
// filled fails the release rather than printing a placeholder.
func cmdEvidence(_ context.Context, _ Runner, args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flags("evidence")
	in := fs.String("in", "", "the JSON the pipeline assembled from its job outputs")
	live := fs.String("live", "", "the smoke evidence block")
	out := fs.String("out", "", "where to write the rendered block, stdout when empty")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return errors.New("evidence needs -in")
	}

	data, err := os.ReadFile(*in)
	if err != nil {
		return fmt.Errorf("read %s: %w", *in, err)
	}
	e, err := ParseEvidence(data)
	if err != nil {
		return err
	}
	if *live != "" {
		block, err := os.ReadFile(*live)
		if err != nil {
			return fmt.Errorf("read %s: %w", *live, err)
		}
		e.LiveCheck = string(block)
	}
	if err := e.Validate(); err != nil {
		return err
	}

	rendered := e.Markdown()
	if *out == "" {
		_, err := fmt.Fprint(stdout, rendered)
		return err
	}
	if err := os.WriteFile(*out, []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	printf(stdout, "wrote the evidence block to %s\n", *out)
	return nil
}

// cmdCheck asserts the build files and the deploy tree still hold the contract
// the pipeline depends on.
func cmdCheck(_ context.Context, _ Runner, args []string, _ func(string) string, stdout io.Writer) error {
	fs := flags("check")
	root := fs.String("root", ".", "the repository root")
	deployRoot := fs.String("deploy", DefaultDeployRoot, "the deploy tree")
	service := fs.String("service", "", "the workload name, derived from cmd/ when empty")
	if err := fs.Parse(args); err != nil {
		return err
	}

	name := *service
	if name == "" {
		derived, err := ServiceName(*root)
		if err != nil {
			return err
		}
		name = derived
	}

	problems, err := CheckDockerfiles(joinRoot(*root, DevDockerfile), joinRoot(*root, CIDockerfile))
	if err != nil {
		return err
	}
	deployProblems, err := CheckDeploy(joinRoot(*root, *deployRoot), name)
	if err != nil {
		return err
	}
	problems = append(problems, deployProblems...)
	if len(problems) > 0 {
		return fmt.Errorf("%d problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	printf(stdout, "build files and deploy tree hold the contract for %q\n", name)
	return nil
}

// cmdPins asserts every action reference is immutable.
func cmdPins(_ context.Context, _ Runner, args []string, _ func(string) string, stdout io.Writer) error {
	fs := flags("pins")
	dir := fs.String("dir", WorkflowDir, "the workflow directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	problems, err := CheckActionPins(*dir)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d unpinned action reference(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	printf(stdout, "every action reference in %s is pinned by digest\n", *dir)
	return nil
}

// cmdSecrets reports the declared secret set and what this run cannot read, so
// a consumer learns about a missing credential during setup.
func cmdSecrets(_ context.Context, _ Runner, args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flags("secrets")
	file := fs.String("file", SecretsFile, "the declared secret set")
	if err := fs.Parse(args); err != nil {
		return err
	}
	decls, err := LoadSecrets(*file)
	if err != nil {
		return err
	}
	problems := CheckSecrets(decls, time.Now().UTC(), getenv)
	for _, d := range decls {
		printf(stdout, "%-12s %-10s %s\n", d.Name, d.Kind, oneLine(d.Purpose))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	return nil
}

// cmdNamespace prints the namespace a target applies into. The overlay is the
// one place a target's namespace is written, and the preflight needs the same
// value the rollout will use.
func cmdNamespace(_ context.Context, _ Runner, args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flags("namespace")
	root := fs.String("root", DefaultDeployRoot, "the deploy tree")
	target := fs.String("target", "", "the target overlay")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir, err := TargetDir(*root, *target)
	if err != nil {
		return err
	}
	overlay, err := kustomizationPath(dir)
	if err != nil {
		return err
	}
	k, err := LoadKustomization(overlay)
	if err != nil {
		return err
	}
	namespace := k.Namespace()
	if namespace == "" {
		return fmt.Errorf("%s declares no namespace", overlay)
	}
	printf(stdout, "%s\n", namespace)
	return writeOutputs(getenv, map[string]string{"namespace": namespace})
}

// printf writes to the caller's output. A failed write to a run log cannot be
// reported anywhere else, so the error is dropped here once rather than at
// every call site.
func printf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// joinRoot joins a path onto the repository root, leaving an absolute path
// alone.
func joinRoot(root, path string) string {
	if root == "" || root == "." {
		return path
	}
	return strings.TrimRight(root, "/") + "/" + path
}

// summaryWriter returns where the run summary goes. The pipeline names a file;
// a local run gets the same text on its terminal.
func summaryWriter(getenv func(string) string, stdout io.Writer) (io.Writer, func(), error) {
	path := strings.TrimSpace(getenv(EnvStepSummary))
	if path == "" {
		return stdout, func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open the run summary: %w", err)
	}
	return f, func() { _ = f.Close() }, nil
}

// writeOutputs publishes job outputs for the steps that follow. A value with a
// newline is written in the delimited form the runner expects.
func writeOutputs(getenv func(string) string, values map[string]string) error {
	path := strings.TrimSpace(getenv(EnvJobOutput))
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open the job output file: %w", err)
	}
	var b strings.Builder
	for _, key := range sortedKeys(values) {
		value := values[key]
		if strings.ContainsAny(value, "\r\n") {
			fmt.Fprintf(&b, "%s<<RELEASE_EOF\n%s\nRELEASE_EOF\n", key, value)
			continue
		}
		fmt.Fprintf(&b, "%s=%s\n", key, value)
	}
	_, writeErr := io.WriteString(f, b.String())
	if joined := errors.Join(writeErr, f.Close()); joined != nil {
		return fmt.Errorf("write the job output file: %w", joined)
	}
	return nil
}

// sortedKeys returns map keys in a stable order, so a job output file is
// byte-identical for the same values.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
