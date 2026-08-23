package web

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestTheEmbeddedBundleCarriesSomeShell(t *testing.T) {
	document, err := fs.ReadFile(Assets(), ShellDocument)
	if err != nil {
		t.Fatalf("the embedded bundle holds no %s: %v", ShellDocument, err)
	}
	if len(document) == 0 {
		t.Fatal("the embedded shell is empty")
	}
	res := do(t, Handler(Assets(), []string{"/v1"}), "GET", "/dashboard", nil)
	if res.StatusCode != 200 {
		t.Errorf("status %d, want 200 from the embedded bundle", res.StatusCode)
	}
	if got := body(t, res); got != string(document) {
		t.Error("the embedded shell is not what a client route is served")
	}
}

// TestAReleaseRefusesThePlaceholderBundle is the gate that keeps a binary with
// no built frontend from shipping. The release build runs the test suite with
// RELEASE=1, which is the same value the build target reads.
func TestAReleaseRefusesThePlaceholderBundle(t *testing.T) {
	if os.Getenv("RELEASE") != "1" {
		t.Skip("set RELEASE=1 to assert the embedded bundle is a built frontend")
	}
	if err := CheckRelease(Assets()); err != nil {
		t.Fatalf("this binary must not be released: %v", err)
	}
}

func TestCheckRelease(t *testing.T) {
	placeholder, err := fs.ReadFile(Assets(), ShellDocument)
	if err != nil {
		t.Fatalf("read the placeholder: %v", err)
	}
	if !strings.Contains(string(placeholder), PlaceholderMarker) {
		t.Fatalf("the checked-in %s must carry the marker %q", ShellDocument, PlaceholderMarker)
	}

	cases := []struct {
		name string
		fsys fs.FS
		want error
	}{
		{
			name: "the checked-in placeholder",
			fsys: Assets(),
			want: ErrPlaceholderBundle,
		},
		{
			name: "a shell with no bundle behind it",
			fsys: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}},
			want: ErrPlaceholderBundle,
		},
		{
			name: "no shell at all",
			fsys: fstest.MapFS{"assets/index-C3xK9pQ2.js": &fstest.MapFile{Data: []byte(entryScript)}},
			want: ErrNoBundle,
		},
		{
			name: "a built bundle",
			fsys: testFS(),
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckRelease(c.fsys)
			if c.want == nil {
				if err != nil {
					t.Fatalf("CheckRelease: %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("CheckRelease: %v, want %v", err, c.want)
			}
			if !strings.Contains(err.Error(), ShellDocument) {
				t.Errorf("the failure %q must name %s", err, ShellDocument)
			}
		})
	}
}

// TestCheckReleaseReportsAnUnreadableBundle keeps a walk failure from reading
// as a passing gate.
func TestCheckReleaseReportsAnUnreadableBundle(t *testing.T) {
	fsys := failingWalkFS{fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(shell)},
	}}
	if err := CheckRelease(fsys); err == nil {
		t.Fatal("an unreadable bundle must fail the release check")
	}
}

// failingWalkFS reads the shell and fails the directory listing behind it.
type failingWalkFS struct{ fstest.MapFS }

func (f failingWalkFS) ReadDir(string) ([]fs.DirEntry, error) {
	return nil, errors.New("unreadable")
}

func TestEntryAsset(t *testing.T) {
	got, err := EntryAsset(testFS())
	if err != nil {
		t.Fatalf("EntryAsset: %v", err)
	}
	if want := "assets/index-C3xK9pQ2.js"; got != want {
		t.Errorf("EntryAsset: %q, want %q", got, want)
	}
	if _, err := EntryAsset(fstest.MapFS{}); !errors.Is(err, ErrNoBundle) {
		t.Errorf("EntryAsset with no shell: %v, want ErrNoBundle", err)
	}
}

func TestParseEntryAsset(t *testing.T) {
	cases := []struct {
		name     string
		document string
		want     string
		wantErr  bool
	}{
		{
			name:     "a module entry wins over a classic script",
			document: `<script src="/legacy.js"></script><script type="module" src="/assets/index-C3xK9pQ2.js"></script>`,
			want:     "assets/index-C3xK9pQ2.js",
		},
		{
			name:     "attribute order and quoting do not matter",
			document: `<script defer crossorigin src='assets/app.9f1c2ab4.js' type='module'></script>`,
			want:     "assets/app.9f1c2ab4.js",
		},
		{
			name:     "a classic script is the fallback",
			document: `<script src="/bundle.js"></script>`,
			want:     "bundle.js",
		},
		{
			name:     "an external script is not the entry asset",
			document: `<script src="https://cdn.example.com/a.js"></script><script src="/bundle.js"></script>`,
			want:     "bundle.js",
		},
		{
			name:     "an inline script carries no entry asset",
			document: `<script>console.log(1)</script>`,
			wantErr:  true,
		},
		{
			name:     "the placeholder references nothing",
			document: `<html><body></body></html>`,
			wantErr:  true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseEntryAsset([]byte(c.document))
			if c.wantErr {
				if err == nil {
					t.Fatalf("ParseEntryAsset: %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEntryAsset: %v", err)
			}
			if got != c.want {
				t.Errorf("ParseEntryAsset: %q, want %q", got, c.want)
			}
		})
	}
}
