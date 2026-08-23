package httpx

import (
	"bufio"
	"net"
	"net/http"
)

// recorder observes the status and the body size of a response so the access
// log and the metrics stage can report them. Both stages need the same two
// numbers, so one wrapper serves both and the response is wrapped once.
type recorder struct {
	http.ResponseWriter
	status  int
	written int64
	// hijacked reports that the connection left the HTTP lifecycle, which
	// makes the status meaningless for that request.
	hijacked bool
}

// newRecorder wraps w, or returns the existing recorder when one is already in
// the chain, so two stages share one set of counters.
func newRecorder(w http.ResponseWriter) *recorder {
	if rec, ok := w.(*recorder); ok {
		return rec
	}
	return &recorder{ResponseWriter: w}
}

func (rec *recorder) WriteHeader(status int) {
	if rec.status == 0 {
		rec.status = status
	}
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *recorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	n, err := rec.ResponseWriter.Write(b)
	rec.written += int64(n)
	return n, err
}

// Status reports the status the handler produced. A handler that returned
// without writing produced the 200 net/http writes for it.
func (rec *recorder) Status() int {
	if rec.status == 0 {
		return http.StatusOK
	}
	return rec.status
}

// wroteHeader reports whether the response is already committed, which decides
// whether a later stage may still replace it with an error envelope.
func (rec *recorder) wroteHeader() bool { return rec.status != 0 }

// Unwrap exposes the wrapped writer to http.ResponseController, which is how
// current code reaches flushing, hijacking, and deadline control through a
// wrapper.
func (rec *recorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

// Flush keeps streaming handlers working through the wrapper, for code that
// asserts http.Flusher rather than using http.ResponseController.
func (rec *recorder) Flush() {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	// Flush reports no error to a handler that asserts http.Flusher.
	_ = http.NewResponseController(rec.ResponseWriter).Flush()
}

// Hijack keeps protocol upgrades working through the wrapper.
func (rec *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buf, err := http.NewResponseController(rec.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	rec.hijacked = true
	return conn, buf, nil
}
