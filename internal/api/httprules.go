package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Site HTTP rules (MH-578 E4-E6): redirects, response headers, basic auth and IP/bot access rules
// for an app or a proxy host, rendered into that target's own Caddy route by the agent's rules.set
// op. See internal/deploy/httprules.go for the settings/agent-op logic and internal/agent/httprules.go
// for the Caddy JSON rendering. Reading is any member; changing rules is owner/admin (apps:
// AbilityManageApps, proxy hosts: AbilityManageServers), like every other write on those resources.
//
// Rate limiting is not offered here: see internal/deploy/httprules.go's package doc for why.

// RedirectRuleOut/HeaderRuleOut/etc mirror agentproto's wire types 1:1; kept separate so the REST
// surface does not change shape just because the agent wire format does.
type RedirectRuleOut struct {
	From          string `json:"from"`
	To            string `json:"to"`
	Code          int    `json:"code"`
	PreserveQuery bool   `json:"preserve_query"`
}

type HeaderRuleOut struct {
	Action string `json:"action" enum:"set,add,remove"`
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
}

type BasicAuthUserOut struct {
	Username string `json:"username"`
}

type BasicAuthRuleOut struct {
	Enabled     bool               `json:"enabled"`
	PathPrefix  string             `json:"path_prefix,omitempty"`
	ExemptPaths []string           `json:"exempt_paths,omitempty"`
	Users       []BasicAuthUserOut `json:"users,omitempty" doc:"Passwords are write-only; only usernames are ever read back."`
}

type AccessRulesOut struct {
	IPAllow      []string `json:"ip_allow,omitempty"`
	IPDeny       []string `json:"ip_deny,omitempty"`
	BlockBadBots bool     `json:"block_bad_bots,omitempty"`
}

// HTTPRulesOut is a site's HTTP rules and their last known apply status.
type HTTPRulesOut struct {
	Redirects []RedirectRuleOut `json:"redirects"`
	Headers   []HeaderRuleOut   `json:"headers"`
	BasicAuth *BasicAuthRuleOut `json:"basic_auth,omitempty"`
	Access    *AccessRulesOut   `json:"access,omitempty"`
	Status    string            `json:"status" doc:"ok | pending | error"`
	Error     *string           `json:"error,omitempty"`
}

// basicAuthUserIn is one basic-auth account on write: a plaintext password, hashed server-side and
// never stored or echoed back (internal/deploy.HashBasicAuthPassword).
type basicAuthUserIn struct {
	Username string `json:"username" minLength:"1" maxLength:"128"`
	Password string `json:"password,omitempty" minLength:"8" maxLength:"72" doc:"Required for a new username; omit to keep an existing user's current password."`
}

type basicAuthRuleIn struct {
	Enabled     bool              `json:"enabled"`
	PathPrefix  string            `json:"path_prefix,omitempty" maxLength:"2048"`
	ExemptPaths []string          `json:"exempt_paths,omitempty" maxItems:"20"`
	Users       []basicAuthUserIn `json:"users,omitempty" maxItems:"50"`
}

type httpRulesIn struct {
	Redirects []RedirectRuleOut `json:"redirects,omitempty" maxItems:"30"`
	Headers   []HeaderRuleOut   `json:"headers,omitempty" maxItems:"30"`
	BasicAuth *basicAuthRuleIn  `json:"basic_auth,omitempty"`
	Access    *AccessRulesOut   `json:"access,omitempty"`
}

func httpRulesOut(status string, errMsg *string, r agentproto.HTTPRules) HTTPRulesOut {
	out := HTTPRulesOut{Status: status, Error: errMsg, Redirects: []RedirectRuleOut{}, Headers: []HeaderRuleOut{}}
	for _, rr := range r.Redirects {
		out.Redirects = append(out.Redirects, RedirectRuleOut{From: rr.From, To: rr.To, Code: rr.Code, PreserveQuery: rr.PreserveQuery})
	}
	for _, h := range r.Headers {
		out.Headers = append(out.Headers, HeaderRuleOut{Action: h.Action, Name: h.Name, Value: h.Value})
	}
	if r.BasicAuth != nil {
		ba := &BasicAuthRuleOut{Enabled: r.BasicAuth.Enabled, PathPrefix: r.BasicAuth.PathPrefix, ExemptPaths: r.BasicAuth.ExemptPaths}
		for _, u := range r.BasicAuth.Users {
			ba.Users = append(ba.Users, BasicAuthUserOut{Username: u.Username})
		}
		out.BasicAuth = ba
	}
	if r.Access != nil {
		out.Access = &AccessRulesOut{IPAllow: r.Access.IPAllow, IPDeny: r.Access.IPDeny, BlockBadBots: r.Access.BlockBadBots}
	}
	return out
}

