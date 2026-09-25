package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// HTTPRulesTools: MH-578 E4-E6 — redirects, response headers, basic auth and IP/bot access rules for
// an app or a proxy host, rendered into that target's own Caddy route by the agent's rules.set op.
// See internal/deploy/httprules.go for the settings/agent-op logic and internal/agent/httprules.go
// for the Caddy JSON rendering. One get/set pair per target (app, proxy host), same shape as the
// edge cache tools: a full-replacement write, read back with the current status.
//
// Rate limiting is not offered here: see internal/deploy/httprules.go's package doc for why.
func HTTPRulesTools() []Tool {
	deploys := func(env *Env) *deploy.Service {
		s, _ := env.App.Deploy.(*deploy.Service)
		return s
	}

	rulesSchema := map[string]any{
		"redirects": map[string]any{"type": "array", "description": "Path redirects, applied before anything else.", "items": map[string]any{
			"type": "object", "required": []string{"from", "to"},
			"properties": map[string]any{
				"from":           str("Path or exact match to redirect from, e.g. /old"),
				"to":             str("Absolute URL or path to redirect to"),
				"code":           map[string]any{"type": "integer", "description": "301, 302 (default), 307 or 308"},
				"preserve_query": map[string]any{"type": "boolean", "description": "Append the original query string to the target"},
			},
		}},
		"headers": map[string]any{"type": "array", "description": "Response headers to set/add/remove.", "items": map[string]any{
			"type": "object", "required": []string{"action", "name"},
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []string{"set", "add", "remove"}},
				"name":   str("Header name, e.g. Strict-Transport-Security"),
				"value":  str("Header value (ignored for remove)"),
			},
		}},
		"basic_auth": map[string]any{"type": "object", "description": "HTTP basic auth on the whole site or a path prefix.",
			"properties": map[string]any{
				"enabled":      map[string]any{"type": "boolean"},
				"path_prefix":  str("Only require auth under this path prefix; empty means the whole site"),
				"exempt_paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Paths that never require auth, e.g. /api/health"},
				"users": map[string]any{"type": "array", "description": "Accounts. A username with no password keeps its existing password.", "items": map[string]any{
					"type": "object", "required": []string{"username"},
					"properties": map[string]any{"username": str("Username"), "password": str("Plaintext, 8-72 chars; omit to keep the current password for an existing username")},
				}},
			},
		},
		"access": map[string]any{"type": "object", "description": "IP allow/deny and bad-bot blocking.", "properties": map[string]any{
			"ip_allow":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "CIDRs; if set, only these may reach the site"},
			"ip_deny":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "CIDRs to block"},
			"block_bad_bots": map[string]any{"type": "boolean", "description": "Block a curated list of scraper/AI-crawler user agents"},
		}},
	}

	return []Tool{
		{
			Name: "app_http_rules_get", Scope: scopes.Read,
			Description: "An app's redirects, response headers, basic auth and IP/bot access rules, and the status of the last " +
				"time they were applied to the app's server.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": str("App id or name")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				row, err := deploy.HTTPRulesOfApp(ctx, env.Q, env.Caller.TeamID, a.ID)
				if err != nil {
					return "", errors.New("could not read the app's HTTP rules")
				}
				rules := deploy.DecodeHTTPRules(row.Redirects, row.Headers, row.BasicAuth, row.Access)
				return httpRulesSummary(row.Status, row.Error, rules), nil
			},
		},
		{
			Name: "app_http_rules_set", Scope: scopes.Write,
			Description: "Replace an app's redirects, response headers, basic auth and IP/bot access rules (each field replaces " +
				"the whole list/object; omit a field to clear it). Applies to the app's server at once. Owner or admin.",
			InputSchema: schema([]string{"app"}, mergeProps(map[string]any{"app": str("App id or name")}, rulesSchema)),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				existingRow, err := deploy.HTTPRulesOfApp(ctx, env.Q, env.Caller.TeamID, a.ID)
				if err != nil {
					return "", errors.New("could not read the app's HTTP rules")
				}
				existing := deploy.DecodeHTTPRules(existingRow.Redirects, existingRow.Headers, existingRow.BasicAuth, existingRow.Access)
				rules, err := rulesFromArgs(args, existing)
				if err != nil {
					return "", err
				}
				s := deploys(env)
				if s == nil {
					return "", errors.New("the deploy service is not running")
				}
				row, err := s.SetHTTPRulesForApp(ctx, env.Q, env.Caller.TeamID, deploy.Actor{UserID: actor(env), Via: "mcp", TokenID: env.Caller.TokenID}, a, rules)
				if err != nil {
					return "", err
				}
				return httpRulesSummary(row.Status, row.Error, rules), nil
			},
		},
		{
			Name: "proxy_host_http_rules_get", Scope: scopes.Read,
			Description: "A proxy host's redirects, response headers, basic auth and IP/bot access rules, and the status of the " +
				"last time they were applied.",
			InputSchema: schema([]string{"proxy"}, map[string]any{"proxy": str("Proxy host id or hostname")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				p, err := deploy.ProxyHostByRef(ctx, env.Q, env.Caller.TeamID, args.Str("proxy"))
				if err != nil {
					return "", errors.New("not found")
				}
				row, err := deploy.HTTPRulesOfProxyHost(ctx, env.Q, env.Caller.TeamID, p.ID)
				if err != nil {
					return "", errors.New("could not read the proxy host's HTTP rules")
				}
				rules := deploy.DecodeHTTPRules(row.Redirects, row.Headers, row.BasicAuth, row.Access)
				return httpRulesSummary(row.Status, row.Error, rules), nil
			},
		},
		{
			Name: "proxy_host_http_rules_set", Scope: scopes.Write,
			Description: "Replace a proxy host's redirects, response headers, basic auth and IP/bot access rules (each field " +
				"replaces the whole list/object; omit a field to clear it). Applies at once. Owner or admin.",
			InputSchema: schema([]string{"proxy"}, mergeProps(map[string]any{"proxy": str("Proxy host id or hostname")}, rulesSchema)),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				p, err := deploy.ProxyHostByRef(ctx, env.Q, env.Caller.TeamID, args.Str("proxy"))
				if err != nil {
					return "", errors.New("not found")
				}
				existingRow, err := deploy.HTTPRulesOfProxyHost(ctx, env.Q, env.Caller.TeamID, p.ID)
				if err != nil {
					return "", errors.New("could not read the proxy host's HTTP rules")
				}
				existing := deploy.DecodeHTTPRules(existingRow.Redirects, existingRow.Headers, existingRow.BasicAuth, existingRow.Access)
				rules, err := rulesFromArgs(args, existing)
				if err != nil {
					return "", err
				}
				s := deploys(env)
				if s == nil {
					return "", errors.New("the deploy service is not running")
				}
				row, err := s.SetHTTPRulesForProxyHost(ctx, env.Q, env.Caller.TeamID, deploy.Actor{UserID: actor(env), Via: "mcp", TokenID: env.Caller.TokenID}, p, rules)
				if err != nil {
					return "", err
				}
				return httpRulesSummary(row.Status, row.Error, rules), nil
			},
		},
	}
}

