// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package generator

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Env is everything the command needs from the outside world. Passing it in
// keeps every exit code reachable from a test without starting a process.
type Env struct {
	// Skeleton is the template's skeleton tree, rooted at the directory that
	// holds manifests/.
	Skeleton fs.FS
	Stdout   io.Writer
	Stderr   io.Writer
	// Now is the clock the waiver expiry is measured against.
	Now time.Time
	// Version is the release of the generator itself. It is the default
	// version an init writes and an upgrade moves to.
	Version string
}

const usage = `template materializes and verifies the files a service template owns.

Usage:
  template init      scaffold a new repository from a profile
  template sync      rewrite generated files to the pinned version
  template check     compare the repository against the pinned version
  template upgrade   move to a new template version, sync, and print the diff
  template manifest  validate the skeleton manifest against the skeleton tree

Run "template <command> -h" for the flags of a command.

Exit codes for check:
  0  clean
  1  the check could not be evaluated
  3  the repository edited generated files
  4  the repository is behind the template
`

// Run executes one command and returns the process exit code.
//
// A write to the command's own streams is not checked: the report of a failed
// write would go to the stream that failed.
func Run(env Env, args []string) int {
	if env.Now.IsZero() {
		env.Now = time.Now()
	}
	if len(args) == 0 {
		_, _ = fmt.Fprint(env.Stderr, usage)
		return ExitError
	}
	var err error
	code := -1
	switch args[0] {
	case "init":
		err = runInit(env, args[1:])
	case "sync":
		err = runSync(env, args[1:])
	case "check":
		code, err = runCheck(env, args[1:])
	case "upgrade":
		err = runUpgrade(env, args[1:])
	case "manifest":
		err = runManifest(env, args[1:])
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(env.Stdout, usage)
		return ExitOK
	default:
		_, _ = fmt.Fprintf(env.Stderr, "unknown command %q\n\n%s", args[0], usage)
		return ExitError
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitError
		}
		_, _ = fmt.Fprintf(env.Stderr, "template: %v\n", err)
		return ExitError
	}
	if code < 0 {
		return ExitOK
	}
	return code
}

// newFlagSet builds a flag set that reports errors through the returned error
// rather than exiting the process.
func newFlagSet(env Env, name string) *flag.FlagSet {
	set := flag.NewFlagSet("template "+name, flag.ContinueOnError)
	set.SetOutput(env.Stderr)
	return set
}

func runInit(env Env, args []string) error {
	set := newFlagSet(env, "init")
	dir := set.String("C", ".", "directory to scaffold")
	module := set.String("module", "", "Go module path of the new repository")
	name := set.String("name", "", "service name, lower case with hyphens")
	profile := set.String("profile", ProfileService,
		"repository shape: "+strings.Join(AllProfiles, ", "))
	features := set.String("features", "",
		"comma separated feature flags to enable: "+strings.Join(AllFeatures, ", "))
	version := set.String("version", env.Version, "template version to record")
	origin := set.String("template", DefaultTemplate, "template identity to record")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *module == "" {
		return errors.New("init needs -module, the Go module path of the new repository")
	}
	if *name == "" {
		*name = filepath.Base(*module)
	}
	cfg := &Config{
		Template: *origin,
		Version:  *version,
		Module:   *module,
		Name:     *name,
		Profile:  *profile,
		Features: map[string]bool{},
	}
	for f := range strings.SplitSeq(*features, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			cfg.Features[f] = true
		}
	}
	if err := cfg.Validate(ConfigFile); err != nil {
		return err
	}
	report, err := Init(env.Skeleton, *dir, cfg)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(env.Stdout, "scaffolded the %s profile of %s at %s\n", cfg.Profile, cfg.Module, *dir)
	_, _ = fmt.Fprint(env.Stdout, report.String())
	return nil
}

// Init scaffolds a repository: it writes the declaration, every selected file,
// and the lock. It refuses a directory that already holds a declaration,
// because a second init would overwrite seed files the consumer owns.
func Init(src fs.FS, dir string, cfg *Config) (*SyncReport, error) {
	if _, err := os.Stat(filepath.Join(dir, ConfigFile)); err == nil {
		return nil, fmt.Errorf("%s already holds %s; use template sync", dir, ConfigFile)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	report, err := Sync(src, dir, cfg, &Lock{Features: map[string]bool{}})
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), cfg.Marshal(), 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", ConfigFile, err)
	}
	return report, nil
}

func runSync(env Env, args []string) error {
	set := newFlagSet(env, "sync")
	dir := set.String("C", ".", "repository directory")
	if err := set.Parse(args); err != nil {
		return err
	}
	cfg, lock, err := load(*dir)
	if err != nil {
		return err
	}
	report, err := Sync(env.Skeleton, *dir, cfg, lock)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprint(env.Stdout, report.String())
	return nil
}

func runCheck(env Env, args []string) (int, error) {
	set := newFlagSet(env, "check")
	dir := set.String("C", ".", "repository directory")
	if err := set.Parse(args); err != nil {
		return ExitError, err
	}
	cfg, lock, err := load(*dir)
	if err != nil {
		return ExitError, err
	}
	report, err := Check(env.Skeleton, *dir, cfg, lock, env.Now)
	if err != nil {
		return ExitError, err
	}
	out := env.Stdout
	if report.Exit() != ExitOK {
		out = env.Stderr
	}
	_, _ = fmt.Fprint(out, report.String())
	return report.Exit(), nil
}

func runUpgrade(env Env, args []string) error {
	set := newFlagSet(env, "upgrade")
	dir := set.String("C", ".", "repository directory")
	version := set.String("version", env.Version, "template version to move to")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *version == "" {
		return errors.New("upgrade needs -version, the template release to move to")
	}
	cfg, lock, err := load(*dir)
	if err != nil {
		return err
	}
	if err := guardProfile(cfg, lock); err != nil {
		return err
	}
	from := cfg.Version
	path := filepath.Join(*dir, ConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", ConfigFile, err)
	}
	updated, err := SetVersionLine(data, *version)
	if err != nil {
		return err
	}
	next, err := ParseConfig(ConfigFile, updated)
	if err != nil {
		return err
	}
	report, err := Sync(env.Skeleton, *dir, next, lock)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", ConfigFile, err)
	}
	_, _ = fmt.Fprintf(env.Stdout, "upgraded %s from %s to %s\n", ConfigFile, from, *version)
	_, _ = fmt.Fprint(env.Stdout, report.String())
	paths := make([]string, 0, len(report.Diffs))
	for p := range report.Diffs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		_, _ = fmt.Fprint(env.Stdout, report.Diffs[p])
	}
	return nil
}

func runManifest(env Env, args []string) error {
	set := newFlagSet(env, "manifest")
	if err := set.Parse(args); err != nil {
		return err
	}
	if err := VerifyCoverage(env.Skeleton); err != nil {
		return err
	}
	m, err := LoadManifest(env.Skeleton)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(env.Stdout, "the manifest declares %d skeleton files and every file is accounted for\n",
		len(m.Entries))
	return nil
}

func load(dir string) (*Config, *Lock, error) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, nil, err
	}
	lock, err := LoadLock(dir)
	if err != nil {
		return nil, nil, err
	}
	return cfg, lock, nil
}
