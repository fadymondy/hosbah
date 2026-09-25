package sitetemplate

import "strings"

import "testing"

func TestBuiltinsParseAndValidate(t *testing.T) {
	bs := Builtins()
	if len(bs) < 5 {
		t.Fatalf("expected at least 5 built-in templates, got %d", len(bs))
	}
	for _, b := range bs {
		if err := b.Template.Validate(); err != nil {
			t.Errorf("builtin %s: %v", b.Template.Name, err)
		}
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	_, err := Parse([]byte("name: x\ndriver: node-api\nrepo_url: https://github.com/a/b\nbogus: true\n"))
	if err == nil {
		t.Fatal("expected an error for an unknown key")
	}
}

func TestParseRejectsOversize(t *testing.T) {
	big := make([]byte, MaxYAMLBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	_, err := Parse(big)
	if err == nil {
		t.Fatal("expected an error for an oversized document")
	}
}

func TestValidateSecretCannotBeLiteral(t *testing.T) {
	tpl := Template{Name: "x", Driver: "node-api", RepoURL: "https://github.com/a/b",
		Env: []EnvEntry{{Name: "TOKEN", Value: "plain", Secret: true}}}
	if err := tpl.Validate(); err == nil {
		t.Fatal("expected an error: a secret env may not carry a literal value")
	}
}

func TestValidateFromNeedsNeeds(t *testing.T) {
	tpl := Template{Name: "x", Driver: "node-api", RepoURL: "https://github.com/a/b",
		Env: []EnvEntry{{Name: "DB_URL", From: FromPostgresURL, Secret: true}}}
	if err := tpl.Validate(); err == nil {
		t.Fatal("expected an error: from postgres_url without needs.postgres")
	}
}

func TestRenderFillsDefaultsAndGenerates(t *testing.T) {
	tpl := Template{Name: "x", Driver: "node-api", RepoURL: "https://github.com/a/b",
		Env: []EnvEntry{
			{Name: "SECRET", Generate: &Generate{Length: 16, Charset: "hex"}, Secret: true},
			{Name: "GREETING", Value: "hello-{{random:8}}"},
		}}
	if err := tpl.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	r, err := tpl.Render(ServiceURLs{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if r.Port != 8080 || r.HealthPath != "/health" || r.Branch != "main" {
		t.Fatalf("driver defaults not applied: %+v", r)
	}
	var secret, greeting string
	for _, e := range r.Env {
		if e.Name == "SECRET" {
			secret = e.Value
		}
		if e.Name == "GREETING" {
			greeting = e.Value
		}
	}
	if len(secret) != 16 {
		t.Fatalf("expected a 16-char generated secret, got %q", secret)
	}
	if !strings.HasPrefix(greeting, "hello-") || len(greeting) != len("hello-")+8 {
		t.Fatalf("expected the {{random:8}} placeholder filled, got %q", greeting)
	}
}

func TestRenderFromServiceURL(t *testing.T) {
	tpl := Template{Name: "x", Driver: "django", RepoURL: "https://github.com/a/b", Needs: Needs{Postgres: true},
		Env: []EnvEntry{{Name: "DATABASE_URL", From: FromPostgresURL, Secret: true}}}
	if err := tpl.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	r, err := tpl.Render(ServiceURLs{Postgres: "postgres://u:p@host:5432/db"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if r.Env[0].Value != "postgres://u:p@host:5432/db" {
		t.Fatalf("expected the postgres URL substituted, got %q", r.Env[0].Value)
	}
	if _, err := tpl.Render(ServiceURLs{}); err == nil {
		t.Fatal("expected an error: no postgres service URL given")
	}
}

func TestDriverByName(t *testing.T) {
	if _, ok := DriverByName("does-not-exist"); ok {
		t.Fatal("expected not found")
	}
	if _, ok := DriverByName("laravel"); !ok {
		t.Fatal("expected laravel to be a known driver")
	}
}
