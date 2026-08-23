package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEvidenceRecordsObservedValuesAndAttempts(t *testing.T) {
	cfg := Config{BaseURL: "https://service.example", Target: "production", Window: time.Minute, Backoff: time.Second}
	results := []Result{
		{Name: "readiness", Expected: "200 with every dependency ok", Observed: "200 status=ok checks=[postgres=ok]", Attempts: 3},
		{Name: "build identity: version", Expected: "v1.2.3", Observed: "v1.2.2", Attempts: 1, Err: errors.New("live version is \"v1.2.2\"")},
	}
	block := Evidence(cfg, results)

	for _, want := range []string{
		"### Live check: production",
		"https://service.example",
		"| readiness |",
		"200 status=ok checks=[postgres=ok]",
		"| 3 |",
		"v1.2.2",
		"fail:",
		"1 of 2 assertions passed.",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("evidence does not contain %q:\n%s", want, block)
		}
	}
}

// An observed value that breaks the table is an observed value nobody reads.
func TestEvidenceCellsStayOnOneRow(t *testing.T) {
	cfg := Config{BaseURL: "https://service.example", Target: "production"}
	block := Evidence(cfg, []Result{{
		Name:     "body",
		Expected: "anything",
		Observed: "line one\nline two | with a separator",
		Attempts: 1,
	}})
	rows := 0
	for line := range strings.SplitSeq(strings.TrimSpace(block), "\n") {
		if strings.HasPrefix(line, "| ") {
			rows++
		}
	}
	if rows != 3 {
		t.Fatalf("got %d table rows, want the header, the separator, and one result:\n%s", rows, block)
	}
	if !strings.Contains(block, `line one line two \| with a separator`) {
		t.Errorf("the separator was not escaped:\n%s", block)
	}
}

func TestEvidenceMarksAnEmptyObservationExplicitly(t *testing.T) {
	block := Evidence(Config{Target: "t"}, []Result{{Name: "n", Expected: "e", Attempts: 1}})
	if !strings.Contains(block, "(empty)") {
		t.Errorf("an empty observation is not marked:\n%s", block)
	}
}

func TestWriteEvidenceAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.md")
	if err := WriteEvidence(path, nil, "first\n"); err != nil {
		t.Fatalf("WriteEvidence: %v", err)
	}
	if err := WriteEvidence(path, nil, "second\n"); err != nil {
		t.Fatalf("WriteEvidence: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if string(data) != "first\nsecond\n" {
		t.Errorf("summary = %q, want both blocks", data)
	}
}

func TestWriteEvidenceFallsBackToStdout(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteEvidence("", &buf, "block\n"); err != nil {
		t.Fatalf("WriteEvidence: %v", err)
	}
	if buf.String() != "block\n" {
		t.Errorf("stdout = %q", buf.String())
	}
}

func TestWriteEvidenceReportsAnUnwritableFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteEvidence(filepath.Join(dir, "absent", "summary.md"), nil, "block"); err == nil {
		t.Fatal("WriteEvidence accepted a path it cannot open")
	}
}

// The end-to-end path: a healthy target writes evidence and exits zero, an
// unhealthy one writes evidence and reports the failure.
func TestRunWritesEvidenceForBothOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*service)
		wantErr bool
		wantRow string
	}{
		{name: "healthy", mutate: func(*service) {}},
		{
			name:    "stale bundle",
			mutate:  func(s *service) { s.document = `<script src="/assets/index-OLD00001.js">` },
			wantErr: true,
			wantRow: "index-OLD00001.js",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := healthy()
			tc.mutate(s)
			cfg := start(t, s)
			evidencePath := filepath.Join(t.TempDir(), "evidence.md")

			values := map[string]string{
				EnvBaseURL:        cfg.BaseURL,
				EnvTarget:         "production",
				EnvExpectVersion:  cfg.ExpectVersion,
				EnvExpectCommit:   cfg.ExpectCommit,
				EnvExpectAsset:    cfg.ExpectAsset,
				EnvChecks:         "checks.yaml",
				EnvWindow:         "100ms",
				EnvBackoff:        "1ms",
				EnvRequestTimeout: "2s",
				EnvEvidence:       evidencePath,
			}
			err := run(context.Background(), env(values), nil)
			if tc.wantErr != (err != nil) {
				t.Fatalf("run error = %v, want error: %v", err, tc.wantErr)
			}

			data, readErr := os.ReadFile(evidencePath)
			if readErr != nil {
				t.Fatalf("read evidence: %v", readErr)
			}
			block := string(data)
			if !strings.Contains(block, "### Live check: production") {
				t.Errorf("evidence has no heading:\n%s", block)
			}
			if tc.wantRow != "" && !strings.Contains(block, tc.wantRow) {
				t.Errorf("evidence does not carry %q:\n%s", tc.wantRow, block)
			}
		})
	}
}

func TestRunReportsAConfigurationFailure(t *testing.T) {
	if err := run(context.Background(), env(map[string]string{}), nil); err == nil {
		t.Fatal("run accepted an empty environment")
	}
}

func TestRunReportsAMissingChecksFile(t *testing.T) {
	values := validEnv()
	values[EnvChecks] = filepath.Join(t.TempDir(), "absent.yaml")
	if err := run(context.Background(), env(values), nil); err == nil {
		t.Fatal("run accepted a checks file that does not exist")
	}
}
