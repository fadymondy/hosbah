package mcp

import (
	"strings"
	"testing"
)

// A destructive result never carries a secret value, even if a tool were to put one there.
// This is a backstop for the rule "results never contain secrets": the tools are written not to
// echo values, and this is what happens when one day a new one does.
func TestScrubSecretsRedactsSecretShapedOutput(t *testing.T) {
	for _, tc := range []struct{ name, in, absent string }{
		{"mcp token", "deleted; token hbu_0123456789abcdef0123", "hbu_0123456789abcdef0123"},
		{"admin token", "minted hba_0123456789abcdef0123", "hba_0123456789abcdef0123"},
		{"enrolment token", "enrolment hbe_0123456789abcdef0123456789", "hbe_0123456789abcdef0123456789"},
		{"deploy hook", "hook hbh_0123456789abcdef0123456789", "hbh_0123456789abcdef0123456789"},
		// Pre-rename prefixes: those tokens still authenticate, so they are still secrets.
		{"legacy mcp token", "deleted; token cld_0123456789abcdef0123", "cld_0123456789abcdef0123"},
		{"legacy admin token", "minted cla_0123456789abcdef0123", "cla_0123456789abcdef0123"},
		{"legacy enrolment token", "enrolment cle_0123456789abcdef0123456789", "cle_0123456789abcdef0123456789"},
		{"legacy deploy hook", "hook cdh_0123456789abcdef0123456789", "cdh_0123456789abcdef0123456789"},
		{"sealed envelope", "sealed csv1:AAAABBBBCCCCDDDD", "csv1:AAAABBBBCCCCDDDD"},
		{"url password", "postgres://app:hunter2@10.0.0.1:5432/app", "hunter2"},
		{"redis url password", "redis://default:s3cr3tvalue@172.17.0.1:6379", "s3cr3tvalue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := scrubSecrets(tc.in)
			if strings.Contains(got, tc.absent) {
				t.Errorf("scrubSecrets(%q) still contains %q: %q", tc.in, tc.absent, got)
			}
			if !strings.Contains(got, "redacted") {
				t.Errorf("scrubSecrets(%q) should say something was redacted, got %q", tc.in, got)
			}
		})
	}
}

// A scrubber that mangles ordinary output is its own bug: every destructive tool's normal answer
// has to survive it unchanged, including the host:port pairs and names that appear in them.
func TestScrubSecretsLeavesOrdinaryOutputAlone(t *testing.T) {
	for _, plain := range []string{
		"Deleted sample-go. Its containers, route and images are removed from the server.",
		"go.cloudy.localhost no longer routes to sample-go.",
		"Server demo-1 forgotten.",
		"Restore-test of backup 04b568b5 of demo-pg started (a throwaway container).",
		"endpoint 172.17.0.1:5432",
		"see https://cloudy-app.fadymondy.com/servers/demo-1",
	} {
		if got := scrubSecrets(plain); got != plain {
			t.Errorf("scrubSecrets rewrote ordinary output:\n in: %q\nout: %q", plain, got)
		}
	}
}

// argsSummary is stored and shown to an approver, so it must carry argument NAMES and the
// resource only — never a value that a tool happened to be given.
func TestArgsSummaryNeverCopiesArgumentValues(t *testing.T) {
	tool := Tool{Name: "delete_app", ResourceType: "app"}
	got := argsSummary(tool, Args{
		"app":     "sample-go",
		"confirm": "sample-go",
		"secret":  "hunter2",
		"token":   "cld_0123456789abcdef0123",
	})
	for _, leaked := range []string{"hunter2", "cld_0123456789abcdef0123"} {
		if strings.Contains(got, leaked) {
			t.Errorf("argsSummary leaked an argument value %q: %s", leaked, got)
		}
	}
	// The names are what makes the summary useful to the approver.
	for _, want := range []string{"delete_app", "sample-go", "secret", "token", "app"} {
		if !strings.Contains(got, want) {
			t.Errorf("argsSummary should mention %q: %s", want, got)
		}
	}
}
