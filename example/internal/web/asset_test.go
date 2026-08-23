package web

import (
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCacheHeadersFollowTheAssetClass(t *testing.T) {
	h := newTestHandler(t)
	cases := []struct {
		target string
		want   string
	}{
		{"/assets/index-C3xK9pQ2.js", cacheImmutable},
		{"/index.html", cacheDocument},
		{"/dashboard", cacheDocument},
		{"/docs", cacheDocument},
		{"/robots.txt", cacheMetadata},
		{"/sitemap.xml", cacheMetadata},
		{"/favicon.ico", cacheDocument},
	}
	for _, c := range cases {
		res := do(t, h, http.MethodGet, c.target, nil)
		if got := res.Header.Get("Cache-Control"); got != c.want {
			t.Errorf("GET %s: Cache-Control %q, want %q", c.target, got, c.want)
		}
		if res.Header.Get("ETag") == "" {
			t.Errorf("GET %s: no entity tag", c.target)
		}
		body(t, res)
	}
}

func TestHashedNameDetection(t *testing.T) {
	hashed := []string{
		"assets/index-C3xK9pQ2.js",
		"app.9f1c2ab4.css",
		"logo-a1b2c3d4e5.svg",
	}
	plain := []string{
		"index.html",
		"robots.txt",
		"apple-touch-icon.png",
		"favicon-32x32.png",
		"assets/style.css",
	}
	for _, name := range hashed {
		if !hashedName(name) {
			t.Errorf("%s: not detected as hashed", name)
		}
	}
	for _, name := range plain {
		if hashedName(name) {
			t.Errorf("%s: detected as hashed", name)
		}
	}
}

func TestTheEntityTagRevalidates(t *testing.T) {
	h := newTestHandler(t)
	first := do(t, h, http.MethodGet, "/assets/index-C3xK9pQ2.js", nil)
	tag := first.Header.Get("ETag")
	if got := body(t, first); got != entryScript {
		t.Fatalf("body %q, want the entry script", got)
	}
	if tag == "" {
		t.Fatal("no entity tag on the first response")
	}
	second := do(t, h, http.MethodGet, "/assets/index-C3xK9pQ2.js",
		http.Header{"If-None-Match": {tag}})
	if second.StatusCode != http.StatusNotModified {
		t.Errorf("status %d, want 304", second.StatusCode)
	}
	if got := body(t, second); got != "" {
		t.Errorf("a 304 carries no body, got %q", got)
	}
}

func TestARangeRequestReturnsPartialContent(t *testing.T) {
	res := do(t, newTestHandler(t), http.MethodGet, "/assets/index-C3xK9pQ2.js",
		http.Header{"Range": {"bytes=0-3"}})
	if res.StatusCode != http.StatusPartialContent {
		t.Fatalf("status %d, want 206", res.StatusCode)
	}
	if got := body(t, res); got != entryScript[:4] {
		t.Errorf("body %q, want %q", got, entryScript[:4])
	}
	if got := res.Header.Get("Content-Range"); got == "" {
		t.Error("no Content-Range on a partial response")
	}
}

func TestAPrecompressedVariantIsNegotiated(t *testing.T) {
	h := newTestHandler(t)
	cases := []struct {
		accept   string
		encoding string
		want     string
	}{
		{"br, gzip", "br", "brotli-bytes"},
		{"gzip", "gzip", "gzip-bytes"},
		{"gzip;q=0.8, br;q=0", "gzip", "gzip-bytes"},
		{"*", "br", "brotli-bytes"},
		{"", "", entryScript},
		{"identity", "", entryScript},
		{"br;q=0, gzip;q=0", "", entryScript},
	}
	tags := map[string]string{}
	for _, c := range cases {
		header := http.Header{}
		if c.accept != "" {
			header.Set("Accept-Encoding", c.accept)
		}
		res := do(t, h, http.MethodGet, "/assets/index-C3xK9pQ2.js", header)
		if got := res.Header.Get("Content-Encoding"); got != c.encoding {
			t.Errorf("accept %q: Content-Encoding %q, want %q", c.accept, got, c.encoding)
		}
		if got := res.Header.Get("Vary"); got != "Accept-Encoding" {
			t.Errorf("accept %q: Vary %q, want Accept-Encoding", c.accept, got)
		}
		if got := res.Header.Get("Content-Type"); !strings.Contains(got, "javascript") {
			t.Errorf("accept %q: Content-Type %q, want the type of the decoded content", c.accept, got)
		}
		if got := body(t, res); got != c.want {
			t.Errorf("accept %q: body %q, want %q", c.accept, got, c.want)
		}
		tags[c.encoding] = res.Header.Get("ETag")
	}
	// A cache keys on the entity tag, so two codings of one file must never
	// share one.
	if tags[""] == tags["gzip"] || tags[""] == tags["br"] || tags["gzip"] == tags["br"] {
		t.Errorf("codings share an entity tag: %v", tags)
	}
}

// TestAMalformedWeightRejectsTheCoding keeps an unparsable q value from
// forcing a variant on a client that may not decode it.
func TestAMalformedWeightRejectsTheCoding(t *testing.T) {
	res := do(t, newTestHandler(t), http.MethodGet, "/assets/index-C3xK9pQ2.js",
		http.Header{"Accept-Encoding": {"gzip;q=high"}})
	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding %q, want the identity representation", got)
	}
	body(t, res)
}

