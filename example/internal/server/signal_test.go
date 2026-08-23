package server

import (
	"context"
	"log/slog"
	"net/http"
	"syscall"
	"testing"
	"time"
)

// TestTerminationSignalStartsTheDrain covers the trigger side of acceptance
// criterion 2. The ordering inside the sequence is asserted in
// TestDrainMarksUnreadyBeforeItStopsAccepting, which drives the same code path
// without signalling the test process.
//
// The test is not parallel: it sends a real signal to this process, and the
// handler is installed only while Run holds it.
func TestTerminationSignalStartsTheDrain(t *testing.T) {
	logs := &syncWriter{}
	s := newServer(http.NotFoundHandler())
	s.Addr = "127.0.0.1:0"
	s.Logger = slog.New(slog.NewTextHandler(logs, nil))
	s.DrainDelay = 0
	s.GracePeriod = time.Second

	stopped := make(chan struct{})
	s.AddComponent(Component{
		Name:  "database",
		Start: func(context.Context) error { return nil },
		Stop:  func(context.Context) error { close(stopped); return nil },
	})

	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()

	deadline := time.Now().Add(5 * time.Second)
	for s.ListenAddr() == "" {
		if time.Now().After(deadline) {
			t.Fatal("server never started listening")
		}
		time.Sleep(waitInterval)
	}

	// The notify handler is installed by Run, so the process traps this rather
	// than terminating on the default action.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SIGTERM did not stop the server")
	}

	select {
	case <-stopped:
	default:
		t.Error("the component was not stopped")
	}
	if indexOf(s.lifecycle(), eventUnready) < 0 {
		t.Errorf("lifecycle = %v, want the service marked unready", s.lifecycle())
	}
}
