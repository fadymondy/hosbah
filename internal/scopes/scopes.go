// Package scopes names what a team MCP token may do. Every user-MCP tool declares
// exactly one of these; a token carries a set of them. There is no "any valid
// token" tier — the fix for fadymondy.com's FM-22, where a tool without a Scope
// was callable by every token.
package scopes

import (
	"sort"
	"strings"
)

const (
	Read        = "read"         // list/get anything except secret values
	Deploy      = "deploy"       // deploy, roll back, restart (P2)
	Write       = "write"        // create/change resources, invite members
	SecretsRead = "secrets:read" // reveal secret values (audited)
	Destructive = "destructive"  // delete/drop/restore; also needs confirm = resource name
)

// All is every scope, in display order.
var All = []string{Read, Deploy, Write, SecretsRead, Destructive}

// Valid reports whether s is a known scope.
func Valid(s string) bool {
	for _, v := range All {
		if v == s {
			return true
		}
	}
	return false
}

// Normalize returns the known, de-duplicated, sorted subset of in, and whether every entry was known.
func Normalize(in []string) (out []string, ok bool) {
	ok = true
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if !Valid(s) {
			ok = false
			continue
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out, ok
}

// Parse reads the stored comma-separated form.
func Parse(stored string) []string {
	out, _ := Normalize(strings.Split(stored, ","))
	return out
}

// Join is the stored form.
func Join(s []string) string { return strings.Join(s, ",") }

// Has reports whether granted contains want.
func Has(granted []string, want string) bool {
	for _, g := range granted {
		if g == want {
			return true
		}
	}
	return false
}
