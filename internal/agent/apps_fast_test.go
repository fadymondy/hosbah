package agent

import (
	"errors"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// TestRunFastStepsRunsEnabledStepsInOrder is the fast track's step runner (MH-573 A7), with a fake
// command runner standing in for `docker exec` — no Docker daemon needed.
func TestRunFastStepsRunsEnabledStepsInOrder(t *testing.T) {
	var ran [][]string
	run := func(args ...string) (int, error) {
		ran = append(ran, append([]string(nil), args...))
		return 0, nil
	}
	steps := []agentproto.FastBuildStep{
		{Key: "install", Name: "Install", Run: "npm ci"},
		{Key: "build", Name: "Build", Run: "npm run build"},
		{Key: "migrate", Name: "Migrate", Run: ""}, // disabled steps arrive with an empty Run — skipped
		{Key: "on_failure", Run: "echo failed"},
	}
	err, code := runFastSteps(steps, run, func(string, ...any) {})
	if err != nil || code != 0 {
		t.Fatalf("want no error, got %v (code %d)", err, code)
	}
	if len(ran) != 2 {
		t.Fatalf("want 2 commands run (install, build), got %d: %v", len(ran), ran)
	}
	if ran[0][2] != "npm ci" || ran[1][2] != "npm run build" {
		t.Fatalf("steps ran out of order or with the wrong command: %v", ran)
	}
}

// A failing step runs on_failure, best-effort, and reports the failing step's error and code.
func TestRunFastStepsRunsOnFailureAndReportsTheFailure(t *testing.T) {
	var ran []string
	run := func(args ...string) (int, error) {
		cmd := args[len(args)-1]
		ran = append(ran, cmd)
		if cmd == "npm run build" {
			return 1, errors.New("exit status 1")
		}
		return 0, nil
	}
	steps := []agentproto.FastBuildStep{
		{Key: "install", Run: "npm ci"},
		{Key: "build", Run: "npm run build"},
		{Key: "migrate", Run: "should not run"},
		{Key: "on_failure", Run: "echo cleanup"},
	}
	err, code := runFastSteps(steps, run, func(string, ...any) {})
	if err == nil || code != 1 {
		t.Fatalf("want the build step's failure reported, got err=%v code=%d", err, code)
	}
	if len(ran) != 3 || ran[0] != "npm ci" || ran[1] != "npm run build" || ran[2] != "echo cleanup" {
		t.Fatalf("want install, build, then on_failure (and no migrate); got %v", ran)
	}
}

func TestRunFastStepsNoStepsIsOK(t *testing.T) {
	err, code := runFastSteps(nil, func(args ...string) (int, error) { return 0, nil }, func(string, ...any) {})
	if err != nil || code != 0 {
		t.Fatalf("no steps must be a no-op success, got %v/%d", err, code)
	}
}

// TestFastCopyArgs (MH-573 A7b): the builder mode's generated `FROM <base>\nCOPY …` Dockerfile —
// "." copies the whole checkout, anything else keeps its relative path under /app.
func TestFastCopyArgs(t *testing.T) {
	cases := []struct{ in, want string }{
		{".", ". /app/"},
		{".next", ".next /app/.next"},
		{"public", "public /app/public"},
		{"/etc/passwd", "etc/passwd /app/etc/passwd"}, // a leading slash is stripped, never escapes /app
	}
	for _, c := range cases {
		if got := fastCopyArgs(c.in); got != c.want {
			t.Errorf("fastCopyArgs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
