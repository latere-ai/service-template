// Package server holds the service runtime: the component run group, the
// health and build-identity endpoints, and the shutdown sequence.
//
// It exists so the lifecycle is written once. A service that wires its own
// goroutines and channels in main gets a different drain behaviour per
// repository, and the failure modes, dropped requests during a rolling update
// and a replica marked ready before its dependencies answer, are invisible
// until they happen in production.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Default timeouts. Read, write, and idle timeouts are always set: a server
// without them holds a connection open until the peer goes away, which is a
// denial-of-service surface reachable without authentication.
const (
	// DefaultReadTimeout bounds reading the request, headers and body.
	DefaultReadTimeout = 15 * time.Second
	// DefaultReadHeaderTimeout bounds reading the request headers alone, so a
	// slow-header client is cut off before it occupies a connection.
	DefaultReadHeaderTimeout = 5 * time.Second
	// DefaultWriteTimeout bounds writing the response.
	DefaultWriteTimeout = 30 * time.Second
	// DefaultIdleTimeout bounds how long a keep-alive connection may sit idle.
	DefaultIdleTimeout = 120 * time.Second

	// DefaultDrainDelay is the pause between marking the service unready and
	// closing the listener. Readiness propagation to a load balancer is not
	// instantaneous, so a service that stops accepting the moment it receives
	// SIGTERM rejects requests already dispatched to it.
	DefaultDrainDelay = 5 * time.Second
	// DefaultGracePeriod is the window in-flight requests get after the
	// listener closes.
	DefaultGracePeriod = 30 * time.Second
	// DefaultStopTimeout bounds one component's stop function.
	DefaultStopTimeout = 15 * time.Second
	// DefaultReadyCheckTimeout bounds one readiness check.
	DefaultReadyCheckTimeout = 2 * time.Second

	// DefaultAddr is the listen address used when configuration carries none.
	DefaultAddr = ":8080"
)

// Lifecycle events recorded in order. The recorded sequence is what makes the
// ordering guarantees, unready before the listener closes and components
// stopped in reverse, assertable, since neither has an external observer once
// the process is gone.
const (
	eventReady           = "ready"
	eventUnready         = "unready"
	eventStopAccepting   = "stop-accepting"
	eventRequestsDrained = "requests-drained"
)

// Component is one dependency with a lifecycle: a database pool, a queue
// consumer, a metrics exporter.
//
// Start is called synchronously and in registration order. It returns when the
// component is running or when it has failed; work that outlives the call
// belongs in a goroutine the component owns and Stop waits for. Stop is called
// in reverse registration order with a bounded context.
type Component struct {
	Name  string
	Start func(context.Context) error
	Stop  func(context.Context) error
}

// readyCheck is one registered readiness probe.
type readyCheck struct {
	name string
	fn   func(context.Context) error
}

// Server runs the HTTP surface and the registered components as one unit.
//
// The exported timeout fields carry the defaults above. A caller that reads
// them from configuration assigns them after New and before Run.
type Server struct {
	// Addr is the listen address, for example ":8080".
	Addr string

	// HTTP connection timeouts.
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration

	// Shutdown budgets.
	DrainDelay        time.Duration
	GracePeriod       time.Duration
	StopTimeout       time.Duration
	ReadyCheckTimeout time.Duration

	// Logger receives the lifecycle records. It defaults to slog.Default().
	Logger *slog.Logger

	handler http.Handler

	mu         sync.Mutex
	components []Component
	checks     []readyCheck
	listenAddr string
	events     []string

	ready    atomic.Bool
	inflight atomic.Int64

	// sleep waits out the drain delay. It is a field so a test drives the
	// drain window instead of racing a wall-clock timer.
	sleep func(context.Context, time.Duration)
	// notify derives the context that shutdown starts from. It is a field so a
	// test triggers the sequence without sending a process signal.
	notify func(context.Context) (context.Context, context.CancelFunc)
}

// newServer builds a server with the default budgets around h.
func newServer(h http.Handler) *Server {
	if h == nil {
		h = http.NotFoundHandler()
	}
	return &Server{
		Addr:              DefaultAddr,
		ReadTimeout:       DefaultReadTimeout,
		ReadHeaderTimeout: DefaultReadHeaderTimeout,
		WriteTimeout:      DefaultWriteTimeout,
		IdleTimeout:       DefaultIdleTimeout,
		DrainDelay:        DefaultDrainDelay,
		GracePeriod:       DefaultGracePeriod,
		StopTimeout:       DefaultStopTimeout,
		ReadyCheckTimeout: DefaultReadyCheckTimeout,
		Logger:            slog.Default(),
		handler:           h,
		sleep:             sleepContext,
		notify:            notifyShutdown,
	}
}

// AddComponent registers a component. Components start in registration order
// and stop in reverse.
func (s *Server) AddComponent(c Component) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.components = append(s.components, c)
}

// AddReadyCheck registers a readiness check. The name appears in the /readyz
// body, so a failing probe says which dependency is unreachable.
func (s *Server) AddReadyCheck(name string, fn func(context.Context) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks = append(s.checks, readyCheck{name: name, fn: fn})
}

// ListenAddr reports the bound address, which differs from Addr when the
// configured port is 0. It is empty before the listener opens.
func (s *Server) ListenAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listenAddr
}

