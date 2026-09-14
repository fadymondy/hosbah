// Package config loads the project's .env into the process environment.
//
// togo.LoadConfig reads only real environment variables — the kernel never
// opens .env itself (the togo CLI does, for `togo serve`). Every Cloudy
// entrypoint calls LoadDotEnv first so `go run ./cmd/api`, `go run ./cmd/migrate`,
// tests and the admin CLI all see the same configuration. Variables already set
// in the environment win, so production can inject real secrets without a file.
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// LoadDotEnv reads the first .env found walking up from the working directory
// (so tests run from a package directory still find the repo's file). A missing
// file is not an error.
func LoadDotEnv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		path := filepath.Join(dir, ".env")
		if f, err := os.Open(path); err == nil { //#nosec G304 -- the project's own .env
			parse(f)
			_ = f.Close()
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

func parse(f *os.File) {
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		// Strip an inline comment on unquoted values: FOO=bar   # note
		if len(val) > 0 && val[0] != '"' && val[0] != '\'' {
			if i := strings.Index(val, " #"); i >= 0 {
				val = strings.TrimSpace(val[:i])
			}
		}
		val = strings.Trim(val, `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}
