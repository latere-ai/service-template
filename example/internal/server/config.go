package server

import (
	"net/http"
	"time"

	"github.com/example/reference-service/internal/config"
)

// New builds the runtime for one service from the loaded configuration and the
// application handler.
//
// The handler serves everything the probe paths do not claim. Every budget
// keeps the default in this package when configuration carries no value, so a
// server is constructible before a configuration file exists.
func New(cfg *config.Config, h http.Handler) *Server {
	s := newServer(h)
	if cfg == nil {
		return s
	}
	if cfg.Addr != "" {
		s.Addr = cfg.Addr
	}
	s.ReadTimeout = positive(cfg.ReadTimeout, s.ReadTimeout)
	s.ReadHeaderTimeout = positive(cfg.ReadHeaderTimeout, s.ReadHeaderTimeout)
	s.WriteTimeout = positive(cfg.WriteTimeout, s.WriteTimeout)
	s.IdleTimeout = positive(cfg.IdleTimeout, s.IdleTimeout)
	s.GracePeriod = positive(cfg.GracePeriod, s.GracePeriod)
	// A zero drain delay is a legitimate choice for a single-replica local run,
	// so it is taken as given rather than replaced by the default.
	if cfg.DrainDelay >= 0 {
		s.DrainDelay = cfg.DrainDelay
	}
	return s
}

// positive keeps the configured value when it carries a deadline and the
// default when it does not, because a zero timeout means no deadline at all.
func positive(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}
