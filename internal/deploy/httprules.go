package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Per-site HTTP rules (MH-578 E4-E6): redirects, response headers, basic auth and IP/bot access
// rules for an app or a proxy host, rendered into that target's own Caddy route by the agent's
// rules.set op (internal/agent/httprules.go, internal/agentproto/httprules.go). Settings live in
// app_httprules / proxy_host_httprules, one row per target, upserted on every save; status/error
// mirror the agent's most recent rules.set result, the same way app_edge_caches does (edgecache.go).
//
// Rate limiting (part of MH-578 E6) is NOT implemented: core Caddy (the plain release tarball this
// agent installs — see cache.go's design note) ships no rate-limit handler module, and there is no
// way to add one short of rebuilding Caddy with xcaddy, which this agent's install path deliberately
// avoids (see tools.go). Shipping IP/bot rules without rate limits, rather than blocking the whole
// feature on it, was the explicit call for this change.

var httpRulesTimeout = 30 * time.Second

// HTTPRulesOfApp reads an app's HTTP rules, or a zero (never configured) value when none exist yet.
func HTTPRulesOfApp(ctx context.Context, q *db.Queries, teamID, appID string) (db.AppHTTPRule, error) {
	row, err := q.AppHTTPRuleOfApp(ctx, db.AppHTTPRuleOfAppParams{TeamID: teamID, AppID: appID})
	if errors.Is(err, sql.ErrNoRows) {
		return db.AppHTTPRule{AppID: appID, TeamID: teamID, Status: "ok", Redirects: "[]", Headers: "[]", BasicAuth: "{}", Access: "{}"}, nil
	}
	return row, err
}

// HTTPRulesOfProxyHost reads a proxy host's HTTP rules, or a zero value when none exist yet.
func HTTPRulesOfProxyHost(ctx context.Context, q *db.Queries, teamID, proxyHostID string) (db.ProxyHostHTTPRule, error) {
	row, err := q.ProxyHostHTTPRuleOfHost(ctx, db.ProxyHostHTTPRuleOfHostParams{TeamID: teamID, ProxyHostID: proxyHostID})
	if errors.Is(err, sql.ErrNoRows) {
		return db.ProxyHostHTTPRule{ProxyHostID: proxyHostID, TeamID: teamID, Status: "ok", Redirects: "[]", Headers: "[]", BasicAuth: "{}", Access: "{}"}, nil
	}
	return row, err
}

// DecodeHTTPRules reads back the jsonb-encoded rules columns into the typed shape. Blank/invalid
// values (never configured yet, or a row from before a column existed) decode to zero values, never
// an error — this is display/read-back, not the validated path (that is HTTPRules.Validate, run on
// every SetHTTPRulesForApp/ForProxyHost).
func DecodeHTTPRules(redirects, headers, basicAuth, access string) agentproto.HTTPRules {
	var r agentproto.HTTPRules
	_ = json.Unmarshal([]byte(redirects), &r.Redirects)
	_ = json.Unmarshal([]byte(headers), &r.Headers)
	if basicAuth != "" && basicAuth != "{}" {
		var ba agentproto.BasicAuthRule
		if json.Unmarshal([]byte(basicAuth), &ba) == nil {
			r.BasicAuth = &ba
		}
	}
	if access != "" && access != "{}" {
		var a agentproto.AccessRules
		if json.Unmarshal([]byte(access), &a) == nil {
			r.Access = &a
		}
	}
	return r
}

