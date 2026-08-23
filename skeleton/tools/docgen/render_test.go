package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.com/service/internal/config"
)

func TestTypeNamesAreWrittenForAnOperatorNotForAProgrammer(t *testing.T) {
	cases := map[reflect.Type]string{
		reflect.TypeFor[time.Duration](): "duration",
		reflect.TypeFor[slog.Level]():    "log level",
		reflect.TypeFor[config.Secret](): "secret",
		reflect.TypeFor[string]():        "string",
		reflect.TypeFor[bool]():          "boolean",
		reflect.TypeFor[float64]():       "number",
		reflect.TypeFor[int]():           "integer",
		reflect.TypeFor[uint16]():        "integer",
		reflect.TypeFor[[]string]():      "[]string",
	}
	for typ, want := range cases {
		if got := typeName(typ); got != want {
			t.Errorf("%s is reported as %q, want %q", typ, got, want)
		}
	}
}

func TestJSONTypesAreWrittenAsAClientParsesThem(t *testing.T) {
	cases := map[reflect.Type]string{
		reflect.TypeFor[string]():         "string",
		reflect.TypeFor[bool]():           "boolean",
		reflect.TypeFor[int]():            "number",
		reflect.TypeFor[float32]():        "number",
		reflect.TypeFor[[]int]():          "array of numbers",
		reflect.TypeFor[map[string]any](): "object",
		reflect.TypeFor[*string]():        "string",
		reflect.TypeFor[chan int]():       "chan int",
	}
	for typ, want := range cases {
		if got := jsonType(typ); got != want {
			t.Errorf("%s is reported as %q, want %q", typ, got, want)
		}
	}
}

func TestADefaultIsDistinguishedFromARequiredValue(t *testing.T) {
	cases := map[string]setting{
		"`:8080`":        {Default: ":8080"},
		"empty":          {},
		"none, required": {Required: true},
	}
	for want, s := range cases {
		if got := defaultCell(s); got != want {
			t.Errorf("%+v renders as %q, want %q", s, got, want)
		}
	}
}

func TestAnEmptyCellSaysSoRatherThanLeavingAHole(t *testing.T) {
	if got := cell(""); got != "none" {
		t.Errorf("an empty cell renders as %q", got)
	}
	if got := cell("a | b"); got != `a \| b` {
		t.Errorf("a table character is not escaped: %q", got)
	}
}

func TestTheDifferenceReportNamesTheFirstDifferingLine(t *testing.T) {
	got := firstDifference([]byte("a\nb\n"), []byte("a\nc\n"))
	if !strings.Contains(got, "line 2") {
		t.Errorf("the report does not name the line: %q", got)
	}
	if report := firstDifference([]byte("a\n"), []byte("a\nextra\n")); !strings.Contains(report, "line 2") {
		t.Errorf("a missing trailing line is not reported: %q", report)
	}
}

func TestDocumentsCannotBeWrittenIntoAMissingDirectory(t *testing.T) {
	if _, err := WriteDocs(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("the documents were written into a directory that does not exist")
	}
}

func TestARendererThatRefusesIsReported(t *testing.T) {
	// A renderer that fails on everything stands in for mermaid refusing a
	// diagram, so the failure path is proved without the renderer installed.
	dir := writeDocs(t, map[string]string{
		"docs/a.md": wrap("flowchart LR\n  a --> b"),
	})
	problems := CheckDiagrams(dir, "/usr/bin/false")
	if len(problems) != 1 || !strings.Contains(problems[0], "did not render") {
		t.Fatalf("a refused diagram was not reported: %v", problems)
	}
	if problems := CheckDiagrams(dir, "/nonexistent/renderer"); len(problems) != 1 {
		t.Fatalf("a missing renderer was not reported: %v", problems)
	}
}

func TestTildeFencesAndUnknownLanguagesAreHandled(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"docs/a.md": "# Title\n\n~~~mermaid\nflowchart LR\n  a --> b\n~~~\n",
	})
	diagrams, problems, err := CollectDiagrams(dir)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(diagrams) != 1 || len(problems) != 0 {
		t.Fatalf("a tilde fence was not read as a diagram: %v, %v", diagrams, problems)
	}
}

func TestAHeadRefusalIsRetriedAsAGet(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := writeDocs(t, map[string]string{"README.md": "# T\n\n[here](" + srv.URL + ")\n"})
	if problems := CheckLinks(dir, true, srv.Client()); len(problems) != 0 {
		t.Fatalf("a host that answers only GET was reported: %v", problems)
	}
	if len(methods) != 2 || methods[0] != http.MethodHead || methods[1] != http.MethodGet {
		t.Errorf("the retry did not happen: %v", methods)
	}
}

func TestAnUnreachableHostIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	target := srv.URL
	srv.Close()

	dir := writeDocs(t, map[string]string{"README.md": "# T\n\n[gone](" + target + ")\n"})
	if problems := CheckLinks(dir, true, nil); len(problems) != 1 {
		t.Fatalf("an unreachable host was not reported: %v", problems)
	}
}

