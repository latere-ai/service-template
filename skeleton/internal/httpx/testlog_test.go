package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// logRecord is the subset of an access or error record the tests assert on.
type logRecord struct {
	Level     string `json:"level"`
	Msg       string `json:"msg"`
	RequestID string `json:"request_id"`
	Route     string `json:"route"`
	Status    int    `json:"status"`
	Panic     bool   `json:"panic"`
	Stack     string `json:"stack"`
	Error     string `json:"error"`
}

// logCapture collects records the package emits, both through the logger
// passed in Options and through the process logger the envelope writer uses.
type logCapture struct {
	handler slog.Handler

	mu  sync.Mutex
	buf bytes.Buffer
}

// captureDefaultLogger routes the process logger into a buffer for the
// duration of the test. The envelope writer logs through it, which is where
// the message that must not reach a response body ends up.
func captureDefaultLogger(t *testing.T) *logCapture {
	t.Helper()

	c := &logCapture{}
	c.handler = slog.NewJSONHandler(&lockedWriter{c: c}, &slog.HandlerOptions{Level: slog.LevelDebug})

	previous := slog.Default()
	slog.SetDefault(slog.New(c.handler))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return c
}

// lockedWriter serializes the handler's writes, because a request served
// through the timeout stage logs from more than one goroutine.
type lockedWriter struct{ c *logCapture }

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.c.mu.Lock()
	defer w.c.mu.Unlock()
	return w.c.buf.Write(p)
}

// records parses everything captured so far.
func (c *logCapture) records(t *testing.T) []logRecord {
	t.Helper()

	c.mu.Lock()
	raw := c.buf.String()
	c.mu.Unlock()

	var out []logRecord
	for line := range strings.SplitSeq(strings.TrimSpace(raw), "\n") {
		if line == "" {
			continue
		}
		var r logRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("parse the log line %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

// find returns the first record matching want and fails when there is none.
func (c *logCapture) find(t *testing.T, want func(logRecord) bool) logRecord {
	t.Helper()

	for _, r := range c.records(t) {
		if want(r) {
			return r
		}
	}
	t.Fatalf("no log record matched; captured:\n%s", c.text())
	return logRecord{}
}

// text returns the raw capture, for a failure message.
func (c *logCapture) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}
