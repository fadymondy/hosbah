package agent

import (
	"context"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
Objects made before the rename from Cloudy. An agent built before 2026-09-19 named what it created
cloudy-… (containers, volumes), cloudy/<app> (images) and labelled it cloudy.*. This agent names
everything hosbah-…, and finds the old objects too (agentproto/names.go has the rules):

  - resolveName: a derived volume or container that already exists under its old name keeps it —
    the data in cloudy-vol-<app>-<slug> stays where it is — and only a new one gets the new name;
  - labelSets / psBoth: an app's containers are looked up under hosbah.* and cloudy.* labels, so a
    drain, a rollback or app.remove stops old-labelled containers as well.
*/

// objectKind is what `docker <kind> inspect` checks: "container" or "volume".
type objectKind string

const (
	kindContainer objectKind = "container"
	kindVolume    objectKind = "volume"
)

// dockerObjectExists reports whether a container or volume of that exact name exists.
func dockerObjectExists(ctx context.Context, run runFunc, kind objectKind, name string) bool {
	out, err := run(ctx, nil, "docker", string(kind), "inspect", "--format", "{{.Name}}", name)
	return err == nil && strings.TrimPrefix(strings.TrimSpace(string(out)), "/") == name
}

// resolveName is the name a derived object has on this host: its pre-rename name when an object of
// that name exists, else the current one. name may be given in either form.
func resolveName(ctx context.Context, run runFunc, kind objectKind, name string) string {
	cands := agentproto.NameCandidates(name)
	if len(cands) == 2 && dockerObjectExists(ctx, run, kind, cands[0]) {
		return cands[0]
	}
	return cands[len(cands)-1]
}

// labelSet is one generation of label keys; filter/format use the given keys in that generation.
type labelSet func(key string) string

// labelSets: the current labels first, then the pre-rename ones.
var labelSets = []labelSet{
	func(k string) string { return k },
	agentproto.LegacyLabel,
}

// psNames runs `docker ps` once per label generation and returns the union of the output lines, in
// order (current generation first: those are the newer containers). args builds one generation's
// arguments.
func psNames(ctx context.Context, run runFunc, args func(l labelSet) []string) ([]string, error) {
	var names []string
	seen := map[string]bool{}
	var firstErr error
	ok := false
	for _, l := range labelSets {
		out, err := run(ctx, nil, "docker", append([]string{"ps"}, args(l)...)...)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ok = true
		for _, line := range strings.Split(string(out), "\n") {
			if line = strings.TrimSpace(line); line != "" && !seen[line] {
				seen[line] = true
				names = append(names, line)
			}
		}
	}
	if !ok {
		return nil, firstErr
	}
	return names, nil
}