func TestALinkToADirectoryOrANonDocumentResolves(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"README.md":      "# T\n\n[dir](docs) and [file](docs/data.json#fragment)\n",
		"docs/data.json": "{}\n",
	})
	if problems := CheckLinks(dir, false, nil); len(problems) != 0 {
		t.Fatalf("a directory or a data file was reported: %v", problems)
	}
}

func TestAMissingComposeFileIsReported(t *testing.T) {
	if problems := checkCompose(filepath.Join(t.TempDir(), "absent.yml"), false); len(problems) != 1 {
		t.Fatalf("a missing compose file was not reported: %v", problems)
	}
}

func TestASubcommandRunsOnItsOwn(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"README.md":          "# T\n\nNothing to resolve.\n",
		"docker-compose.yml": "services:\n  postgres:\n    image: postgres:17.6-alpine@sha256:" + pinnedDigest + "\n",
	})
	for _, command := range []string{"links", "refs", "images"} {
		args := []string{command, "-root", dir, "-compose", filepath.Join(dir, "docker-compose.yml")}
		if out, err := runCommand(t, args...); err != nil {
			t.Errorf("%s: %v\n%s", command, err, out)
		}
	}
	out, err := runCommand(t, "diagrams", "-root", dir, "-mermaid", "none")
	if err != nil || !strings.Contains(out, "structure only") {
		t.Errorf("diagrams: %v\n%s", err, out)
	}
}

func TestGenerationFailsWhenTheDirectoryIsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docs")
	if err := os.WriteFile(path, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write the file: %v", err)
	}
	if _, err := runCommand(t, "generate", "-docs", path); err == nil {
		t.Fatal("the documents were written over a file")
	}
}

func TestARepeatedHeadingGetsItsOwnAnchor(t *testing.T) {
	anchors := Anchors("# Title\n\n## A section\n\n```md\n## A section\n```\n\n## A section\n")
	if !anchors["a-section"] || !anchors["a-section-1"] {
		t.Fatalf("a repeated heading has no second anchor: %v", anchors)
	}
	if len(anchors) != 3 {
		t.Errorf("a heading inside a code block was read as a heading: %v", anchors)
	}
}

func TestAReferenceStyleLinkIsResolved(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"README.md": "# T\n\nSee [the guide][guide].\n\n[guide]: docs/guide.md\n",
	})
	if problems := CheckLinks(dir, false, nil); len(problems) != 1 {
		t.Fatalf("a reference definition was not checked: %v", problems)
	}
}

func TestTheTokenExchangeFailuresAreReported(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"no token endpoint": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("WWW-Authenticate", "Bearer service=\"registry\"")
			w.WriteHeader(http.StatusUnauthorized)
		},
		"the token endpoint refuses": func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/token") {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+"http://"+r.Host+`/token"`)
			w.WriteHeader(http.StatusUnauthorized)
		},
		"the token is not readable": func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/token") {
				_, _ = w.Write([]byte("not json"))
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+"http://"+r.Host+`/token"`)
			w.WriteHeader(http.StatusUnauthorized)
		},
		"no token is issued": func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/token") {
				_, _ = w.Write([]byte(`{}`))
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+"http://"+r.Host+`/token"`)
			w.WriteHeader(http.StatusUnauthorized)
		},
		"no digest in the answer": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()
			registry := &Registry{Client: srv.Client(), BaseURL: srv.URL}
			if _, err := registry.Digest("library/postgres", "17.6-alpine"); err == nil {
				t.Fatal("the exchange was reported as successful")
			}
		})
	}
}

func TestAnIssuedAccessTokenIsAccepted(t *testing.T) {
	digest := "sha256:" + strings.Repeat("b", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/token") {
			_, _ = w.Write([]byte(`{"access_token":"issued"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer issued" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srvURL(r)+`/token"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Docker-Content-Digest", digest)
	}))
	defer srv.Close()

	// A registry with no client of its own uses the default one, which is the
	// path a command takes.
	registry := &Registry{BaseURL: srv.URL}
	got, err := registry.Digest("library/postgres", "17.6-alpine")
	if err != nil {
		t.Fatalf("read the digest: %v", err)
	}
	if got != digest {
		t.Errorf("the digest is %q, want %q", got, digest)
	}
}

// srvURL rebuilds the base URL of a test server from a request it served.
func srvURL(r *http.Request) string { return "http://" + r.Host }

func TestAnUnreachableRegistryIsReported(t *testing.T) {
	registry := &Registry{BaseURL: "http://127.0.0.1:1"}
	if _, err := registry.Digest("library/postgres", "17.6"); err == nil {
		t.Fatal("an unreachable registry was reported as successful")
	}
	if _, err := (&Registry{BaseURL: "://broken"}).Digest("library/postgres", "17.6"); err == nil {
		t.Fatal("an unusable base URL was accepted")
	}
}

func TestTheConfiguredRendererIsUsedAsGiven(t *testing.T) {
	var out strings.Builder
	if got := renderer("/opt/mermaid", &out); got != "/opt/mermaid" {
		t.Errorf("the configured renderer was replaced with %q", got)
	}
	if out.String() != "" {
		t.Errorf("a configured renderer produced a notice: %q", out.String())
	}
}
