package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// shell is the application shell the test bundle serves for a client-side
// route. It references a hashed entry asset, as a built bundle does.
const shell = `<!doctype html><html><head><title>App</title></head>` +
	`<body><div id="root"></div>` +
	`<script type="module" src="/assets/index-C3xK9pQ2.js"></script></body></html>`

const entryScript = "console.log('app');\n"

const prerendered = `<!doctype html><html><head><title>Docs</title>` +
	`<meta name="description" content="documentation"></head><body>docs</body></html>`

// testFS is a built bundle: a shell, a hashed entry asset with precompressed
// variants beside it, a prerendered public route, and crawler metadata.
func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                  &fstest.MapFile{Data: []byte(shell)},
		"assets/index-C3xK9pQ2.js":    &fstest.MapFile{Data: []byte(entryScript)},
		"assets/index-C3xK9pQ2.js.gz": &fstest.MapFile{Data: []byte("gzip-bytes")},
		"assets/index-C3xK9pQ2.js.br": &fstest.MapFile{Data: []byte("brotli-bytes")},
		"docs/index.html":             &fstest.MapFile{Data: []byte(prerendered)},
		"robots.txt":                  &fstest.MapFile{Data: []byte("User-agent: *\n")},
		"sitemap.xml":                 &fstest.MapFile{Data: []byte("<urlset></urlset>")},
		"favicon.ico":                 &fstest.MapFile{Data: []byte("icon")},
	}
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	return Handler(testFS(), []string{"/v1", "/api"})
}

// do runs one request against a handler and returns the response.
//
// The body is closed on cleanup rather than by each caller. A response body
// nobody closes is the same defect in a test as in production code, and one
// owner here is what keeps every call site from having to remember it.
func do(t *testing.T, h http.Handler, method, target string, header http.Header) *http.Response {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	for k, values := range header {
		for _, v := range values {
			r.Header.Add(k, v)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	res := w.Result()
	t.Cleanup(func() {
		if err := res.Body.Close(); err != nil {
			t.Errorf("close the body: %v", err)
		}
	})
	return res
}

// body reads a response that do returned. Closing belongs to do, so this
// reads and nothing else.
func body(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read the body: %v", err)
	}
	return string(b)
}

func TestHardLoadOfAClientRouteReturnsTheShell(t *testing.T) {
	h := newTestHandler(t)
	for _, target := range []string{"/dashboard", "/dashboard/settings/42", "/a/b/c/d"} {
		res := do(t, h, http.MethodGet, target, nil)
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", target, res.StatusCode)
		}
		if got := body(t, res); got != shell {
			t.Errorf("GET %s: body %q, want the shell", target, got)
		}
	}
}

func TestHeadOfAClientRouteReturnsTheShellStatus(t *testing.T) {
	res := do(t, newTestHandler(t), http.MethodHead, "/dashboard", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content type %q, want text/html", ct)
	}
}

func TestWriteMethodOnAnUnknownPathIsNotAllowed(t *testing.T) {
	h := newTestHandler(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		res := do(t, h, method, "/dashboard", nil)
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s /dashboard: status %d, want 405", method, res.StatusCode)
		}
		if allow := res.Header.Get("Allow"); allow != "GET, HEAD" {
			t.Errorf("%s /dashboard: Allow %q, want \"GET, HEAD\"", method, allow)
		}
		got := body(t, res)
		if strings.Contains(got, "<html") {
			t.Errorf("%s /dashboard: body is markup: %q", method, got)
		}
		assertEnvelope(t, got, http.StatusMethodNotAllowed)
	}
}

// TestAWriteMethodOnAnExistingDocumentServesIt pins the precedence rule: the
// file test comes before the method test, so a write method on a path the
// bundle holds serves the file. Only an unknown path is answered with 405.
func TestAWriteMethodOnAnExistingDocumentServesIt(t *testing.T) {
	h := newTestHandler(t)
	for _, target := range []string{"/", "/index.html", "/favicon.ico"} {
		res := do(t, h, http.MethodPost, target, nil)
		if res.StatusCode != http.StatusOK {
			t.Errorf("POST %s: status %d, want 200", target, res.StatusCode)
		}
		body(t, res)
	}
}

