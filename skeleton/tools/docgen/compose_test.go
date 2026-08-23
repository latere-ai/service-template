package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const pinnedDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func composeWith(image string) string {
	return "services:\n  postgres:\n    image: " + image + "\n"
}

func TestAnUnpinnedImageFails(t *testing.T) {
	cases := map[string]struct {
		image string
		want  string
	}{
		"no digest":     {"postgres:17.6-alpine", "is not pinned by digest"},
		"latest":        {"postgres:latest@sha256:" + pinnedDigest, "uses the latest tag"},
		"floating tag":  {"postgres:17@sha256:" + pinnedDigest, "names no major and minor version"},
		"digest only":   {"postgres@sha256:" + pinnedDigest, "is not pinned by digest"},
		"short digest":  {"postgres:17.6@sha256:abcdef", "is not pinned by digest"},
		"no version at": {"postgres:alpine@sha256:" + pinnedDigest, "names no major and minor version"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			problems := CheckImages("docker-compose.yml", composeWith(c.image))
			if len(problems) != 1 {
				t.Fatalf("want one problem, got %v", problems)
			}
			if !strings.Contains(problems[0], c.want) {
				t.Errorf("the failure does not say what is wrong: %v", problems[0])
			}
			if !strings.Contains(problems[0], "docker-compose.yml:3") {
				t.Errorf("the failure does not name the file and the line: %v", problems[0])
			}
		})
	}
}

func TestAPinnedImagePasses(t *testing.T) {
	problems := CheckImages("docker-compose.yml", composeWith("postgres:17.6-alpine@sha256:"+pinnedDigest))
	if len(problems) != 0 {
		t.Fatalf("a pinned image was reported: %v", problems)
	}
}

func TestAStackWithNoImageFails(t *testing.T) {
	if problems := CheckImages("docker-compose.yml", "services:\n  postgres:\n    build: .\n"); len(problems) != 1 {
		t.Fatalf("a stack with no image passed: %v", problems)
	}
}

// The stack committed in this repository is pinned, which is the rule that
// keeps a local run and a deployed run the same versions.
func TestTheCommittedStackIsPinned(t *testing.T) {
	text, err := readFile(repoRoot + "/docker-compose.yml")
	if err != nil {
		t.Fatalf("read the compose file: %v", err)
	}
	if problems := CheckImages("docker-compose.yml", text); len(problems) != 0 {
		t.Fatalf("the committed stack is not pinned:\n  %s", strings.Join(problems, "\n  "))
	}
}

// fakeRegistry answers the manifest request the way a public registry does:
// an anonymous challenge, a token endpoint, and the digest in a header.
func fakeRegistry(t *testing.T, digest string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			if r.URL.Query().Get("scope") == "" {
				t.Errorf("the token request carries no scope: %s", r.URL)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"issued"}`))
		case strings.HasPrefix(r.URL.Path, "/v2/"):
			if r.Header.Get("Authorization") != "Bearer issued" {
				w.Header().Set("WWW-Authenticate",
					`Bearer realm="`+srv.URL+`/token",service="registry",scope="repository:library/postgres:pull"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Docker-Content-Digest", digest)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestResolveReportsAStalePin(t *testing.T) {
	current := "sha256:" + strings.Repeat("a", 64)
	srv := fakeRegistry(t, current)
	registry := &Registry{Client: srv.Client(), BaseURL: srv.URL}

	text := composeWith("postgres:17.6-alpine@sha256:" + pinnedDigest)
	problems := ResolveImages("docker-compose.yml", text, registry)
	if len(problems) != 1 || !strings.Contains(problems[0], current) {
		t.Fatalf("a stale pin was not reported with the current digest: %v", problems)
	}

	text = composeWith("postgres:17.6-alpine@" + current)
	if problems := ResolveImages("docker-compose.yml", text, registry); len(problems) != 0 {
		t.Fatalf("a current pin was reported: %v", problems)
	}
}

func TestResolveReportsARegistryFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	problems := ResolveImages("docker-compose.yml",
		composeWith("postgres:17.6-alpine@sha256:"+pinnedDigest),
		&Registry{Client: srv.Client(), BaseURL: srv.URL})
	if len(problems) != 1 || !strings.Contains(problems[0], "answered 500") {
		t.Fatalf("a registry failure was not reported: %v", problems)
	}
}

func TestReferencesResolveToTheirRegistryAndNamespace(t *testing.T) {
	cases := map[string][2]string{
		"postgres":                 {defaultRegistry, defaultNamespace + "/postgres"},
		"library/postgres":         {defaultRegistry, "library/postgres"},
		"ghcr.io/owner/service":    {"ghcr.io", "owner/service"},
		"localhost:5000/service":   {"localhost:5000", "service"},
		"registry.example.com/a/b": {"registry.example.com", "a/b"},
	}
	for reference, want := range cases {
		host, path := splitReference(reference)
		if host != want[0] || path != want[1] {
			t.Errorf("%s resolved to %s and %s, want %s and %s", reference, host, path, want[0], want[1])
		}
	}
}
