package agentproto

import (
	"strings"
	"testing"
	"time"
)

func TestLogsSearchParamsValidate(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555555"
	if err := (LogsSearchParams{AppID: id}).Validate(); err != nil {
		t.Fatalf("a bare app id should validate: %v", err)
	}
	if err := (LogsSearchParams{AppID: id, Process: "worker:default", Query: "boom", Level: LogLevelError}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (LogsSearchParams{AppID: id, Regex: true, Query: `err(or)?\b`}).Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []LogsSearchParams{
		{AppID: "not-a-uuid"},
		{AppID: id, Process: "bogus process"},
		{AppID: id, Level: "critical"},
		{AppID: id, Regex: true, Query: "("}, // invalid RE2
		{AppID: id, Query: strings.Repeat("x", MaxLogSearchQueryLen+1)},
		{AppID: id, Regex: true, Query: strings.Repeat("x", MaxLogSearchRegexLen+1)},
		{AppID: id, Limit: MaxLogSearchLines + 1},
		{AppID: id, Limit: -1},
		{AppID: id, Since: "2026-01-02T00:00:00Z", Until: "2026-01-01T00:00:00Z"}, // until before since
		{AppID: id, Since: "2000-01-01T00:00:00Z", Until: "2026-01-01T00:00:00Z"}, // window too wide
		{AppID: id, Since: "not a time"},
	}
	for i, p := range bad {
		if p.Validate() == nil {
			t.Errorf("case %d: %+v should have been rejected", i, p)
		}
	}
}

func TestCompileLogSearchRegex(t *testing.T) {
	re, err := CompileLogSearchRegex("boom", false)
	if err != nil || !re.MatchString("kaboom!") {
		t.Fatal("plain regex should match a substring")
	}
	if re.MatchString("BOOM") {
		t.Fatal("case sensitive by default")
	}
	ci, err := CompileLogSearchRegex("boom", true)
	if err != nil || !ci.MatchString("BOOM") {
		t.Fatal("case_insensitive should fold case")
	}
	if _, err := CompileLogSearchRegex("(unterminated", false); err == nil {
		t.Fatal("expected a compile error")
	}
}

func TestParseLogSearchWindow(t *testing.T) {
	since, until, err := ParseLogSearchWindow("", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := until.Sub(since); got != time.Hour {
		t.Errorf("default window: got %s, want 1h", got)
	}

	since, until, err = ParseLogSearchWindow("6h", "")
	if err != nil {
		t.Fatal(err)
	}
	if d := until.Sub(since); d < 6*time.Hour-time.Second || d > 6*time.Hour+time.Second {
		t.Errorf("6h relative window: got %s", d)
	}

	since, until, err = ParseLogSearchWindow("7d", "")
	if err != nil {
		t.Fatal(err)
	}
	if d := until.Sub(since); d < 7*24*time.Hour-time.Second || d > 7*24*time.Hour+time.Second {
		t.Errorf("7d relative window: got %s", d)
	}

	since, until, err = ParseLogSearchWindow("2026-01-01T00:00:00Z", "2026-01-01T01:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !since.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) || !until.Equal(time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)) {
		t.Errorf("RFC 3339 bounds not honoured: %s .. %s", since, until)
	}

	if _, _, err := ParseLogSearchWindow("not a time", ""); err == nil {
		t.Fatal("expected an error")
	}
}