func TestUnknownPathUnderTheAPIPrefixReturnsTheEnvelope(t *testing.T) {
	h := newTestHandler(t)
	for _, target := range []string{"/v1", "/v1/nope", "/api/widgets/1"} {
		res := do(t, h, http.MethodGet, target, nil)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", target, res.StatusCode)
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
			t.Errorf("GET %s: content type %q, want problem+json", target, ct)
		}
		assertEnvelope(t, body(t, res), http.StatusNotFound)
	}
}

// TestAPrefixMatchStopsAtAPathBoundary guards the client route that starts
// with the same letters as the API prefix. It must reach the shell, not the
// error envelope.
func TestAPrefixMatchStopsAtAPathBoundary(t *testing.T) {
	h := newTestHandler(t)
	for _, target := range []string{"/v1beta", "/apidocs", "/v1beta/page"} {
		res := do(t, h, http.MethodGet, target, nil)
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", target, res.StatusCode)
		}
		if got := body(t, res); got != shell {
			t.Errorf("GET %s: want the shell", target)
		}
	}
}

func TestAPrerenderedDocumentWinsOverTheShell(t *testing.T) {
	h := newTestHandler(t)
	for _, target := range []string{"/docs", "/docs/"} {
		res := do(t, h, http.MethodGet, target, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200", target, res.StatusCode)
		}
		if got := body(t, res); got != prerendered {
			t.Errorf("GET %s: body %q, want the prerendered document", target, got)
		}
	}
}

// TestASiblingHTMLDocumentIsServedForAnExtensionlessPath covers the flat
// prerender layout, where a public route is written as <route>.html.
func TestASiblingHTMLDocumentIsServedForAnExtensionlessPath(t *testing.T) {
	fsys := testFS()
	fsys["pricing.html"] = &fstest.MapFile{Data: []byte("<html>pricing</html>")}
	res := do(t, Handler(fsys, nil), http.MethodGet, "/pricing", nil)
	if got := body(t, res); got != "<html>pricing</html>" {
		t.Errorf("body %q, want the sibling document", got)
	}
}

func TestATraversalPathFallsThroughToTheShell(t *testing.T) {
	h := newTestHandler(t)
	for _, target := range []string{"/../../etc/passwd", "/assets/../../secret"} {
		res := do(t, h, http.MethodGet, target, nil)
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", target, res.StatusCode)
		}
		if got := body(t, res); got != shell {
			t.Errorf("GET %s: served %q, want the shell", target, got)
		}
	}
}

// TestTheCatchAllDoesNotConflictWithRegisteredRoutes builds the router the way
// the server does: method-specific API routes, a probe route, then the static
// handler as a method-agnostic catch-all. Registration must not panic and the
// specific routes must keep precedence.
// TestAPathThatIsNotAValidFileNameFallsThroughToTheShell covers a request
// path that decodes to bytes no file name can hold.
func TestAPathThatIsNotAValidFileNameFallsThroughToTheShell(t *testing.T) {
	res := do(t, newTestHandler(t), http.MethodGet, "/%FF%FE", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", res.StatusCode)
	}
	if got := body(t, res); got != shell {
		t.Errorf("body %q, want the shell", got)
	}
}

