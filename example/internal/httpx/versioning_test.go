package httpx

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPrefixAndPathCarryTheMajorVersion(t *testing.T) {
	if got := Prefix(CurrentMajor); got != "/v1" {
		t.Errorf("Prefix(%d) = %q, want /v1", CurrentMajor, got)
	}
	if got := Path(2, "items/{id}"); got != "/v2/items/{id}" {
		t.Errorf("Path = %q, want /v2/items/{id}", got)
	}
	if got := Path(1, "/items"); got != "/v1/items" {
		t.Errorf("Path = %q, want /v1/items", got)
	}
}

func TestMajorFromPath(t *testing.T) {
	cases := map[string]struct {
		path  string
		major int
		ok    bool
	}{
		"versioned":   {"/v1/items", 1, true},
		"two digits":  {"/v12/items", 12, true},
		"no version":  {"/items", 0, false},
		"not numeric": {"/vNext/items", 0, false},
		"zero":        {"/v0/items", 0, false},
		"root":        {"/", 0, false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			major, ok := MajorFromPath(tc.path)
			if major != tc.major || ok != tc.ok {
				t.Fatalf("MajorFromPath(%q) = %d, %v; want %d, %v", tc.path, major, ok, tc.major, tc.ok)
			}
		})
	}
}

func TestDeprecatedRouteAnnouncesItsSunset(t *testing.T) {
	since := time.Date(2026, time.January, 5, 12, 0, 0, 0, time.UTC)
	sunset := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)

	h := Deprecated(Deprecation{
		Since:         since,
		Sunset:        sunset,
		Successor:     "https://api.example.com/v2/items",
		Documentation: "https://docs.example.com/deprecations/items-v1",
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/items", nil))

	if got, want := res.Header().Get("Deprecation"), "@"+strconv.FormatInt(since.Unix(), 10); got != want {
		t.Errorf("Deprecation = %q, want %q", got, want)
	}
	if got, want := res.Header().Get("Sunset"), sunset.Format(http.TimeFormat); got != want {
		t.Errorf("Sunset = %q, want %q", got, want)
	}
	if _, err := time.Parse(http.TimeFormat, res.Header().Get("Sunset")); err != nil {
		t.Errorf("the Sunset header is not an HTTP date: %v", err)
	}

	links := strings.Join(res.Header().Values("Link"), ", ")
	if !strings.Contains(links, `rel="successor-version"`) || !strings.Contains(links, "/v2/items") {
		t.Errorf("Link = %q, want the successor", links)
	}
	if !strings.Contains(links, `rel="deprecation"`) {
		t.Errorf("Link = %q, want the deprecation documentation", links)
	}
}

func TestDeprecatedRouteAnnouncesItselfOnAnErrorToo(t *testing.T) {
	captureDefaultLogger(t)

	sunset := time.Now().Add(90 * 24 * time.Hour)
	route := Deprecated(Deprecation{Sunset: sunset})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, New(http.StatusNotFound, "no item with that id"))
	}))
	h := Handler(route, Options{})

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/items/42", nil))

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNotFound)
	}
	decodeEnvelope(t, res)
	if res.Header().Get("Deprecation") == "" || res.Header().Get("Sunset") == "" {
		t.Fatalf("a failing deprecated route announced nothing: %v", res.Header())
	}
}

func TestDeprecationDefaultsToNowWhenNoDateIsGiven(t *testing.T) {
	h := Deprecated(Deprecation{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/items", nil))

	value := res.Header().Get("Deprecation")
	if !strings.HasPrefix(value, "@") {
		t.Fatalf("Deprecation = %q, want a structured field date", value)
	}
	if res.Header().Get("Sunset") != "" {
		t.Error("a deprecation with no sunset date announced one")
	}
}
