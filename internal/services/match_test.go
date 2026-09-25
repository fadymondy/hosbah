package services

import "testing"

func TestMatchLinks(t *testing.T) {
	candidates := []Candidate{
		{Kind: "shared", ID: "svc-pg", Host: "10.10.10.102", Port: 5432},
		{Kind: "shared", ID: "svc-redis", Host: "10.10.10.103", Port: 6379},
		{Kind: "data", ID: "svc-mysite-db", Host: "127.0.0.1", Port: 5433},
	}
	endpoints := map[string]ConnEndpoint{
		"DATABASE_URL":     {Scheme: "postgres", Host: "10.10.10.102", Port: 5432},
		"REDIS_URL":        {Scheme: "redis", Host: "10.10.10.103", Port: 6379},
		"APP_DATABASE_URL": {Scheme: "postgres", Host: "127.0.0.1", Port: 5433},
		"NO_MATCH_URL":     {Scheme: "http", Host: "1.2.3.4", Port: 80},
		"CASE_URL":         {Host: "10.10.10.102", Port: 5432}, // same host, exact case
	}
	matches := MatchLinks(endpoints, candidates)
	want := map[string]string{
		"DATABASE_URL":     "svc-pg",
		"REDIS_URL":        "svc-redis",
		"APP_DATABASE_URL": "svc-mysite-db",
		"CASE_URL":         "svc-pg",
	}
	got := map[string]string{}
	for _, m := range matches {
		got[m.Variable] = m.ID
	}
	if len(got) != len(want) {
		t.Fatalf("got %d matches, want %d: %+v", len(got), len(want), got)
	}
	for v, id := range want {
		if got[v] != id {
			t.Errorf("var %s matched %q, want %q", v, got[v], id)
		}
	}
	if _, ok := got["NO_MATCH_URL"]; ok {
		t.Error("NO_MATCH_URL should not have matched anything")
	}
}

func TestMatchLinksHostCaseInsensitive(t *testing.T) {
	candidates := []Candidate{{Kind: "shared", ID: "svc-1", Host: "Shared-Host.local", Port: 4222}}
	endpoints := map[string]ConnEndpoint{"NATS_URL": {Host: "shared-host.local", Port: 4222}}
	matches := MatchLinks(endpoints, candidates)
	if len(matches) != 1 || matches[0].ID != "svc-1" {
		t.Fatalf("expected a case-insensitive host match, got %+v", matches)
	}
}

func TestMatchLinksPortMismatch(t *testing.T) {
	candidates := []Candidate{{Kind: "shared", ID: "svc-1", Host: "10.0.0.5", Port: 5432}}
	endpoints := map[string]ConnEndpoint{"DATABASE_URL": {Host: "10.0.0.5", Port: 6379}}
	if matches := MatchLinks(endpoints, candidates); len(matches) != 0 {
		t.Fatalf("expected no match on port mismatch, got %+v", matches)
	}
}
