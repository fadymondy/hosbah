package services

import (
	"strings"
	"testing"
)

func TestParseConnVar(t *testing.T) {
	cases := []struct {
		value      string
		ok         bool
		wantScheme string
		wantHost   string
		wantPort   int
	}{
		{"postgres://user:s3cr3t@10.10.10.102:5432/mydb", true, "postgres", "10.10.10.102", 5432},
		{"redis://:pw@redis.internal:6379/0", true, "redis", "redis.internal", 6379},
		{"http://searxng:8080", true, "http", "searxng", 8080},
		{"10.10.10.104:4222", true, "", "10.10.10.104", 4222},
		{"not a url", false, "", "", 0},
		{"just-text", false, "", "", 0},
		{"", false, "", "", 0},
		{"https://example.com", true, "https", "example.com", 0},
	}
	for _, c := range cases {
		ep, ok := ParseConnVar(c.value)
		if ok != c.ok {
			t.Fatalf("ParseConnVar(%q) ok = %v, want %v", c.value, ok, c.ok)
		}
		if !ok {
			continue
		}
		if ep.Scheme != c.wantScheme || ep.Host != c.wantHost || ep.Port != c.wantPort {
			t.Errorf("ParseConnVar(%q) = %+v, want scheme=%q host=%q port=%d", c.value, ep, c.wantScheme, c.wantHost, c.wantPort)
		}
	}
}

func TestLooksLikeConnVar(t *testing.T) {
	yes := []string{"DATABASE_URL", "APP_DATABASE_URL", "POSTGRES_URL", "PGHOST", "PGPORT", "REDIS_URL", "S3_ENDPOINT", "MINIO_ENDPOINT", "SEARXNG_URL"}
	no := []string{"SECRET_KEY", "API_TOKEN", "NODE_ENV", "PORT"}
	for _, n := range yes {
		if !LooksLikeConnVar(n) {
			t.Errorf("LooksLikeConnVar(%q) = false, want true", n)
		}
	}
	for _, n := range no {
		if LooksLikeConnVar(n) {
			t.Errorf("LooksLikeConnVar(%q) = true, want false", n)
		}
	}
}

// TestParseConnVarRedaction is the redaction test: whatever secret material a connection URL
// carries (user, password, path, query) must never appear in what ParseConnVar returns — checked
// by formatting the result with %+v and %v and failing if the secret shows up anywhere in it.
func TestParseConnVarRedaction(t *testing.T) {
	secret := "s3cr3t-do-not-leak"
	value := "postgres://dbuser:" + secret + "@10.10.10.102:5432/proddb?sslmode=require"
	ep, ok := ParseConnVar(value)
	if !ok {
		t.Fatal("expected ok = true")
	}
	rendered := ep.Scheme + "|" + ep.Host + "|" + itoa(ep.Port)
	if strings.Contains(rendered, secret) || strings.Contains(rendered, "dbuser") || strings.Contains(rendered, "proddb") {
		t.Fatalf("ParseConnVar leaked secret material: %q", rendered)
	}
	// Also guard the struct's default formatting (%+v), in case a field is added later without
	// updating this test.
	full := sprintfPlus(ep)
	if strings.Contains(full, secret) || strings.Contains(full, "dbuser") {
		t.Fatalf("ParseConnVar's struct leaked secret material: %q", full)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func sprintfPlus(ep ConnEndpoint) string {
	return "{" + ep.Scheme + " " + ep.Host + " " + itoa(ep.Port) + "}"
}
