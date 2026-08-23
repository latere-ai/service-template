package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestServerSetsEveryConnectionTimeout covers acceptance criterion 6: read,
// write, and idle timeouts are always set on the HTTP server.
func TestServerSetsEveryConnectionTimeout(t *testing.T) {
	srv := newServer(nil).httpServer(context.Background())

	timeouts := map[string]time.Duration{
		"ReadTimeout":       srv.ReadTimeout,
		"ReadHeaderTimeout": srv.ReadHeaderTimeout,
		"WriteTimeout":      srv.WriteTimeout,
		"IdleTimeout":       srv.IdleTimeout,
	}
	for name, d := range timeouts {
		if d <= 0 {
			t.Errorf("%s = %v, want a positive deadline", name, d)
		}
	}
}

// TestReadHeaderTimeoutClosesASilentClient proves the header deadline is wired
// to the listener and not only stored on the struct.
func TestReadHeaderTimeoutClosesASilentClient(t *testing.T) {
	h := newHarness(t, http.NotFoundHandler())
	h.ReadHeaderTimeout = 100 * time.Millisecond
	h.ReadTimeout = 100 * time.Millisecond
	h.start()

	conn, err := net.DialTimeout("tcp", h.ListenAddr(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close connection: %v", err)
		}
	}()

	// The client sends the request line and then nothing. Without a header
	// deadline the connection stays open until the client goes away.
	if _, err := conn.Write([]byte("GET /livez HTTP/1.1\r\n")); err != nil {
		t.Fatalf("write request line: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	// A server that enforces the deadline either answers 408 or closes the
	// connection. A server without one blocks here until the read deadline.
	buf := make([]byte, 128)
	n, err := conn.Read(buf)
	if err == nil && !strings.Contains(string(buf[:n]), "408") {
		t.Fatalf("server kept the connection open, read %q", string(buf[:n]))
	}
}

// TestComponentsStartInOrderAndStopInReverse covers acceptance criterion 4.
func TestComponentsStartInOrderAndStopInReverse(t *testing.T) {
	h := newHarness(t, http.NotFoundHandler())
	for _, name := range []string{"database", "cache", "queue"} {
		h.AddComponent(Component{
			Name:  name,
			Start: func(context.Context) error { return nil },
			Stop:  func(context.Context) error { return nil },
		})
	}
	h.start()
	h.trigger()

	if err := h.wait(); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	got := componentEvents(h.lifecycle())
	want := []string{
		"start:database", "start:cache", "start:queue",
		"stop:queue", "stop:cache", "stop:database",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("component sequence = %v, want %v", got, want)
	}
}

// TestFailedStartStopsWhatAlreadyStarted proves a component that cannot start
// does not leave the components before it running.
func TestFailedStartStopsWhatAlreadyStarted(t *testing.T) {
	s := newServer(http.NotFoundHandler())
	s.Addr = "127.0.0.1:0"
	s.DrainDelay = 0

	wantErr := errors.New("dial refused")
	s.AddComponent(Component{
		Name:  "database",
		Start: func(context.Context) error { return nil },
		Stop:  func(context.Context) error { return nil },
	})
	s.AddComponent(Component{
		Name:  "cache",
		Start: func(context.Context) error { return wantErr },
		Stop:  func(context.Context) error { return nil },
	})
	s.AddComponent(Component{
		Name:  "queue",
		Start: func(context.Context) error { t.Error("queue started after cache failed"); return nil },
	})

	err := s.Run(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run = %v, want it to wrap %v", err, wantErr)
	}
	if !strings.Contains(err.Error(), "cache") {
		t.Errorf("Run error = %q, want the failing component named", err)
	}
	if got, want := fmt.Sprint(componentEvents(s.lifecycle())), fmt.Sprint([]string{"start:database", "stop:database"}); got != want {
		t.Fatalf("component sequence = %v, want %v", got, want)
	}
}

// TestDrainMarksUnreadyBeforeItStopsAccepting covers acceptance criterion 2.
// The proof is behavioural: during the drain window the listener still answers
// and the answer is already 503.
func TestDrainMarksUnreadyBeforeItStopsAccepting(t *testing.T) {
	h := newHarness(t, http.NotFoundHandler())
	h.AddReadyCheck("database", func(context.Context) error { return nil })
	// The gate, not the clock, releases the drain, so a long delay costs the
	// test nothing and the asserted value is unambiguous.
	h.DrainDelay = 42 * time.Second
	h.holdDrain()
	h.start()

	if resp, _ := h.get(ReadyPath); resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s before shutdown = %d, want 200", ReadyPath, resp.StatusCode)
	}

	h.trigger()

	// The listener is still open here, which is the whole point of the delay:
	// a request the load balancer already dispatched still gets a response.
	body := h.waitStatus(ReadyPath, http.StatusServiceUnavailable)
	if !strings.Contains(body, statusDraining) {
		t.Errorf("body = %q, want it to report %q", body, statusDraining)
	}
	if resp, _ := h.get(LivePath); resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s during drain = %d, want 200", LivePath, resp.StatusCode)
	}

	h.releaseDrain()
	if err := h.wait(); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	if got := h.drainWait(); got != h.DrainDelay {
		t.Errorf("drain waited %v, want the configured delay %v", got, h.DrainDelay)
	}

	events := h.lifecycle()
	unready, stopAccepting := indexOf(events, eventUnready), indexOf(events, eventStopAccepting)
	if unready < 0 || stopAccepting < 0 {
		t.Fatalf("lifecycle = %v, want both %q and %q", events, eventUnready, eventStopAccepting)
	}
	if unready >= stopAccepting {
		t.Fatalf("lifecycle = %v, want %q strictly before %q", events, eventUnready, eventStopAccepting)
	}
}