// rulesFromArgs builds agentproto.HTTPRules from raw MCP tool arguments (already-decoded JSON,
// unlike the REST body which huma validates and types for us). Existing is used to carry over a
// basic-auth user's bcrypt hash when its password is omitted.
func rulesFromArgs(args Args, existing agentproto.HTTPRules) (agentproto.HTTPRules, error) {
	raw, err := json.Marshal(map[string]any(args))
	if err != nil {
		return agentproto.HTTPRules{}, err
	}
	var in struct {
		Redirects []agentproto.RedirectRule `json:"redirects"`
		Headers   []agentproto.HeaderRule   `json:"headers"`
		BasicAuth *struct {
			Enabled     bool     `json:"enabled"`
			PathPrefix  string   `json:"path_prefix"`
			ExemptPaths []string `json:"exempt_paths"`
			Users       []struct {
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"users"`
		} `json:"basic_auth"`
		Access *agentproto.AccessRules `json:"access"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return agentproto.HTTPRules{}, fmt.Errorf("bad rules: %w", err)
	}
	existingHashes := map[string]string{}
	if existing.BasicAuth != nil {
		for _, u := range existing.BasicAuth.Users {
			existingHashes[u.Username] = u.BcryptHash
		}
	}
	out := agentproto.HTTPRules{Redirects: in.Redirects, Headers: in.Headers, Access: in.Access}
	if in.BasicAuth != nil {
		ba := &agentproto.BasicAuthRule{Enabled: in.BasicAuth.Enabled, PathPrefix: in.BasicAuth.PathPrefix, ExemptPaths: in.BasicAuth.ExemptPaths}
		for _, u := range in.BasicAuth.Users {
			hash, ok := existingHashes[u.Username]
			if u.Password != "" {
				h, herr := deploy.HashBasicAuthPassword(u.Password)
				if herr != nil {
					return agentproto.HTTPRules{}, herr
				}
				hash, ok = h, true
			}
			if !ok {
				return agentproto.HTTPRules{}, fmt.Errorf("basic auth user %s needs a password (it is not an existing user)", u.Username)
			}
			ba.Users = append(ba.Users, agentproto.BasicAuthUser{Username: u.Username, BcryptHash: hash})
		}
		out.BasicAuth = ba
	}
	return out, nil
}

func httpRulesSummary(status string, errMsg *string, r agentproto.HTTPRules) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("status: %s", status))
	if errMsg != nil && *errMsg != "" {
		parts = append(parts, "error: "+*errMsg)
	}
	parts = append(parts, fmt.Sprintf("%d redirect(s)", len(r.Redirects)))
	parts = append(parts, fmt.Sprintf("%d header rule(s)", len(r.Headers)))
	if r.BasicAuth != nil && r.BasicAuth.Enabled {
		parts = append(parts, fmt.Sprintf("basic auth on (%d user(s))", len(r.BasicAuth.Users)))
	} else {
		parts = append(parts, "basic auth off")
	}
	if r.Access != nil {
		parts = append(parts, fmt.Sprintf("access: %d allow CIDR(s), %d deny CIDR(s), bad bots %s",
			len(r.Access.IPAllow), len(r.Access.IPDeny), onOff(r.Access.BlockBadBots, "on", "off")))
	}
	raw, _ := json.Marshal(r)
	return strings.Join(parts, "; ") + ". Rules: " + string(raw)
}
