package httpx

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CORSOptions describes which browser origins may call this service.
//
// The zero value allows nothing, which is the safe default for a service whose
// browser client is served from the same origin. An allowed origin is matched
// exactly; there is no pattern language, because a wildcard subdomain rule is
// the usual way an origin allowlist ends up wider than its author believed.
type CORSOptions struct {
	// AllowedOrigins lists origins verbatim, for example
	// "https://app.example.com". The single entry "*" allows any origin and is
	// rejected together with AllowCredentials, because a credentialed wildcard
	// hands any site the caller's cookies.
	AllowedOrigins []string
	// AllowedMethods defaults to the safe methods plus POST, PUT, PATCH, and
	// DELETE.
	AllowedMethods []string
	// AllowedHeaders lists request headers a browser may send. Content-Type
	// and Authorization are included by default because every JSON API needs
	// them.
	AllowedHeaders []string
	// ExposedHeaders lists response headers a browser script may read. The
	// request identifier is included by default so a browser client can report
	// it.
	ExposedHeaders []string
	// AllowCredentials permits cookies and the Authorization header on a
	// cross-origin request.
	AllowCredentials bool
	// MaxAge is how long a browser may cache a preflight result.
	MaxAge time.Duration
}

var (
	defaultCORSMethods = []string{
		http.MethodGet, http.MethodHead, http.MethodPost,
		http.MethodPut, http.MethodPatch, http.MethodDelete,
	}
	defaultCORSHeaders = []string{"Content-Type", "Authorization", HeaderRequestID}
	defaultCORSExposed = []string{HeaderRequestID}
)

// CORS answers preflight requests and marks cross-origin responses.
//
// It sits before authentication because a preflight carries no credentials by
// definition: an OPTIONS probe answered with 401 tells the browser the real
// request is not allowed, and the route becomes unreachable from the page.
func CORS(o CORSOptions) func(http.Handler) http.Handler {
	allowed := normalizeOrigins(o.AllowedOrigins)
	wildcard := len(allowed) == 1 && allowed[0] == "*"
	credentials := o.AllowCredentials && !wildcard

	methods := strings.Join(orDefault(o.AllowedMethods, defaultCORSMethods), ", ")
	headers := strings.Join(orDefault(o.AllowedHeaders, defaultCORSHeaders), ", ")
	exposed := strings.Join(orDefault(o.ExposedHeaders, defaultCORSExposed), ", ")
	maxAge := strconv.Itoa(int(o.MaxAge.Seconds()))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			// The response varies by origin whether or not this one is
			// allowed, so a shared cache cannot serve one origin's answer to
			// another.
			w.Header().Add("Vary", "Origin")

			if origin == "" || !originAllowed(allowed, wildcard, origin) {
				if isPreflight(r) {
					// A preflight from a disallowed origin is answered without
					// the allow headers. The browser blocks the real request,
					// and the route is never reached.
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			h := w.Header()
			if wildcard {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
			}
			if credentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
			if exposed != "" {
				h.Set("Access-Control-Expose-Headers", exposed)
			}

			if !isPreflight(r) {
				next.ServeHTTP(w, r)
				return
			}

			h.Add("Vary", "Access-Control-Request-Method")
			h.Add("Vary", "Access-Control-Request-Headers")
			h.Set("Access-Control-Allow-Methods", methods)
			h.Set("Access-Control-Allow-Headers", headers)
			if o.MaxAge > 0 {
				h.Set("Access-Control-Max-Age", maxAge)
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}
}

// isPreflight reports the OPTIONS request a browser sends before a request it
// cannot make simply.
func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions &&
		r.Header.Get("Access-Control-Request-Method") != ""
}

// originAllowed compares against the allowlist. Origin comparison is
// case-insensitive in the scheme and the host, which is the only part an
// origin has.
func originAllowed(allowed []string, wildcard bool, origin string) bool {
	if wildcard {
		return true
	}
	return slices.Contains(allowed, strings.ToLower(origin))
}

func normalizeOrigins(origins []string) []string {
	out := make([]string, 0, len(origins))
	for _, o := range origins {
		o = strings.ToLower(strings.TrimSpace(o))
		if o != "" {
			out = append(out, o)
		}
	}
	return out
}

func orDefault(v, fallback []string) []string {
	if len(v) > 0 {
		return v
	}
	return fallback
}
