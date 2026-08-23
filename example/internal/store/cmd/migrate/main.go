// Command migrate applies the pending database migrations, or checks the
// migration files without a database.
//
// It is a separate step on purpose. The serving process never applies
// migrations: two replicas starting together would race, and a start-up
// migration ties a schema change to rollout timing.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/example/reference-service/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

// run holds the body so a test drives the command without starting a process.
// The environment is injected for the same reason.
func run(ctx context.Context, args []string, out io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(out)
	dir := fs.String("dir", "migrations", "directory holding the migration files")
	dsn := fs.String("dsn", "", "database connection string, defaulting to DATABASE_URL")
	check := fs.Bool("check", false,
		"read the migration files and run the compatibility check without connecting")
	if err := fs.Parse(args); err != nil {
		return err
	}

	migrations := os.DirFS(*dir)
	migs, err := store.Load(migrations)
	if err != nil {
		return err
	}
	if err := store.CheckCompatibility(migs); err != nil {
		return err
	}
	if *check {
		_, err := fmt.Fprintf(out, "%s: %d %s, backward compatible\n", *dir, len(migs), plural(len(migs)))
		return err
	}

	url := *dsn
	if url == "" {
		url = getenv("DATABASE_URL")
	}
	if url == "" {
		return errors.New("no connection string: pass -dsn or set DATABASE_URL")
	}
	if err := store.Migrate(ctx, url, migrations); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s: %d %s applied or already present\n", *dir, len(migs), plural(len(migs)))
	return err
}

// plural keeps the report readable for a directory holding one migration.
func plural(n int) string {
	if n == 1 {
		return "migration"
	}
	return "migrations"
}
