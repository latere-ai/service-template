package worker

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

func TestParseInvocation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		args    []string
		want    Invocation
		wantErr string
	}{
		{
			name: "no arguments serve",
			args: nil,
			want: Invocation{Mode: ModeServe},
		},
		{
			name: "work",
			args: []string{"-mode=work"},
			want: Invocation{Mode: ModeWork},
		},
		{
			name: "serve and work in one process",
			args: []string{"-mode", "all"},
			want: Invocation{Mode: ModeAll},
		},
		{
			name: "a named job selects the job mode",
			args: []string{"-job=backfill"},
			want: Invocation{Mode: ModeJob, Job: "backfill"},
		},
		{
			name: "the job mode stated with its job",
			args: []string{"-mode=job", "-job=backfill"},
			want: Invocation{Mode: ModeJob, Job: "backfill"},
		},
		{
			name:    "the job mode without a job",
			args:    []string{"-mode=job"},
			wantErr: "needs -job",
		},
		{
			name:    "a job named against another mode",
			args:    []string{"-mode=serve", "-job=backfill"},
			wantErr: "does not run a single job",
		},
		{
			name:    "an unknown mode",
			args:    []string{"-mode=migrate"},
			wantErr: "unknown mode",
		},
		{
			name:    "a positional argument",
			args:    []string{"backfill"},
			wantErr: "unexpected argument",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseInvocation(tc.args, io.Discard)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseInvocation(%v) error = %v, want one containing %q", tc.args, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseInvocation(%v): %v", tc.args, err)
			}
			if got != tc.want {
				t.Fatalf("ParseInvocation(%v) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}

func TestParseInvocationHelpIsNotAFailure(t *testing.T) {
	t.Parallel()
	out := &strings.Builder{}

	if _, err := ParseInvocation([]string{"-h"}, out); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("ParseInvocation(-h) error = %v, want flag.ErrHelp", err)
	}
	for _, m := range Modes {
		if !strings.Contains(out.String(), string(m)) {
			t.Errorf("the help text does not name the %q mode:\n%s", m, out.String())
		}
	}
}

func TestInvocationSelectsWhatTheProcessDoes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		mode                      Mode
		serves, works, runsOneJob bool
	}{
		{ModeServe, true, false, false},
		{ModeWork, false, true, false},
		{ModeAll, true, true, false},
		{ModeJob, false, false, true},
	}
	for _, tc := range cases {
		inv := Invocation{Mode: tc.mode}
		if got := inv.Serves(); got != tc.serves {
			t.Errorf("%s Serves = %v, want %v", tc.mode, got, tc.serves)
		}
		if got := inv.Works(); got != tc.works {
			t.Errorf("%s Works = %v, want %v", tc.mode, got, tc.works)
		}
		if got := inv.RunsJob(); got != tc.runsOneJob {
			t.Errorf("%s RunsJob = %v, want %v", tc.mode, got, tc.runsOneJob)
		}
	}
}
