// Package migrate is Hosbah's migration ledger.
//
// It applies the versioned files in db/atlas/migrations and records each one in
// schema_migrations. togo 0.2.31's stock migrate re-executed every schema
// fragment on every run with no record of what had been applied; this replaces it:
//
//   - files apply in lexical (= version) order, each in its own transaction, and
//     the ledger row is written in that same transaction;
//   - an applied file whose checksum has changed stops the run: history is not
//     rewritten in place, you add a new migration;
//   - a Postgres advisory lock serialises concurrent runs (two deploys at once).
//
// Used by cmd/migrate (`togo migrate`) and by the integration tests, which migrate
// their own database before running.
package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Dir is the versioned migration directory, relative to the repo root.
const Dir = "db/atlas/migrations"

const lockKey = 7_326_104_118 // arbitrary and stable: never change it (old and new binaries must share the lock)

// Migration is one versioned file.
type Migration struct {
	Version  string // file name without .sql, e.g. 20260914101053_init
	SQL      string
	Checksum string
}

// Load reads every *.sql in dir, sorted by version.
func Load(dir string) ([]Migration, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	out := make([]Migration, 0, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f) //#nosec G304 -- repo migration files
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		out = append(out, Migration{
			Version:  strings.TrimSuffix(filepath.Base(f), ".sql"),
			SQL:      string(b),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}
	return out, nil
}

// Run applies pending migrations from dir (or only reports them when statusOnly).
func Run(ctx context.Context, db *sql.DB, dir string, statusOnly bool, w io.Writer) error {
	conn, err := db.Conn(ctx) // advisory locks are per session: hold one connection
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, lockKey) //nolint:errcheck

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    text PRIMARY KEY,
		checksum   text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("ledger: %w", err)
	}

	applied := map[string]string{}
	rows, err := conn.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v, c string
		if err := rows.Scan(&v, &c); err != nil {
			rows.Close()
			return err
		}
		applied[v] = c
	}
	rows.Close()

	migs, err := Load(dir)
	if err != nil {
		return err
	}
	if len(migs) == 0 {
		return fmt.Errorf("no migrations found in %s", dir)
	}

	pending := 0
	for _, m := range migs {
		sum, done := applied[m.Version]
		switch {
		case done && sum != m.Checksum:
			return fmt.Errorf("%s was edited after it was applied (ledger %s…, file %s…) — add a new migration instead",
				m.Version, sum[:12], m.Checksum[:12])
		case done:
			if statusOnly {
				fmt.Fprintf(w, "applied  %s\n", m.Version)
			}
			continue
		}
		pending++
		if statusOnly {
			fmt.Fprintf(w, "PENDING  %s\n", m.Version)
			continue
		}
		if err := apply(ctx, conn, m); err != nil {
			return fmt.Errorf("%s: %w", m.Version, err)
		}
		fmt.Fprintf(w, "applied  %s\n", m.Version)
	}
	if !statusOnly {
		fmt.Fprintf(w, "migrate complete (%d applied, %d total)\n", pending, len(migs))
	}
	return nil
}

func apply(ctx context.Context, conn *sql.Conn, m Migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)`, m.Version, m.Checksum); err != nil {
		return err
	}
	return tx.Commit()
}

// RepoDir finds dir by walking up from the working directory, so a test running
// in internal/api still finds db/atlas/migrations.
func RepoDir(dir string) string {
	wd, err := os.Getwd()
	if err != nil {
		return dir
	}
	for {
		candidate := filepath.Join(wd, dir)
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			return dir
		}
		wd = parent
	}
}