// Run starts the components, serves HTTP, and blocks until the context is
// cancelled, a termination signal arrives, or the listener fails. It then runs
// the drain sequence and stops the components in reverse order.
func (s *Server) Run(ctx context.Context) error {
	runCtx, stopNotify := s.notify(ctx)
	defer stopNotify()

	// Shutdown work outlives the cancelled run context: the drain delay, the
	// grace period, and the component stops all need a live context after the
	// signal that started them.
	shutdownCtx := context.WithoutCancel(ctx)

	started, err := s.startComponents(runCtx)
	if err != nil {
		return errors.Join(err, s.stopComponents(shutdownCtx, started))
	}

	// requestCtx is the parent of every connection context, so cancelling it
	// cancels the handlers still running when the grace period expires.
	requestCtx, cancelRequests := context.WithCancel(shutdownCtx)
	defer cancelRequests()

	srv := s.httpServer(requestCtx)

	// The bind honours the run context, so a shutdown signal that arrives
	// while the address is still resolving stops here rather than after the
	// socket is open and the process is accepting connections it will drop.
	var lc net.ListenConfig
	ln, err := lc.Listen(runCtx, "tcp", s.listenTarget())
	if err != nil {
		return errors.Join(fmt.Errorf("listen on %s: %w", s.listenTarget(), err),
			s.stopComponents(shutdownCtx, started))
	}
	s.setListenAddr(ln.Addr().String())
	s.ready.Store(true)
	s.record(eventReady)
	s.logger().InfoContext(ctx, "serving", "addr", ln.Addr().String())

	serveErr := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case err := <-serveErr:
		s.ready.Store(false)
		s.record(eventUnready)
		return errors.Join(err, s.stopComponents(shutdownCtx, started))
	case <-runCtx.Done():
	}

	drainErr := s.drain(shutdownCtx, srv, cancelRequests, serveErr)
	return errors.Join(drainErr, s.stopComponents(shutdownCtx, started))
}

// httpServer builds the HTTP server. Every connection timeout is set here:
// a zero value means no deadline, so one omission reopens the slow-client
// surface the defaults exist to close.
func (s *Server) httpServer(requestCtx context.Context) *http.Server {
	return &http.Server{
		Handler:           s.routes(),
		ReadTimeout:       s.ReadTimeout,
		ReadHeaderTimeout: s.ReadHeaderTimeout,
		WriteTimeout:      s.WriteTimeout,
		IdleTimeout:       s.IdleTimeout,
		BaseContext:       func(net.Listener) context.Context { return requestCtx },
		ErrorLog:          slog.NewLogLogger(s.logger().Handler(), slog.LevelWarn),
	}
}

// drain runs the shutdown sequence: mark unready, wait out the propagation
// delay, stop accepting, wait for in-flight requests, then cancel what is left.
func (s *Server) drain(ctx context.Context, srv *http.Server, cancelRequests context.CancelFunc, serveErr <-chan error) error {
	s.ready.Store(false)
	s.record(eventUnready)
	s.logger().InfoContext(ctx, "draining", "delay", s.DrainDelay, "grace_period", s.GracePeriod)

	s.sleep(ctx, s.DrainDelay)

	graceCtx, cancelGrace := context.WithTimeout(ctx, s.GracePeriod)
	defer cancelGrace()

	// Shutdown closes the listener first, so the record marks the point where
	// the service stops accepting connections.
	s.record(eventStopAccepting)
	err := srv.Shutdown(graceCtx)
	if err != nil {
		// The grace period expired. The requests still running are cancelled
		// through their context and the count is logged, so the budget can be
		// tuned from evidence rather than guessed.
		cut := s.inflight.Load()
		s.logger().WarnContext(ctx, "grace period expired, cancelling in-flight requests",
			"count", cut, "grace_period", s.GracePeriod)
		cancelRequests()
		if closeErr := srv.Close(); closeErr != nil {
			s.logger().WarnContext(ctx, "close listener", "error", closeErr.Error())
		}
	}
	s.record(eventRequestsDrained)

	// Serve returns as soon as the listener closes; reading it keeps the
	// goroutine from outliving Run.
	if serveShutdownErr := <-serveErr; serveShutdownErr != nil {
		return serveShutdownErr
	}
	// A cut-off request is reported through the log record above, not as a
	// process failure: the sequence itself completed.
	return nil
}

// startComponents starts components in registration order and returns the ones
// that started. A failure stops what already started, in reverse.
func (s *Server) startComponents(ctx context.Context) ([]Component, error) {
	s.mu.Lock()
	components := make([]Component, len(s.components))
	copy(components, s.components)
	s.mu.Unlock()

	started := make([]Component, 0, len(components))
	for _, c := range components {
		if c.Start == nil {
			started = append(started, c)
			continue
		}
		if err := c.Start(ctx); err != nil {
			return started, fmt.Errorf("start component %s: %w", c.Name, err)
		}
		s.record("start:" + c.Name)
		started = append(started, c)
	}
	return started, nil
}

// stopComponents stops the given components in reverse order. Every stop runs
// even when an earlier one fails, because a leaked pool is worse than a second
// error.
func (s *Server) stopComponents(ctx context.Context, started []Component) error {
	var errs []error
	for _, c := range slices.Backward(started) {
		if c.Stop == nil {
			continue
		}
		stopCtx, cancel := context.WithTimeout(ctx, s.StopTimeout)
		err := c.Stop(stopCtx)
		cancel()
		s.record("stop:" + c.Name)
		if err != nil {
			errs = append(errs, fmt.Errorf("stop component %s: %w", c.Name, err))
		}
	}
	return errors.Join(errs...)
}

// countInFlight tracks the requests a handler is currently serving, which is
// the number reported when the grace period expires.
func (s *Server) countInFlight(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.inflight.Add(1)
		defer s.inflight.Add(-1)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logger() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}

func (s *Server) listenTarget() string {
	if s.Addr == "" {
		return DefaultAddr
	}
	return s.Addr
}

func (s *Server) setListenAddr(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listenAddr = addr
}

// record appends a lifecycle event.
func (s *Server) record(event string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

// lifecycle reports the recorded events in order.
func (s *Server) lifecycle() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

// sleepContext waits for d, or until ctx ends.
func sleepContext(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
