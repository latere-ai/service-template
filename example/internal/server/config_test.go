package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/example/reference-service/internal/config"
)

// TestNewTakesTheAddressFromConfiguration fixes the one value the runtime
// reads from configuration at construction.
func TestNewTakesTheAddressFromConfiguration(t *testing.T) {
	s := New(&config.Config{Addr: "127.0.0.1:9999"}, http.NotFoundHandler())
	if s.Addr != "127.0.0.1:9999" {
		t.Errorf("Addr = %q, want the configured address", s.Addr)
	}
	if s.handler == nil {
		t.Error("handler = nil, want the application handler")
	}
}

// TestNewFallsBackToTheDefaultAddress keeps a server constructible before a
// configuration file exists, which is what a test and a local run do.
func TestNewFallsBackToTheDefaultAddress(t *testing.T) {
	s := New(nil, nil)
	if s.Addr != DefaultAddr {
		t.Errorf("Addr = %q, want %q", s.Addr, DefaultAddr)
	}
	if s.handler == nil {
		t.Error("handler = nil, want a not-found handler")
	}
}

// TestNewTakesEveryBudgetFromConfiguration proves the configured deadlines
// reach the HTTP server rather than being silently replaced by the defaults.
func TestNewTakesEveryBudgetFromConfiguration(t *testing.T) {
	cfg := &config.Config{
		Addr:              "127.0.0.1:0",
		ReadTimeout:       11 * time.Second,
		ReadHeaderTimeout: 12 * time.Second,
		WriteTimeout:      13 * time.Second,
		IdleTimeout:       14 * time.Second,
		DrainDelay:        15 * time.Second,
		GracePeriod:       16 * time.Second,
	}
	s := New(cfg, http.NotFoundHandler())

	srv := s.httpServer(context.Background())
	budgets := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"ReadTimeout", srv.ReadTimeout, cfg.ReadTimeout},
		{"ReadHeaderTimeout", srv.ReadHeaderTimeout, cfg.ReadHeaderTimeout},
		{"WriteTimeout", srv.WriteTimeout, cfg.WriteTimeout},
		{"IdleTimeout", srv.IdleTimeout, cfg.IdleTimeout},
		{"DrainDelay", s.DrainDelay, cfg.DrainDelay},
		{"GracePeriod", s.GracePeriod, cfg.GracePeriod},
	}
	for _, b := range budgets {
		if b.got != b.want {
			t.Errorf("%s = %v, want %v", b.name, b.got, b.want)
		}
	}
}

// TestNewKeepsDefaultsForUnsetBudgets covers a configuration that carries no
// deadline: the server still sets one.
func TestNewKeepsDefaultsForUnsetBudgets(t *testing.T) {
	s := New(&config.Config{Addr: "127.0.0.1:0"}, nil)

	srv := s.httpServer(context.Background())
	if srv.ReadTimeout != DefaultReadTimeout || srv.WriteTimeout != DefaultWriteTimeout || srv.IdleTimeout != DefaultIdleTimeout {
		t.Fatalf("timeouts = %v/%v/%v, want the package defaults", srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout)
	}
}
