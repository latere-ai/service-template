package web

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/example/reference-service/internal/httpx"
)

// outcome is what the handler decides to do with a request. The decision is
// separated from the response so the precedence rule is testable without a
// server.
type outcome int

const (
	// outcomeAPINotFound is an unmatched path under an API prefix. It gets the
	// JSON error envelope, never the shell: a mistyped endpoint that answers
	// 200 with markup is the failure that costs the most client-side
	// debugging time.
	outcomeAPINotFound outcome = iota
	// outcomeAsset is a file the bundle holds, including a prerendered
	// document for a public route.
	outcomeAsset
	// outcomeShell is a client-side route on a hard load. The shell answers
	// with 200 and the router in the browser resolves the path.
	outcomeShell
	// outcomeNotAllowed is a write method on an unknown path. Returning markup
	// to a programmatic client hides the real error, so it gets 405.
	outcomeNotAllowed
	// outcomeNoBundle is a request that would need a shell the binary does not
	// embed.
	outcomeNoBundle
)

// handler serves the bundle and applies the precedence rule.
type handler struct {
	bundle      *bundle
	apiPrefixes []string
}

// Handler serves the built frontend at the lowest route precedence.
//
// It is registered as a method-agnostic catch-all, which is what keeps it from
// conflicting with the method-specific API and probe routes registered before
// it. A catch-all bound to one method would let the router answer an unknown
// path itself, and the decision below would never run.
//
// apiPrefixes are the path prefixes the API owns, for example "/v1". A request
// under one of them that reaches this handler is an unmatched API path and is
// answered with the error envelope.
func Handler(dist fs.FS, apiPrefixes []string) http.Handler {
	b, err := newBundle(dist)
	if err != nil {
		// A bundle that cannot be read is a build defect, and the process is
		// still able to serve the API, so the failure is recorded rather than
		// fatal. Requests for the missing documents answer 404.
		// The handler is built before a request exists, so the record
		// carries the background context.
		slog.ErrorContext(context.Background(), "read the embedded frontend bundle",
			slog.Any("error", err))
	}
	return &handler{bundle: b, apiPrefixes: normalizePrefixes(apiPrefixes)}
}

// normalizePrefixes puts every prefix in "/segment" form and drops the empty
// ones, so a caller may pass "v1", "/v1", or "/v1/" and mean the same thing.
func normalizePrefixes(prefixes []string) []string {
	out := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		p = strings.Trim(strings.TrimSpace(p), "/")
		if p == "" {
			continue
		}
		out = append(out, "/"+p)
	}
	return out
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	out, name := h.decide(r.Method, r.URL.Path)
	switch out {
	case outcomeAsset, outcomeShell:
		a, ok := h.bundle.lookup(name)
		if !ok {
			// Unreachable: decide returns these outcomes only for a name the
			// bundle holds.
			httpx.WriteError(w, r, httpx.New(http.StatusNotFound, "no such document"))
			return
		}
		h.bundle.serve(w, r, a)
	case outcomeAPINotFound:
		httpx.WriteError(w, r, httpx.Newf(http.StatusNotFound,
			"no route for %s %s", r.Method, r.URL.Path))
	case outcomeNotAllowed:
		w.Header().Set("Allow", "GET, HEAD")
		httpx.WriteError(w, r, httpx.Newf(http.StatusMethodNotAllowed,
			"%s is not allowed on %s", r.Method, r.URL.Path))
	case outcomeNoBundle:
		httpx.WriteError(w, r, httpx.New(http.StatusNotFound,
			"this binary embeds no frontend bundle"))
	}
}

// decide applies the route precedence rule to one request.
//
// The order is: an unmatched API path, then a file the bundle holds, then a
// prerendered document for the path, then the shell for a read method, and
// 405 for anything else. Probe and metadata routes never reach here, because
// the router matches them first.
func (h *handler) decide(method, requestPath string) (outcome, string) {
	if h.underAPI(requestPath) {
		return outcomeAPINotFound, ""
	}
	name := documentName(requestPath)
	if _, ok := h.bundle.lookup(name); ok {
		return outcomeAsset, name
	}
	// A public route is prerendered to a complete document at build time. It
	// is preferred over the shell, because it carries the title, the
	// description, and the content that a client which runs no JavaScript
	// needs in the first response.
	for _, candidate := range []string{path.Join(name, ShellDocument), name + ".html"} {
		if _, ok := h.bundle.lookup(candidate); ok {
			return outcomeAsset, candidate
		}
	}
	if method != http.MethodGet && method != http.MethodHead {
		return outcomeNotAllowed, ""
	}
	if _, ok := h.bundle.lookup(ShellDocument); !ok {
		return outcomeNoBundle, ""
	}
	return outcomeShell, ShellDocument
}

// underAPI reports whether a path is inside a prefix the API owns. The match
// is on a path boundary, so the prefix "/v1" covers "/v1" and "/v1/things"
// and never "/v1beta", which would send a client route the error envelope.
func (h *handler) underAPI(p string) bool {
	for _, prefix := range h.apiPrefixes {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// documentName maps a request path to a path from the document root. The path
// is cleaned first, so a traversal attempt resolves inside the bundle and, at
// worst, finds no file and falls through to the shell.
func documentName(requestPath string) string {
	cleaned := path.Clean("/" + requestPath)
	if strings.HasSuffix(requestPath, "/") && cleaned != "/" {
		cleaned += "/"
	}
	name := strings.TrimPrefix(cleaned, "/")
	if name == "" || strings.HasSuffix(name, "/") {
		name += ShellDocument
	}
	if !fs.ValidPath(name) {
		return ShellDocument
	}
	return name
}
