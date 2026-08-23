package httpx

import (
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

// CurrentMajor is the major version this service serves. Every route sits
// under its prefix, so a breaking change is a new prefix served beside the old
// one rather than a change under the client's feet.
const CurrentMajor = 1

// Inside a major version only additive change is allowed: a new endpoint, a
// new optional request field, a new response field. Removing a field,
// narrowing a type, or making an optional field required is a new major
// version.

// Prefix returns the path prefix of a major version, for example "/v1".
func Prefix(major int) string {
	return "/v" + strconv.Itoa(major)
}

// Path joins a route to its version prefix, so a route table states the
// version once per route and never spells the prefix by hand.
func Path(major int, route string) string {
	return path.Join(Prefix(major), route)
}

// Deprecation describes a route that is on its way out. It becomes the
// response headers a client needs to discover the change without reading a
// changelog: when the route was deprecated, when it stops answering, and what
// replaces it.
type Deprecation struct {
	// Since is when the route was announced as deprecated.
	Since time.Time
	// Sunset is when the route stops answering. It must be after Since.
	Sunset time.Time
	// Successor is the URI of the replacement route, reported as a link with
	// relation "successor-version".
	Successor string
	// Documentation is the URI describing the change, reported as a link with
	// relation "deprecation".
	Documentation string
}

// Deprecated marks every response from a route as deprecated.
//
// The headers are set before the handler runs, so an error response from the
// route carries them too. A client that only ever sees failures from a route
// still learns that the route is going away.
func Deprecated(d Deprecation) func(http.Handler) http.Handler {
	deprecation := deprecationValue(d.Since)
	sunset := ""
	if !d.Sunset.IsZero() {
		sunset = d.Sunset.UTC().Format(http.TimeFormat)
	}
	links := links(d)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Deprecation", deprecation)
			if sunset != "" {
				h.Set("Sunset", sunset)
			}
			for _, l := range links {
				h.Add("Link", l)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// deprecationValue renders the deprecation date as a structured field date,
// which is the form the header is defined in. A zero date means the route is
// deprecated as of now, so the header is never empty.
func deprecationValue(since time.Time) string {
	if since.IsZero() {
		since = time.Now()
	}
	return "@" + strconv.FormatInt(since.UTC().Unix(), 10)
}

// links renders the successor and the documentation as Link header values.
func links(d Deprecation) []string {
	var out []string
	if d.Successor != "" {
		out = append(out, fmt.Sprintf("<%s>; rel=\"successor-version\"", d.Successor))
	}
	if d.Documentation != "" {
		out = append(out, fmt.Sprintf("<%s>; rel=\"deprecation\"; type=\"text/html\"", d.Documentation))
	}
	return out
}

// MajorFromPath reports the major version a path carries, and whether it
// carries one. It exists so telemetry and access records can group by version
// without each caller reparsing the prefix.
func MajorFromPath(p string) (int, bool) {
	p = strings.TrimPrefix(p, "/")
	segment, _, _ := strings.Cut(p, "/")
	if !strings.HasPrefix(segment, "v") {
		return 0, false
	}
	major, err := strconv.Atoi(segment[1:])
	if err != nil || major < 1 {
		return 0, false
	}
	return major, true
}