// toAgentRules turns the write body into the wire type, hashing any new basic-auth passwords and
// carrying over the bcrypt hash of any username whose password was omitted (kept as-is).
func toAgentRules(in httpRulesIn, existing agentproto.HTTPRules) (agentproto.HTTPRules, error) {
	existingHashes := map[string]string{}
	if existing.BasicAuth != nil {
		for _, u := range existing.BasicAuth.Users {
			existingHashes[u.Username] = u.BcryptHash
		}
	}
	var out agentproto.HTTPRules
	for _, r := range in.Redirects {
		out.Redirects = append(out.Redirects, agentproto.RedirectRule{From: r.From, To: r.To, Code: r.Code, PreserveQuery: r.PreserveQuery})
	}
	for _, h := range in.Headers {
		out.Headers = append(out.Headers, agentproto.HeaderRule{Action: h.Action, Name: h.Name, Value: h.Value})
	}
	if in.BasicAuth != nil {
		ba := &agentproto.BasicAuthRule{Enabled: in.BasicAuth.Enabled, PathPrefix: in.BasicAuth.PathPrefix, ExemptPaths: in.BasicAuth.ExemptPaths}
		for _, u := range in.BasicAuth.Users {
			hash, ok := existingHashes[u.Username]
			if u.Password != "" {
				h, err := deploy.HashBasicAuthPassword(u.Password)
				if err != nil {
					return out, err
				}
				hash = h
				ok = true
			}
			if !ok {
				return out, huma.Error422UnprocessableEntity("basic auth user " + u.Username + " needs a password (it is not an existing user)")
			}
			ba.Users = append(ba.Users, agentproto.BasicAuthUser{Username: u.Username, BcryptHash: hash})
		}
		out.BasicAuth = ba
	}
	if in.Access != nil {
		out.Access = &agentproto.AccessRules{IPAllow: in.Access.IPAllow, IPDeny: in.Access.IPDeny, BlockBadBots: in.Access.BlockBadBots}
	}
	return out, nil
}

func (d *deps) registerHTTPRules(api huma.API) {
	tags := []string{"HTTP rules"}

	appBase := "/api/teams/{team}/apps/{app}/rules"
	huma.Register(api, huma.Operation{OperationID: "get-app-http-rules", Method: http.MethodGet, Path: appBase, Tags: tags,
		Summary: "An app's redirects, response headers, basic auth and IP/bot access rules. Any member.",
	}, func(ctx context.Context, in *AppPath) (*struct{ Body HTTPRulesOut }, error) {
		out := &struct{ Body HTTPRulesOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			row, err := deploy.HTTPRulesOfApp(ctx, q, m.TeamID, a.ID)
			if err != nil {
				return huma.Error500InternalServerError("could not read the app's HTTP rules")
			}
			rules := deploy.DecodeHTTPRules(row.Redirects, row.Headers, row.BasicAuth, row.Access)
			out.Body = httpRulesOut(row.Status, row.Error, rules)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-app-http-rules", Method: http.MethodPut, Path: appBase, Tags: tags,
		Summary: "Replace an app's redirects, response headers, basic auth and IP/bot access rules. Applies to the app's server " +
			"at once. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body httpRulesIn
	}) (*struct{ Body HTTPRulesOut }, error) {
		out := &struct{ Body HTTPRulesOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			existingRow, err := deploy.HTTPRulesOfApp(ctx, q, m.TeamID, a.ID)
			if err != nil {
				return huma.Error500InternalServerError("could not read the app's HTTP rules")
			}
			existing := deploy.DecodeHTTPRules(existingRow.Redirects, existingRow.Headers, existingRow.BasicAuth, existingRow.Access)
			rules, err := toAgentRules(in.Body, existing)
			if err != nil {
				return err
			}
			s := d.deploys()
			if s == nil {
				return huma.Error500InternalServerError("deploy service unavailable")
			}
			row, err := s.SetHTTPRulesForApp(ctx, q, m.TeamID, webActor(id), a, rules)
			if err != nil {
				return actionErr(err, "could not update the app's HTTP rules")
			}
			out.Body = httpRulesOut(row.Status, row.Error, rules)
			return nil
		})
		return out, err
	})

	proxyBase := "/api/teams/{team}/proxy-hosts/{proxy}/rules"
	huma.Register(api, huma.Operation{OperationID: "get-proxy-host-http-rules", Method: http.MethodGet, Path: proxyBase, Tags: tags,
		Summary: "A proxy host's redirects, response headers, basic auth and IP/bot access rules. Any member.",
	}, func(ctx context.Context, in *ProxyHostPath) (*struct{ Body HTTPRulesOut }, error) {
		out := &struct{ Body HTTPRulesOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			p, err := deploy.ProxyHostByRef(ctx, q, m.TeamID, in.Proxy)
			if err != nil {
				return errNotFound
			}
			row, err := deploy.HTTPRulesOfProxyHost(ctx, q, m.TeamID, p.ID)
			if err != nil {
				return huma.Error500InternalServerError("could not read the proxy host's HTTP rules")
			}
			rules := deploy.DecodeHTTPRules(row.Redirects, row.Headers, row.BasicAuth, row.Access)
			out.Body = httpRulesOut(row.Status, row.Error, rules)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-proxy-host-http-rules", Method: http.MethodPut, Path: proxyBase, Tags: tags,
		Summary: "Replace a proxy host's redirects, response headers, basic auth and IP/bot access rules. Applies at once. " +
			"Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		ProxyHostPath
		Body httpRulesIn
	}) (*struct{ Body HTTPRulesOut }, error) {
		out := &struct{ Body HTTPRulesOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			p, err := deploy.ProxyHostByRef(ctx, q, m.TeamID, in.Proxy)
			if err != nil {
				return errNotFound
			}
			existingRow, err := deploy.HTTPRulesOfProxyHost(ctx, q, m.TeamID, p.ID)
			if err != nil {
				return huma.Error500InternalServerError("could not read the proxy host's HTTP rules")
			}
			existing := deploy.DecodeHTTPRules(existingRow.Redirects, existingRow.Headers, existingRow.BasicAuth, existingRow.Access)
			rules, err := toAgentRules(in.Body, existing)
			if err != nil {
				return err
			}
			s := d.deploys()
			if s == nil {
				return huma.Error500InternalServerError("deploy service unavailable")
			}
			row, err := s.SetHTTPRulesForProxyHost(ctx, q, m.TeamID, webActor(id), p, rules)
			if err != nil {
				return actionErr(err, "could not update the proxy host's HTTP rules")
			}
			out.Body = httpRulesOut(row.Status, row.Error, rules)
			return nil
		})
		return out, err
	})
}