// TestAVariantWithNoIdentityIsNotServed covers a bundle that carries a
// compressed file whose source was pruned. Its media type and its cache class
// come from the decoded name, so it cannot be negotiated on its own.
func TestAVariantWithNoIdentityIsNotServed(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":   &fstest.MapFile{Data: []byte(shell)},
		"orphan.js.gz": &fstest.MapFile{Data: []byte("gzip-bytes")},
	}
	res := do(t, Handler(fsys, nil), http.MethodGet, "/orphan.js",
		http.Header{"Accept-Encoding": {"gzip"}})
	if got := body(t, res); got != shell {
		t.Errorf("body %q, want the shell", got)
	}
	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding %q, want none", got)
	}
}

// TestAnUnknownExtensionIsSniffed keeps a file with no registered media type
// from being served as an empty content type.
func TestAnUnknownExtensionIsSniffed(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(shell)},
		"data.zzz":   &fstest.MapFile{Data: []byte("plain text")},
	}
	res := do(t, Handler(fsys, nil), http.MethodGet, "/data.zzz", nil)
	if got := res.Header.Get("Content-Type"); got == "" {
		t.Error("no content type for an unknown extension")
	}
	body(t, res)
}

// failingFS reports an error for every file, which is what an unreadable
// bundle looks like at start-up.
type failingFS struct{ fstest.MapFS }

func (f failingFS) Open(name string) (fs.File, error) {
	if name == "." {
		return f.MapFS.Open(name)
	}
	return nil, errors.New("unreadable")
}

// ReadFile shadows the one the embedded map filesystem provides, which
// fs.ReadFile prefers over Open.
func (f failingFS) ReadFile(string) ([]byte, error) {
	return nil, errors.New("unreadable")
}

// TestAHandlerOverAnUnreadableBundleStillServes keeps a build defect from
// taking the process down: the bundle is empty and every request answers the
// envelope.
func TestAHandlerOverAnUnreadableBundleStillServes(t *testing.T) {
	h := Handler(failingFS{fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(shell)}}}, nil)
	res := do(t, h, http.MethodGet, "/", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", res.StatusCode)
	}
	assertEnvelope(t, body(t, res), http.StatusNotFound)
}

func TestAnUnreadableBundleIsReportedAndServesNothing(t *testing.T) {
	b, err := newBundle(failingFS{fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(shell)}}})
	if err == nil {
		t.Fatal("an unreadable bundle must report an error")
	}
	if b == nil {
		t.Fatal("newBundle must return a usable bundle even on error")
	}
	if _, ok := b.lookup(ShellDocument); ok {
		t.Error("an unreadable file must not enter the bundle")
	}
}
