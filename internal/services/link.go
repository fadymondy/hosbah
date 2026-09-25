package services

import (
	"context"

	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
)

// envSealContext must match deploy.EnvSealContext (internal/deploy/service.go) exactly — kept as a
// local copy, not an import, because internal/deploy imports internal/ops which (through the
// shared-services scan on the agent heartbeat) must be free to import this package.
func envSealContext(appID, name string) string { return "env:" + appID + ":" + name }

// DEKProvider unwraps a team's data key. *ops.Service satisfies this; kept as a local interface
// (not an import of internal/ops) because internal/ops calls into this package to scan shared
// services on the agent heartbeat, and internal/ops must not be imported back.
type DEKProvider interface {
	TeamDEK(ctx context.Context, q *db.Queries, teamID string) ([]byte, error)
}

// LinkOut is one detected or manual app-to-service link, ready for the API/MCP to show.
type LinkOut struct {
	Variable    string
	ServiceKind string // "shared" | "data"
	ServiceID   string
	Source      string // "auto" | "manual"
}

/*
Detect parses an app's env variables looking for connection endpoints (any var LooksLikeConnVar
names, or whose value parses as a URL or host:port — see ParseConnVar), matches them against the
team's shared services and the app's own server's data services, and replaces the app's automatic
links (source = auto; a manual link, made through Update, is never touched here) with what it
found. It opens each variable's sealed value only to compute scheme/host/port; the value itself
never leaves this function — it is not returned, not logged, and is discarded once parsed. Team
scope; the caller has already checked the app.
*/
func Detect(ctx context.Context, q *db.Queries, o DEKProvider, teamID string, a db.App) ([]LinkOut, error) {
	vars, err := q.ListEnvVarsOfApp(ctx, db.ListEnvVarsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	// Only open (decrypt) variables that look like a connection variable by name, or that we will
	// try to parse regardless — parsing every value would decrypt secrets that plainly are not
	// endpoints (API keys, tokens); LooksLikeConnVar plus a generic URL/host:port shape both apply
	// per variable inside the loop, but we still must open the value to know its shape.
	var dek []byte
	endpoints := map[string]ConnEndpoint{}
	for _, v := range vars {
		if dek == nil {
			if dek, err = o.TeamDEK(ctx, q, teamID); err != nil {
				return nil, err
			}
		}
		pt, err := crypto.Open(dek, teamID, envSealContext(a.ID, v.Name), v.ValueSealed)
		if err != nil {
			continue
		}
		ep, ok := ParseConnVar(string(pt))
		if ok {
			endpoints[v.Name] = ep
		}
	}
	if len(endpoints) == 0 {
		err := q.ReplaceAppServiceLinksAuto(ctx, db.ReplaceAppServiceLinksAutoParams{AppID: a.ID, TeamID: teamID})
		return nil, err
	}

	var candidates []Candidate
	shared, err := q.ListSharedServicesOfTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	for _, s := range shared {
		if s.Hidden || s.Host == nil || *s.Host == "" {
			continue
		}
		port := 0
		if s.Port != nil {
			port = int(*s.Port)
		}
		candidates = append(candidates, Candidate{Kind: "shared", ID: s.ID, Host: *s.Host, Port: port})
	}
	dataSvcs, err := q.ListDataServicesOfServer(ctx, db.ListDataServicesOfServerParams{ServerID: a.ServerID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	for _, d := range dataSvcs {
		if d.Host == nil || *d.Host == "" {
			continue
		}
		port := 0
		if d.Port != nil {
			port = int(*d.Port)
		}
		candidates = append(candidates, Candidate{Kind: "data", ID: d.ID, Host: *d.Host, Port: port})
	}

	matches := MatchLinks(endpoints, candidates)

	if err := q.ReplaceAppServiceLinksAuto(ctx, db.ReplaceAppServiceLinksAutoParams{AppID: a.ID, TeamID: teamID}); err != nil {
		return nil, err
	}
	out := make([]LinkOut, 0, len(matches))
	for _, m := range matches {
		row, err := q.UpsertAppServiceLink(ctx, db.UpsertAppServiceLinkParams{
			ID: sharedServiceID(a.ID, m.Variable), TeamID: teamID, AppID: a.ID,
			ServiceKind: m.Kind, ServiceID: m.ID, Variable: m.Variable, Source: "auto",
		})
		if err != nil {
			return nil, err
		}
		out = append(out, LinkOut{Variable: row.Variable, ServiceKind: row.ServiceKind, ServiceID: row.ServiceID, Source: row.Source})
	}
	return out, nil
}
