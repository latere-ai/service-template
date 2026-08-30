// Package web serves a built single-page frontend from the binary.
//
// The package owns three decisions that a hand-written static handler usually
// gets wrong: a hard load of a client-side route returns the application shell
// instead of 404, an unknown path under an API prefix returns the JSON error
// envelope instead of markup, and the identity of the embedded bundle is
// recorded so a binary cannot silently ship an old frontend.
package web

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

// publicDir is the fixed directory the frontend build writes into and the
// binary embeds. all: is required because a bundler emits files whose names
// begin with a dot or an underscore, which the default pattern skips.
const publicDir = "public"

//go:embed all:public
var publicFS embed.FS

// PlaceholderMarker identifies the document that keeps publicDir present in
// version control before the frontend is built. A shipped bundle never carries
// it, and CheckRelease refuses a build that does.
const PlaceholderMarker = "template:frontend-placeholder"

// ShellDocument is the document served for a client-side route.
const ShellDocument = "index.html"

// ErrNoBundle reports that the embedded tree holds no shell document.
var ErrNoBundle = errors.New("the embedded bundle holds no " + ShellDocument)

// ErrPlaceholderBundle reports that the embedded tree is still the checked-in
// placeholder rather than a built frontend.
var ErrPlaceholderBundle = errors.New("the embedded bundle holds only the placeholder document")

// Assets returns the embedded bundle rooted at the document root, so the
// shell is reachable as "index.html".
func Assets() fs.FS {
	sub, err := fs.Sub(publicFS, publicDir)
	if err != nil {
		// The directory is embedded above, so a failure here means the build
		// tree lost it. Returning the unrooted tree keeps that visible as a
		// missing document rather than a panic on the request path.
		return publicFS
	}
	return sub
}

// CheckRelease reports whether a bundle is fit to ship. It requires both a
// shell document without the placeholder marker and at least one hashed asset,
// because either condition alone is met by a hand-written document with no
// bundle behind it.
func CheckRelease(fsys fs.FS) error {
	shell, err := fs.ReadFile(fsys, ShellDocument)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNoBundle, err)
	}
	if strings.Contains(string(shell), PlaceholderMarker) {
		return fmt.Errorf("%w: %s carries the marker %q",
			ErrPlaceholderBundle, ShellDocument, PlaceholderMarker)
	}
	hashed, err := hasHashedAsset(fsys)
	if err != nil {
		return err
	}
	if !hashed {
		return fmt.Errorf("%w: no hashed asset is present beside %s",
			ErrPlaceholderBundle, ShellDocument)
	}
	return nil
}

// hasHashedAsset reports whether the tree holds a file whose name carries a
// content hash, which is what a bundler emits and a hand-written document
// never has.
func hasHashedAsset(fsys fs.FS) (bool, error) {
	found := false
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || found {
			return nil
		}
		if hashedName(strings.TrimSuffix(strings.TrimSuffix(p, ".br"), ".gz")) {
			found = true
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("walk the frontend bundle: %w", err)
	}
	return found, nil
}

// EntryAsset reports the hashed entry asset the shell document references. The
// build passes it to the linker and /version reports it, so a deploy can prove
// the running binary serves the bundle the release built.
func EntryAsset(fsys fs.FS) (string, error) {
	shell, err := fs.ReadFile(fsys, ShellDocument)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoBundle, err)
	}
	return ParseEntryAsset(shell)
}

// scriptTag matches a script element and captures its attributes, so the entry
// asset is read from the document the browser reads rather than from a build
// manifest that can disagree with it.
var scriptTag = regexp.MustCompile(`(?is)<script\b([^>]*)>`)

// srcAttribute captures the value of a src attribute.
var srcAttribute = regexp.MustCompile(`(?is)\bsrc\s*=\s*["']([^"']+)["']`)

// moduleAttribute matches the module type a bundled entry point carries.
var moduleAttribute = regexp.MustCompile(`(?is)\btype\s*=\s*["']module["']`)

// ParseEntryAsset returns the entry asset a document references, as a path
// relative to the document root. A module script wins over a classic one,
// because a bundle emits the entry point as a module and any classic script is
// a polyfill or an analytics snippet.
func ParseEntryAsset(document []byte) (string, error) {
	var classic string
	for _, m := range scriptTag.FindAllSubmatch(document, -1) {
		attrs := m[1]
		src := srcAttribute.FindSubmatch(attrs)
		if src == nil {
			continue
		}
		name := strings.TrimPrefix(strings.TrimSpace(string(src[1])), "/")
		if name == "" || strings.Contains(name, "://") {
			continue
		}
		if moduleAttribute.Match(attrs) {
			return path.Clean(name), nil
		}
		if classic == "" {
			classic = path.Clean(name)
		}
	}
	if classic != "" {
		return classic, nil
	}
	return "", fmt.Errorf("the document references no entry script")
}