// HashBasicAuthPassword bcrypt-hashes a plaintext password for a basic-auth account. The plaintext
// itself is never stored or sent to an agent — only this hash is (agentproto.BasicAuthUser.BcryptHash,
// checked narrowly by agentproto.ValidBcryptHash on both sides of the wire).
func HashBasicAuthPassword(plaintext string) (string, error) {
	if len(plaintext) < 8 {
		return "", invalid("basic auth passwords must be at least 8 characters")
	}
	if len(plaintext) > 72 {
		// bcrypt itself silently ignores bytes beyond 72; refuse rather than pretend a longer
		// password was accepted.
		return "", invalid("basic auth passwords must be at most 72 characters")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

func encodeRulesColumns(r agentproto.HTTPRules) (redirects, headers, basicAuth, access string, err error) {
	rb, err := json.Marshal(nonNilRedirects(r.Redirects))
	if err != nil {
		return "", "", "", "", err
	}
	hb, err := json.Marshal(nonNilHeaders(r.Headers))
	if err != nil {
		return "", "", "", "", err
	}
	ba := "{}"
	if r.BasicAuth != nil {
		b, err := json.Marshal(r.BasicAuth)
		if err != nil {
			return "", "", "", "", err
		}
		ba = string(b)
	}
	ac := "{}"
	if r.Access != nil {
		b, err := json.Marshal(r.Access)
		if err != nil {
			return "", "", "", "", err
		}
		ac = string(b)
	}
	return string(rb), string(hb), ba, ac, nil
}

func nonNilRedirects(r []agentproto.RedirectRule) []agentproto.RedirectRule {
	if r == nil {
		return []agentproto.RedirectRule{}
	}
	return r
}

func nonNilHeaders(h []agentproto.HeaderRule) []agentproto.HeaderRule {
	if h == nil {
		return []agentproto.HeaderRule{}
	}
	return h
}

// SetHTTPRulesForApp changes an app's HTTP rules, applies them on the app's server at once (agent
// rules.set), and records the result. Owner/admin only — enforced by the caller (AbilityManageApps).
func (s *Service) SetHTTPRulesForApp(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, rules agentproto.HTTPRules) (db.AppHTTPRule, error) {
	if err := rules.Validate(); err != nil {
		return db.AppHTTPRule{}, invalid(err.Error())
	}
	redirects, headers, basicAuth, access, err := encodeRulesColumns(rules)
	if err != nil {
		return db.AppHTTPRule{}, invalid("bad rules")
	}
	row, err := q.UpsertAppHTTPRule(ctx, db.UpsertAppHTTPRuleParams{
		AppID: a.ID, TeamID: teamID, Redirects: redirects, Headers: headers, BasicAuth: basicAuth, Access: access, CreatedBy: actor.UserID,
	})
	if err != nil {
		return db.AppHTTPRule{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "app.http_rules_updated", "app", a.ID, actor.audit(map[string]any{
		"redirects": len(rules.Redirects), "headers": len(rules.Headers),
		"basic_auth": rules.BasicAuth != nil && rules.BasicAuth.Enabled,
	})); err != nil {
		return row, err
	}
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
	if err != nil || srv.AgentPublicKey == nil {
		_, _ = q.SetAppHTTPRuleStatus(ctx, db.SetAppHTTPRuleStatusParams{Status: "error", Error: strPtr("that app's server has no agent enrolled yet"), TeamID: teamID, AppID: a.ID})
		return row, conflict("that app's server has no agent enrolled yet")
	}
	params := agentproto.HTTPRulesSetParams{TargetType: agentproto.HTTPRuleTargetApp, TargetID: a.ID, Rules: rules}
	raw, _ := json.Marshal(params)
	op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: srv.ID, Kind: agentproto.KindHTTPRulesSet, Params: raw, CreatedBy: actor.UserID})
	if err != nil {
		return row, err
	}
	status, errMsg := "ok", (*string)(nil)
	if _, aerr := s.AwaitResult(ctx, op, httpRulesTimeout, &struct{}{}); aerr != nil {
		status = "error"
		msg := aerr.Error()
		errMsg = &msg
	}
	updated, serr := q.SetAppHTTPRuleStatus(ctx, db.SetAppHTTPRuleStatusParams{Status: status, Error: errMsg, TeamID: teamID, AppID: a.ID})
	if serr != nil {
		return row, serr
	}
	if errMsg != nil {
		return updated, errors.New(*errMsg)
	}
	return updated, nil
}

// SetHTTPRulesForProxyHost is SetHTTPRulesForApp's proxy-host equivalent.
func (s *Service) SetHTTPRulesForProxyHost(ctx context.Context, q *db.Queries, teamID string, actor Actor, p db.ProxyHost, rules agentproto.HTTPRules) (db.ProxyHostHTTPRule, error) {
	if err := rules.Validate(); err != nil {
		return db.ProxyHostHTTPRule{}, invalid(err.Error())
	}
	redirects, headers, basicAuth, access, err := encodeRulesColumns(rules)
	if err != nil {
		return db.ProxyHostHTTPRule{}, invalid("bad rules")
	}
	row, err := q.UpsertProxyHostHTTPRule(ctx, db.UpsertProxyHostHTTPRuleParams{
		ProxyHostID: p.ID, TeamID: teamID, Redirects: redirects, Headers: headers, BasicAuth: basicAuth, Access: access, CreatedBy: actor.UserID,
	})
	if err != nil {
		return db.ProxyHostHTTPRule{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "proxy_host.http_rules_updated", "proxy_host", p.ID, actor.audit(map[string]any{
		"redirects": len(rules.Redirects), "headers": len(rules.Headers),
		"basic_auth": rules.BasicAuth != nil && rules.BasicAuth.Enabled,
	})); err != nil {
		return row, err
	}
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: p.ServerID, TeamID: teamID})
	if err != nil || srv.AgentPublicKey == nil {
		_, _ = q.SetProxyHostHTTPRuleStatus(ctx, db.SetProxyHostHTTPRuleStatusParams{Status: "error", Error: strPtr("that server has no agent enrolled yet"), TeamID: teamID, ProxyHostID: p.ID})
		return row, conflict("that server has no agent enrolled yet")
	}
	params := agentproto.HTTPRulesSetParams{TargetType: agentproto.HTTPRuleTargetProxy, TargetID: p.ID, Rules: rules}
	raw, _ := json.Marshal(params)
	op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: srv.ID, Kind: agentproto.KindHTTPRulesSet, Params: raw, CreatedBy: actor.UserID})
	if err != nil {
		return row, err
	}
	status, errMsg := "ok", (*string)(nil)
	if _, aerr := s.AwaitResult(ctx, op, httpRulesTimeout, &struct{}{}); aerr != nil {
		status = "error"
		msg := aerr.Error()
		errMsg = &msg
	}
	updated, serr := q.SetProxyHostHTTPRuleStatus(ctx, db.SetProxyHostHTTPRuleStatusParams{Status: status, Error: errMsg, TeamID: teamID, ProxyHostID: p.ID})
	if serr != nil {
		return row, serr
	}
	if errMsg != nil {
		return updated, errors.New(*errMsg)
	}
	return updated, nil
}
