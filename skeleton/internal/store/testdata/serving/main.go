// This file is the negative fixture for the gate that keeps migrations out of
// the serving process. It is never compiled: the go tool ignores testdata.
package main

import (
	"context"
	"os"

	"example.com/service/internal/store"
)

func main() {
	// The call the gate exists to catch.
	if err := store.Migrate(context.Background(), os.Getenv("DATABASE_URL"), os.DirFS("migrations")); err != nil {
		panic(err)
	}
}
