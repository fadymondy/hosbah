// Command migrate applies Cloudy's versioned migrations through the ledger in
// internal/migrate. Usage: go run ./cmd/migrate [status]   (also: togo migrate)
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/togo-framework/togo"

	"github.com/fadymondy/cloudy/internal/config"
	"github.com/fadymondy/cloudy/internal/migrate"
)

func main() {
	config.LoadDotEnv()      // togo.LoadConfig reads only the process environment
	cfg := togo.LoadConfig() // same resolution as the server
	if cfg.DatabaseURL == "" {
		fail("DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		fail("open: %v", err)
	}
	defer db.Close()

	status := len(os.Args) > 1 && os.Args[1] == "status"
	if err := migrate.Run(context.Background(), db, migrate.RepoDir(migrate.Dir), status, os.Stdout); err != nil {
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "migrate: "+format+"\n", args...)
	os.Exit(1)
}
