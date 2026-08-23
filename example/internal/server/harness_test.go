package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// waitInterval is the poll step for a condition a test cannot observe directly,
// such as the listener becoming reachable.
const waitInterval = time.Millisecond

// syncWriter serializes the log records a test reads while the server writes.
type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// harness runs a server on a loopback port with the shutdown trigger and the
// drain delay under the test's control.
type harness struct {
	*Server

	t         *testing.T
	logs      *syncWriter
	trigger   context.CancelFunc
	drainGate chan struct{}
	done      chan error

	mu       sync.Mutex
	sleptFor time.Duration
}

// recordSleep keeps the delay the drain sequence asked for, so a test asserts
// the configured value reached the call and not only that a wait happened.
func (h *harness) recordSleep(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sleptFor = d
}

// drainWait reports the delay the drain sequence asked for.
func (h *harness) drainWait() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sleptFor
}

// newHarness builds a server around h. The drain gate starts open, so a test
// that does not care about the drain window is not delayed by it.
func newHarness(t *testing.T, h http.Handler) *harness {
	t.Helper()

	logs := &syncWriter{}
	s := newServer(h)
	s.Addr = "127.0.0.1:0"
	s.Logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s.DrainDelay = 0
	s.GracePeriod = 5 * time.Second
	s.StopTimeout = time.Second
	s.ReadyCheckTimeout = time.Second

	gate := make(chan struct{})
	close(gate)

	hs := &harness{Server: s, t: t, logs: logs, drainGate: gate, done: make(chan error, 1)}
	// The drain delay is a gate rather than a timer, so the ordering assertions
	// do not race a wall clock on a loaded machine.
	s.sleep = func(_ context.Context, d time.Duration) {
		hs.recordSleep(d)
		<-hs.drainGate
	}
	return hs
}

// holdDrain replaces the open gate with one the test closes itself.
func (h *harness) holdDrain() {
	h.drainGate = make(chan struct{})
}

// releaseDrain lets the drain delay finish.
func (h *harness) releaseDrain() {
	close(h.drainGate)
}

// start runs the server and blocks until it is listening.
func (h *harness) start() {
	h.t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	h.trigger = cancel
	// The signal wiring is replaced so a test drives shutdown by cancelling
	// this context instead of signalling the whole test process.
	h.notify = func(parent context.Context) (context.Context, context.CancelFunc) {
		return context.WithCancel(parent)
	}

	go func() { h.done <- h.Run(ctx) }()
	h.t.Cleanup(func() {
		cancel()
		select {
		case <-h.drainGate:
		default:
			close(h.drainGate)
		}
		select {
		case <-h.done:
		case <-time.After(10 * time.Second):
			h.t.Error("Run did not return")
		}
	})
	h.waitListening()
}

// waitListening blocks until the listener has a bound address.
func (h *harness) waitListening() {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.ListenAddr() != "" {
			return
		}
		time.Sleep(waitInterval)
	}
	h.t.Fatal("server never started listening")
}

// wait returns the value Run reported.
func (h *harness) wait() error {
	h.t.Helper()
	select {
	case err := <-h.done:
		h.done <- err
		return err
	case <-time.After(10 * time.Second):
		h.t.Fatal("Run did not return")
		return nil
	}
}

func (h *harness) url(path string) string {
	return "http://" + h.ListenAddr() + path
}

// get performs a request against the running server.
func (h *harness) get(path string) (*http.Response, string) {
	h.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h.url(path), nil)
	if err != nil {
		h.t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("GET %s: %v", path, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			h.t.Errorf("close body: %v", err)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("read body: %v", err)
	}
	return resp, string(body)
}

// waitStatus polls a path until it answers with the wanted status.
func (h *harness) waitStatus(path string, want int) string {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last int
	for time.Now().Before(deadline) {
		resp, body := h.get(path)
		if resp.StatusCode == want {
			return body
		}
		last = resp.StatusCode
		time.Sleep(waitInterval)
	}
	h.t.Fatalf("GET %s never returned %d, last status %d", path, want, last)
	return ""
}

// indexOf reports where an event sits in the recorded lifecycle.
func indexOf(events []string, event string) int {
	for i, e := range events {
		if e == event {
			return i
		}
	}
	return -1
}

// logContains reports whether the captured records hold the substring.
func (h *harness) logContains(substr string) bool {
	return strings.Contains(h.logs.String(), substr)
}
