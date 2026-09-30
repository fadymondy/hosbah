package mcp

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	circlexo "github.com/circlexo/circlexo-go"

	"github.com/fadymondy/hosbah/internal/app"
	hub "github.com/fadymondy/hosbah/internal/circlexo"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Federated MCP: /api/mcp also accepts an access token the CircleXO hub issued (the hub's MCP gateway,
or a client that got one from the hub's authorization server). It is verified against the hub's JWKS
with the audience set to Hosbah's app id (the SDK's Verifier), and mapped to the same Caller a
hbu_ token gets:

	token subject -> Hosbah user   circlexo_user_links (the person must have signed in with CircleXO
	                               once, so a link exists)
	token org_id  -> team          circlexo_org_links, ACTIVE only
	membership    -> role          team_members, re-read on every call

An unlinked user, an unknown or deinstalled org, or a non-member is refused (403), never guessed.
hbu_/cld_ tokens are handled before this and are unchanged.

Scopes. The hub's gateway scopes (openid, profile, org, ...) say nothing about what a tool may do, so
they are not the tool scopes. Instead:
  - a token that names Hosbah tool scopes ("write") gets exactly those;
  - a token the hub gateway exchanged for an agent (it carries an act chain) gets read, write and
    deploy: never secrets:read (revealing a secret) or destructive (deleting), which stay with a
    human's own hbu_ token;
  - any other hub token gets read only.
Whatever the source, the user's CURRENT team role still gates every tool (needs), so an agent never
exceeds its human. The synthetic token id ("circlexo:<subject>[:<agent>]") is stable so approval
requests and the audit log can tell hub callers apart.
*/

// ErrDenied is a valid hub token that Hosbah refuses to act on (unlinked user, unconnected org, not a
// member). The transport answers 403 with Msg, so the caller can tell it from a bad token.
type ErrDenied struct{ Msg string }

func (e *ErrDenied) Error() string { return e.Msg }

func denied(msg string) error { return &ErrDenied{Msg: msg} }

// authHub authenticates a CircleXO-issued access token. It reports ok=false (a plain 401) for a token
// that is not a valid hub token, or when the integration is off.
func authHub(ctx context.Context, a *app.App, token string) (Caller, error, bool) {
	svc := hub.Current()
	if svc == nil || a == nil {
		return Caller{}, nil, false
	}
	claims, err := svc.Verifier.VerifyAccessToken(ctx, token)
	if err != nil || claims.Subject == "" || claims.IsService() {
		return Caller{}, nil, false
	}
	userID, err := svc.UserBySubject(ctx, claims.Subject)
	if errors.Is(err, sql.ErrNoRows) {
		return Caller{}, denied("this CircleXO user has not signed in to Hosbah yet: sign in with CircleXO once"), true
	}
	if err != nil {
		return Caller{}, ErrUnauthorized, true
	}
	if claims.OrgID == "" {
		return Caller{}, denied("this token names no organization"), true
	}
	link, err := svc.OrgByID(ctx, claims.OrgID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !link.Active) {
		return Caller{}, denied("this organization is not connected to a Hosbah team"), true
	}
	if err != nil {
		return Caller{}, ErrUnauthorized, true
	}

	var c Caller
	err = tenancy.Tx(ctx, a.SQLDB, tenancy.Scope{UserID: userID, TeamID: link.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		m, err := q.MembershipFor(ctx, db.MembershipForParams{TeamID: link.TeamID, UserID: userID})
		if err != nil {
			return denied("you are not a member of this team")
		}
		u, err := q.UserByID(ctx, userID)
		if err != nil || u.DisabledAt != nil {
			return ErrUnauthorized // a disabled account's tokens die with it
		}
		if at, err := q.TeamSuspendedAt(ctx, link.TeamID); err != nil {
			return ErrUnauthorized
		} else if at != nil {
			return ErrTeamSuspended
		}
		id := "circlexo:" + claims.Subject
		name := "CircleXO"
		if claims.AgentID != "" {
			id += ":" + claims.AgentID
			name = "CircleXO agent " + claims.AgentID
		}
		c = Caller{TokenID: id, TokenName: name, UserID: userID, Email: u.Email, TeamID: link.TeamID, Role: m.Role,
			Scopes: hubToolScopes(claims)}
		for a := claims.Actor; a != nil; a = a.Actor {
			c.Actors = append(c.Actors, a.Subject)
		}
		return nil
	})
	if err != nil {
		var d *ErrDenied
		if errors.As(err, &d) || errors.Is(err, ErrTeamSuspended) {
			return Caller{}, err, true
		}
		return Caller{}, ErrUnauthorized, true
	}
	return c, nil, true
}

// hubToolScopes is the Hosbah tool scopes a verified hub token stands for, before the role narrows
// them (see the comment at the top of this file).
func hubToolScopes(c *circlexo.Claims) []string {
	var named []string
	for _, sc := range c.Scopes() {
		if scopes.Valid(sc) {
			named = append(named, sc)
		}
	}
	if len(named) > 0 {
		out, _ := scopes.Normalize(named)
		return out
	}
	if c.Actor != nil {
		return slices.Clone([]string{scopes.Read, scopes.Write, scopes.Deploy})
	}
	return []string{scopes.Read}
}
