package services

import "strings"

// Candidate is one shared or data service a link can point at.
type Candidate struct {
	Kind string // "shared" | "data" — app_service_links.service_kind
	ID   string
	Host string
	Port int
}

// Match is one app env var matched to a candidate.
type Match struct {
	Variable string
	Kind     string
	ID       string
}

// MatchLinks matches each env var's endpoint against the candidates by host (case-insensitive
// exact, or IP equal) and port (exact, or the endpoint had none and the candidate has exactly one
// well-known port for its kind). Pure: no I/O, for unit tests. Each var matches at most one
// candidate, the first whose host and port both agree.
func MatchLinks(endpoints map[string]ConnEndpoint, candidates []Candidate) []Match {
	var out []Match
	for _, name := range sortedKeys(endpoints) {
		ep := endpoints[name]
		if ep.Host == "" {
			continue
		}
		for _, c := range candidates {
			if c.Host == "" || !strings.EqualFold(c.Host, ep.Host) {
				continue
			}
			if ep.Port != 0 && c.Port != 0 && ep.Port != c.Port {
				continue
			}
			out = append(out, Match{Variable: name, Kind: c.Kind, ID: c.ID})
			break
		}
	}
	return out
}

func sortedKeys(m map[string]ConnEndpoint) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// stable, deterministic order for tests; not performance-sensitive (env var counts are small)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