// TestInFlightRequestFinishesInsideGracePeriod covers the first half of
// acceptance criterion 3.
func TestInFlightRequestFinishesInsideGracePeriod(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		if _, err := w.Write([]byte("done")); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	h.GracePeriod = 10 * time.Second
	h.start()

	result := make(chan string, 1)
	go func() {
		_, body := h.get("/work")
		result <- body
	}()
	<-entered

	h.trigger()
	// The request is in flight when the drain starts. Releasing it inside the
	// grace period must let it finish with its response intact.
	time.Sleep(20 * time.Millisecond)
	close(release)

	select {
	case body := <-result:
		if body != "done" {
			t.Fatalf("body = %q, want %q", body, "done")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight request never completed")
	}
	if err := h.wait(); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if h.logContains("cancelling in-flight requests") {
		t.Error("a request that finished inside the grace period was reported as cut off")
	}
}

// TestInFlightRequestIsCancelledAfterGracePeriod covers the second half of
// acceptance criterion 3: the request context ends and the count is logged.
func TestInFlightRequestIsCancelledAfterGracePeriod(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(cancelled)
	}))
	h.GracePeriod = 100 * time.Millisecond

	// The component stop blocks until the test has seen the cancellation, so a
	// server that only cancelled its requests on the way out of Run, after the
	// components were closed, would deadlock here instead of passing. A handler
	// still running while its database pool closes is the case this pins.
	stopGate := make(chan struct{})
	h.AddComponent(Component{
		Name:  "database",
		Start: func(context.Context) error { return nil },
		Stop:  func(context.Context) error { <-stopGate; return nil },
	})
	h.start()

	go func() {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h.url("/slow"), nil)
		if err != nil {
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			if err := resp.Body.Close(); err != nil {
				t.Errorf("close body: %v", err)
			}
		}
	}()
	<-entered

	h.trigger()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		close(stopGate)
		t.Fatal("the request context was not cancelled before the components stopped")
	}
	close(stopGate)

	if err := h.wait(); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if !h.logContains("cancelling in-flight requests") {
		t.Fatalf("logs = %q, want the cut-off record", h.logs.String())
	}
	if !h.logContains("count=1") {
		t.Fatalf("logs = %q, want one request counted", h.logs.String())
	}
}

// componentEvents keeps the start and stop records and drops the rest, so an
// assertion on component ordering does not restate the drain sequence.
func componentEvents(events []string) []string {
	kept := make([]string, 0, len(events))
	for _, e := range events {
		if strings.HasPrefix(e, "start:") || strings.HasPrefix(e, "stop:") {
			kept = append(kept, e)
		}
	}
	return kept
}

// TestRunReportsAListenFailure proves a port that cannot be bound is reported
// and does not leave the components running.
func TestRunReportsAListenFailure(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer func() {
		if err := taken.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	}()

	s := newServer(http.NotFoundHandler())
	s.Addr = taken.Addr().String()
	stopped := false
	s.AddComponent(Component{
		Name:  "database",
		Start: func(context.Context) error { return nil },
		Stop:  func(context.Context) error { stopped = true; return nil },
	})

	err = s.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "listen on") {
		t.Fatalf("Run = %v, want a listen failure", err)
	}
	if !stopped {
		t.Error("the started component was not stopped after the listener failed")
	}
}

// TestStopReportsEveryComponentFailure proves one failing stop does not hide
// the others: a leaked pool is worse than a second error.
func TestStopReportsEveryComponentFailure(t *testing.T) {
	first := errors.New("pool close failed")
	second := errors.New("consumer close failed")

	s := newServer(nil)
	started := []Component{
		{Name: "database", Stop: func(context.Context) error { return first }},
		{Name: "queue", Stop: func(context.Context) error { return second }},
		{Name: "cache"},
	}

	err := s.stopComponents(context.Background(), started)
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("stopComponents = %v, want both failures", err)
	}
}

// TestDefaultsCoverAnUnsetServer keeps a zero-valued field from becoming an
// unbounded listen address or a nil logger at run time.
func TestDefaultsCoverAnUnsetServer(t *testing.T) {
	s := &Server{}
	if s.listenTarget() != DefaultAddr {
		t.Errorf("listenTarget = %q, want %q", s.listenTarget(), DefaultAddr)
	}
	if s.logger() == nil {
		t.Error("logger = nil, want the default logger")
	}
}

// TestSleepContextIsBoundedByTheContext proves the drain delay does not
// outlive a context that has already ended.
func TestSleepContextIsBoundedByTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	sleepContext(ctx, time.Minute)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("sleepContext took %v after the context ended", elapsed)
	}

	start = time.Now()
	sleepContext(context.Background(), 0)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("sleepContext with no delay took %v", elapsed)
	}
}
