package worker

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Mode selects what a process does with the image it was started from. One
// image therefore runs the web deployment, the worker deployment, and a one-off
// task, and moving work between processes is a deployment change rather than a
// code change.
type Mode string

// The modes.
const (
	// ModeServe handles requests only.
	ModeServe Mode = "serve"
	// ModeWork runs scheduled and continuous jobs only.
	ModeWork Mode = "work"
	// ModeAll serves and works in one process, which is what a small
	// deployment wants and what a large one splits without changing code.
	ModeAll Mode = "all"
	// ModeJob runs one named job once and exits.
	ModeJob Mode = "job"
)

// Modes lists every mode in the order the flag help prints them.
var Modes = []Mode{ModeServe, ModeWork, ModeAll, ModeJob}

// Invocation is what the command line asked the process to do.
type Invocation struct {
	// Mode is the selected mode.
	Mode Mode
	// Job is the job to run once. It is set only for [ModeJob].
	Job string
}

// Serves reports whether the process handles requests.
func (i Invocation) Serves() bool { return i.Mode == ModeServe || i.Mode == ModeAll }

// Works reports whether the process runs scheduled and continuous jobs.
func (i Invocation) Works() bool { return i.Mode == ModeWork || i.Mode == ModeAll }

// RunsJob reports whether the process runs one named job and exits.
func (i Invocation) RunsJob() bool { return i.Mode == ModeJob }

// ParseInvocation reads the mode flags from args, which is os.Args[1:] for a
// process. Flag output and errors are written to out.
//
// The default mode is serve, so an image started with no arguments behaves the
// way a deployment expects a service to behave. Naming a job selects job mode
// on its own, because "-job backfill" states the intent completely.
//
// It reports flag.ErrHelp when help was requested, which is a successful exit
// rather than a failure.
func ParseInvocation(args []string, out io.Writer) (Invocation, error) {
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	fs.SetOutput(out)

	mode := fs.String("mode", string(ModeServe),
		fmt.Sprintf("execution mode: %s", strings.Join(modeNames(), ", ")))
	job := fs.String("job", "", "job to run once, which selects the job mode")

	if err := fs.Parse(args); err != nil {
		return Invocation{}, err
	}
	if fs.NArg() > 0 {
		return Invocation{}, fmt.Errorf("worker: unexpected argument %q", fs.Arg(0))
	}

	inv := Invocation{Mode: Mode(*mode), Job: strings.TrimSpace(*job)}
	if !validMode(inv.Mode) {
		return Invocation{}, fmt.Errorf("worker: unknown mode %q, the modes are %s",
			*mode, strings.Join(modeNames(), ", "))
	}

	// A named job selects job mode unless another mode was stated, in which
	// case the two instructions disagree and neither is guessed.
	explicit := explicitMode(args)
	switch {
	case inv.Job != "" && !explicit:
		inv.Mode = ModeJob
	case inv.Job != "" && inv.Mode != ModeJob:
		return Invocation{}, fmt.Errorf("worker: mode %q does not run a single job, drop -job or use -mode=%s",
			inv.Mode, ModeJob)
	case inv.Job == "" && inv.Mode == ModeJob:
		return Invocation{}, fmt.Errorf("worker: mode %s needs -job with a job name", ModeJob)
	}
	return inv, nil
}

// explicitMode reports whether the arguments carry the mode flag, which
// separates the default from the same value stated on the command line.
func explicitMode(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "-mode" || a == "--mode" ||
			strings.HasPrefix(a, "-mode=") || strings.HasPrefix(a, "--mode=") {
			return true
		}
	}
	return false
}

// validMode reports whether m is one of the modes.
func validMode(m Mode) bool {
	return slices.Contains(Modes, m)
}

// modeNames reports the modes as strings for a message.
func modeNames() []string {
	out := make([]string, 0, len(Modes))
	for _, m := range Modes {
		out = append(out, string(m))
	}
	return out
}
