// Command assethash prints the hashed entry asset of the embedded frontend
// bundle.
//
// The build passes the value to the linker, /version reports it, and the deploy
// smoke compares the two. That is how a rollout proves the running binary
// serves the bundle the release built, rather than an older one an image layer
// happened to carry.
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/example/reference-service/internal/web"
)

func main() { os.Exit(main1(os.Stdout, os.Stderr, web.Assets())) }

// main1 holds the body so a test can drive the exit code without starting a
// process. The embedded tree is the tree the binary serves, so the stamped
// value cannot describe a bundle the binary does not hold.
func main1(out, errOut io.Writer, fsys fs.FS) int {
	if err := run(out, fsys); err != nil {
		_, _ = fmt.Fprintln(errOut, "assethash:", err)
		return 1
	}
	return 0
}

// run reads the entry asset the shell document references and writes it as one
// line, which is the form the make variable reads.
func run(out io.Writer, fsys fs.FS) error {
	name, err := web.EntryAsset(fsys)
	if err != nil {
		return fmt.Errorf("%w; run: make frontend-embed", err)
	}
	_, err = fmt.Fprintln(out, name)
	return err
}
