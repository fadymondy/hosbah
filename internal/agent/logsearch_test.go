package agent

import (
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func TestBuildLogMatcherPlain(t *testing.T) {
	m, err := buildLogMatcher(agentproto.LogsSearchParams{Query: "boom"})
	if err != nil {
		t.Fatal(err)
	}
	if !m("it went kaboom!") {
		t.Error("substring should match")
	}
	if m("BOOM") {
		t.Error("case sensitive by default")
	}
}

func TestBuildLogMatcherCaseInsensitive(t *testing.T) {
	m, err := buildLogMatcher(agentproto.LogsSearchParams{Query: "boom", CaseInsensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if !m("BOOM") {
		t.Error("case_insensitive should fold case")
	}
}

func TestBuildLogMatcherRegex(t *testing.T) {
	m, err := buildLogMatcher(agentproto.LogsSearchParams{Query: `err(or)?\b`, Regex: true})
	if err != nil {
		t.Fatal(err)
	}
	if !m("connection err") || !m("fatal error occurred") {
		t.Error("regex should match both forms")
	}
	if m("no problem here") {
		t.Error("should not match unrelated text")
	}
	if _, err := buildLogMatcher(agentproto.LogsSearchParams{Query: "(", Regex: true}); err == nil {
		t.Error("expected a compile error")
	}
}

func TestBuildLogMatcherEmptyQuery(t *testing.T) {
	m, err := buildLogMatcher(agentproto.LogsSearchParams{})
	if err != nil {
		t.Fatal(err)
	}
	if !m("") || !m("anything at all") {
		t.Error("an empty query should match every line")
	}
}

func TestDetectLevel(t *testing.T) {
	cases := map[string]string{
		"panic: nil pointer dereference":          agentproto.LogLevelError,
		"ERROR failed to connect":                 agentproto.LogLevelError,
		"request failed with status 500":          agentproto.LogLevelError,
		"WARN: deprecated flag used":              agentproto.LogLevelWarn,
		"this is a warning about memory":          agentproto.LogLevelWarn,
		"GET /health 200 OK":                      "",
		"no errors found in this batch, actually": "", // "errors" is not the whole word "error"
	}
	for line, want := range cases {
		if got := detectLevel(line); got != want {
			t.Errorf("detectLevel(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestSplitDockerTimestamp(t *testing.T) {
	at, rest, ok := splitDockerTimestamp("2026-09-25T10:00:00.123456789Z hello world")
	if !ok {
		t.Fatal("expected ok")
	}
	if rest != "hello world" {
		t.Errorf("rest = %q", rest)
	}
	if at.UTC().Format(time.RFC3339) != "2026-09-25T10:00:00Z" {
		t.Errorf("at = %s", at)
	}
	if _, _, ok := splitDockerTimestamp("not a timestamped line"); ok {
		t.Error("a line with no valid timestamp should report ok=false")
	}
	if _, _, ok := splitDockerTimestamp("no-space-at-all"); ok {
		t.Error("a line with no space should report ok=false")
	}
}

func TestDockerLogsSearchArgs(t *testing.T) {
	args := dockerLogsSearchArgs("hosbah-myapp-web-abc123", "2026-01-01T00:00:00Z", "2026-01-01T01:00:00Z")
	want := []string{"logs", "--timestamps", "--since", "2026-01-01T00:00:00Z", "--until", "2026-01-01T01:00:00Z", "hosbah-myapp-web-abc123"}
	if len(args) != len(want) {
		t.Fatalf("got %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("got %v, want %v", args, want)
		}
	}
	noUntil := dockerLogsSearchArgs("c1", "2026-01-01T00:00:00Z", "")
	for _, a := range noUntil {
		if a == "--until" {
			t.Error("an empty until should not add --until")
		}
	}
}
