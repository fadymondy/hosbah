package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// setComposerPHPVersion edits composer.json's "require"."php" in place (MH-576 D2): Railpack's PHP
// provider reads the version only from there, not from an env var.
func TestSetComposerPHPVersion(t *testing.T) {
	t.Run("replaces an existing php constraint", func(t *testing.T) {
		src := `{
    "name": "acme/app",
    "require": {
        "php": "^8.1",
        "laravel/framework": "^11.0"
    }
}
`
		out, changed := setComposerPHPVersion(src, "8.3")
		if !changed {
			t.Fatal("want changed = true")
		}
		if !json.Valid([]byte(out)) {
			t.Fatalf("not valid JSON: %s", out)
		}
		if !strings.Contains(out, `"php": "8.3"`) {
			t.Fatalf("php constraint not rewritten: %s", out)
		}
		if strings.Contains(out, "8.1") {
			t.Fatalf("old constraint left behind: %s", out)
		}
		// The rest of the file (other keys, laravel/framework) is untouched.
		if !strings.Contains(out, `"name": "acme/app"`) || !strings.Contains(out, `"laravel/framework": "^11.0"`) {
			t.Fatalf("unrelated content changed: %s", out)
		}
	})

	t.Run("inserts a php constraint when require has none", func(t *testing.T) {
		src := `{
    "require": {
        "laravel/framework": "^11.0"
    }
}
`
		out, changed := setComposerPHPVersion(src, "8.2")
		if !changed {
			t.Fatal("want changed = true")
		}
		if !json.Valid([]byte(out)) {
			t.Fatalf("not valid JSON: %s", out)
		}
		if !strings.Contains(out, `"php": "8.2"`) || !strings.Contains(out, `"laravel/framework": "^11.0"`) {
			t.Fatalf("insert failed: %s", out)
		}
	})

	t.Run("inserts into an empty require object", func(t *testing.T) {
		src := `{
    "require": {}
}
`
		out, changed := setComposerPHPVersion(src, "8.4")
		if !changed {
			t.Fatal("want changed = true")
		}
		if !json.Valid([]byte(out)) {
			t.Fatalf("not valid JSON: %s", out)
		}
		if !strings.Contains(out, `"php": "8.4"`) {
			t.Fatalf("insert into empty require failed: %s", out)
		}
	})

	t.Run("reports unchanged when there is no require object", func(t *testing.T) {
		src := `{"name": "acme/app"}`
		out, changed := setComposerPHPVersion(src, "8.3")
		if changed {
			t.Fatal("want changed = false")
		}
		if out != src {
			t.Fatalf("source should be untouched: %s", out)
		}
	})
}

// pinComposerPHPVersion (MH-576 D2) is the app.build step that rewrites the checkout's
// composer.json ahead of `railpack prepare`; a missing composer.json or a "" / "auto" version is a
// no-op (not an error), just logged.
func TestPinComposerPHPVersion(t *testing.T) {
	t.Run("no-op for auto or empty", func(t *testing.T) {
		src := t.TempDir()
		oc, _ := opCtx(t, agentproto.BuildParams{})
		if err := pinComposerPHPVersion(oc, src, "auto"); err != nil {
			t.Fatalf("auto: %v", err)
		}
		if err := pinComposerPHPVersion(oc, src, ""); err != nil {
			t.Fatalf("empty: %v", err)
		}
		if _, err := os.Stat(filepath.Join(src, "composer.json")); !os.IsNotExist(err) {
			t.Fatal("should not have created composer.json")
		}
	})

	t.Run("rewrites an existing composer.json", func(t *testing.T) {
		src := t.TempDir()
		body := `{
    "require": {
        "php": "^8.1",
        "ext-mbstring": "*"
    }
}
`
		if err := os.WriteFile(filepath.Join(src, "composer.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		oc, log := opCtx(t, agentproto.BuildParams{})
		if err := pinComposerPHPVersion(oc, src, "8.3"); err != nil {
			t.Fatalf("pin: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(src, "composer.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(got) {
			t.Fatalf("not valid JSON: %s", got)
		}
		if !strings.Contains(string(got), `"php": "8.3"`) {
			t.Fatalf("php not pinned: %s", got)
		}
		if !strings.Contains(string(got), `"ext-mbstring": "*"`) {
			t.Fatalf("unrelated require entry lost: %s", got)
		}
		found := false
		for _, l := range *log {
			if strings.Contains(l, "pinned composer.json") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected a log line about the pin, got: %v", *log)
		}
	})

	t.Run("logs and continues when there is no composer.json", func(t *testing.T) {
		src := t.TempDir()
		oc, log := opCtx(t, agentproto.BuildParams{})
		if err := pinComposerPHPVersion(oc, src, "8.3"); err != nil {
			t.Fatalf("want no error, got %v", err)
		}
		found := false
		for _, l := range *log {
			if strings.Contains(l, "no composer.json") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected a log line about the missing composer.json, got: %v", *log)
		}
	})
}