func TestTheCatchAllDoesNotConflictWithRegisteredRoutes(t *testing.T) {
	mux := http.NewServeMux()
	defer func() {
		if v := recover(); v != nil {
			t.Fatalf("registering the routes panicked: %v", v)
		}
	}()
	mux.HandleFunc("GET /v1/things", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := io.WriteString(w, "api"); err != nil {
			t.Errorf("write the API body: %v", err)
		}
	})
	mux.HandleFunc("POST /v1/things", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := io.WriteString(w, "ok"); err != nil {
			t.Errorf("write the probe body: %v", err)
		}
	})
	mux.Handle("/", newTestHandler(t))

	cases := []struct {
		method, target, want string
		status               int
	}{
		{http.MethodGet, "/v1/things", "api", http.StatusOK},
		{http.MethodGet, "/healthz", "ok", http.StatusOK},
		{http.MethodGet, "/dashboard", shell, http.StatusOK},
	}
	for _, c := range cases {
		res := do(t, mux, c.method, c.target, nil)
		if res.StatusCode != c.status {
			t.Errorf("%s %s: status %d, want %d", c.method, c.target, res.StatusCode, c.status)
		}
		if got := body(t, res); got != c.want {
			t.Errorf("%s %s: body %q, want %q", c.method, c.target, got, c.want)
		}
	}

	// An unmatched path under the API prefix reaches the catch-all and gets
	// the envelope rather than the shell.
	res := do(t, mux, http.MethodGet, "/v1/nope", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("GET /v1/nope: status %d, want 404", res.StatusCode)
	}
	assertEnvelope(t, body(t, res), http.StatusNotFound)

	// A method registered on the path still reaches its own handler.
	if res := do(t, mux, http.MethodPost, "/v1/things", nil); res.StatusCode != http.StatusCreated {
		t.Errorf("POST /v1/things: status %d, want 201", res.StatusCode)
	}

	// A method-agnostic catch-all matches every path, so the router never
	// reaches its own 405 branch: an unregistered method on a registered API
	// path arrives here and is answered as an unmatched API path. The API
	// group owns the method rule for its own routes.
	unregistered := do(t, mux, http.MethodDelete, "/v1/things", nil)
	if unregistered.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE /v1/things: status %d, want 404 from the catch-all", unregistered.StatusCode)
	}
	assertEnvelope(t, body(t, unregistered), http.StatusNotFound)
}

func TestAHandlerWithNoAPIPrefixServesEveryPath(t *testing.T) {
	h := Handler(testFS(), nil)
	res := do(t, h, http.MethodGet, "/v1/anything", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", res.StatusCode)
	}
	if got := body(t, res); got != shell {
		t.Errorf("want the shell with no API prefix declared")
	}
}

func TestPrefixesAreNormalized(t *testing.T) {
	h := Handler(testFS(), []string{"v1/", " /api ", "", "/"})
	for _, target := range []string{"/v1/x", "/api/x"} {
		if res := do(t, h, http.MethodGet, target, nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", target, res.StatusCode)
		}
	}
	if res := do(t, h, http.MethodGet, "/other", nil); res.StatusCode != http.StatusOK {
		t.Errorf("an empty prefix must not swallow every path")
	}
}

// TestABinaryWithNoBundleAnswersTheEnvelope covers the profile that serves an
// API only. No request may answer 200 with an empty document.
func TestABinaryWithNoBundleAnswersTheEnvelope(t *testing.T) {
	h := Handler(fstest.MapFS{}, []string{"/v1"})
	res := do(t, h, http.MethodGet, "/dashboard", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", res.StatusCode)
	}
	assertEnvelope(t, body(t, res), http.StatusNotFound)
}

func TestANilFilesystemIsServedAsAnEmptyBundle(t *testing.T) {
	res := do(t, Handler(nil, nil), http.MethodGet, "/", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", res.StatusCode)
	}
}

// assertEnvelope fails when a body is not the JSON error envelope with the
// expected status.
func assertEnvelope(t *testing.T, got string, status int) {
	t.Helper()
	var envelope struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatalf("body %q does not parse as the error envelope: %v", got, err)
	}
	if envelope.Status != status {
		t.Errorf("envelope status %d, want %d", envelope.Status, status)
	}
	if envelope.Title == "" || envelope.Type == "" {
		t.Errorf("envelope %q is missing a title or a type", got)
	}
}
