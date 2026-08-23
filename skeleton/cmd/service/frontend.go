package main

import (
	"example.com/service/internal/httpx"
	"example.com/service/internal/web"
)

// This file is present when the repository selected the frontend feature. It
// assigns the seam the entry point calls, so the entry point itself never names
// the web package and compiles without it.
func init() { mountFrontend = serveShell }

// serveShell replaces the fallback with the embedded single-page application.
//
// It is the lowest route precedence on purpose: an application route answers
// first, and everything else is a client-side route the shell resolves in the
// browser. The version prefixes are declared so an unmatched path under the
// interface is answered with the error envelope rather than with the shell,
// which is what keeps a typo in a client call a 404 and not an HTML document.
func serveShell(a *assembly) error {
	a.fallback = web.Handler(web.Assets(), []string{httpx.Prefix(httpx.CurrentMajor)})
	return nil
}
