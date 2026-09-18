package api

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/deploy"
	"github.com/fadymondy/cloudy/internal/tenancy"
)

// ProxyHostOut is one proxy host of the Caddy proxy manager.
type ProxyHostOut struct {
	ID          string     `json:"id"`
	ServerID    string     `json:"server_id"`
	ServerName  string     `json:"server_name,omitempty"`
	Hostname    string     `json:"hostname"`
	Upstream    string     `json:"upstream" doc:"host:port, http://host:port or https://host:port"`
	TLS         string     `json:"tls" enum:"auto,internal,off"`
	Enabled     bool       `json:"enabled"`
	Websockets  bool       `json:"websockets"`
	Verified    bool       `json:"verified"`
	VerifiedAt  *time.Time `json:"verified_at"`
	VerifyName  string     `json:"verify_name" doc:"TXT record name that proves control of the hostname"`
	VerifyValue string     `json:"verify_value"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func toProxyHostOut(p db.ProxyHost, serverName string) ProxyHostOut {
	return ProxyHostOut{ID: p.ID, ServerID: p.ServerID, ServerName: serverName, Hostname: p.Hostname, Upstream: p.Upstream, TLS: p.Tls,
		Enabled: p.Enabled, Websockets: p.Websockets, Verified: p.VerifiedAt != nil, VerifiedAt: p.VerifiedAt,
		VerifyName: "_cloudy-verify." + p.Hostname, VerifyValue: p.VerifyToken, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

// ProxyHostPath addresses one proxy host (id or hostname).
type ProxyHostPath struct {
	TeamPath
	Proxy string `path:"proxy" maxLength:"253"`
}

// ProxyHostChange is what can change on a proxy host.
type ProxyHostChange struct {
	Upstream   string `json:"upstream,omitempty" maxLength:"300" doc:"host:port, http://host:port or https://host:port"`
	TLS        string `json:"tls,omitempty" enum:"auto,internal,off" doc:"auto: public certificate once verified; internal: Caddy's local CA; off: plain HTTP."`
	Enabled    *bool  `json:"enabled,omitempty"`
	Websockets *bool  `json:"websockets,omitempty"`
	SkipVerify bool   `json:"skip_verify,omitempty" doc:"https upstream with a self-signed certificate: do not verify it."`
}

func (d *deps) registerProxyHosts(api huma.API) {
	tags := []string{"Proxy hosts"}

	type proxyListOut struct {
		Body struct {
			ProxyHosts []ProxyHostOut `json:"proxy_hosts"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-proxy-hosts", Method: http.MethodGet, Path: "/api/teams/{team}/proxy-hosts",
		Tags: tags, Summary: "The team's proxy hosts (Caddy proxy manager): hostname → upstream on a server",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Server string `query:"server" maxLength:"64" doc:"Only this server's (id or name)"`
	}) (*proxyListOut, error) {
		out := &proxyListOut{}
		out.Body.ProxyHosts = []ProxyHostOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			var only string
			if in.Server != "" {
				s, err := q.ServerInTeamByRef(ctx, db.ServerInTeamByRefParams{TeamID: m.TeamID, Ref: in.Server})
				if err != nil {
					return errNotFound
				}
				only = s.ID
			}
			rows, err := q.ListProxyHostsOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list proxy hosts")
			}
			for _, r := range rows {
				if only != "" && r.ServerID != only {
					continue
				}
				out.Body.ProxyHosts = append(out.Body.ProxyHosts, toProxyHostOut(db.ProxyHost{ID: r.ID, TeamID: r.TeamID, ServerID: r.ServerID,
					Hostname: r.Hostname, Upstream: r.Upstream, Tls: r.Tls, Enabled: r.Enabled, Websockets: r.Websockets, VerifyToken: r.VerifyToken,
					VerifiedAt: r.VerifiedAt, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}, r.ServerName))
			}
			return nil
		})
		return out, err
	})

	type proxyOneOut struct{ Body ProxyHostOut }
	huma.Register(api, huma.Operation{OperationID: "create-proxy-host", Method: http.MethodPost, Path: "/api/teams/{team}/proxy-hosts",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Put an upstream behind a hostname on one of the team's servers (owner or admin). With the team's Cloudflare " +
			"holding the zone, the DNS record is created and the hostname verified at once.",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body struct {
			Server   string `json:"server" minLength:"1" maxLength:"64" doc:"Server id or name whose Caddy serves the hostname"`
			Hostname string `json:"hostname" minLength:"3" maxLength:"253"`
			ProxyHostChange
		}
	}) (*proxyOneOut, error) {
		out := &proxyOneOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			b := in.Body
			p, err := d.deploys().CreateProxyHost(ctx, q, m.TeamID, webActor(id), deploy.ProxyHostInput{Server: b.Server, Hostname: b.Hostname,
				Upstream: b.Upstream, TLS: b.TLS, Enabled: b.Enabled, Websockets: b.Websockets, SkipVerify: b.SkipVerify})
			if err != nil {
				return actionErr(err, "could not add the proxy host")
			}
			out.Body = toProxyHostOut(p, "")
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "get-proxy-host", Method: http.MethodGet, Path: "/api/teams/{team}/proxy-hosts/{proxy}",
		Tags: tags, Summary: "One proxy host",
	}, func(ctx context.Context, in *ProxyHostPath) (*proxyOneOut, error) {
		out := &proxyOneOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			p, err := deploy.ProxyHostByRef(ctx, q, m.TeamID, in.Proxy)
			if err != nil {
				return errNotFound
			}
			out.Body = toProxyHostOut(p, "")
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-proxy-host", Method: http.MethodPatch, Path: "/api/teams/{team}/proxy-hosts/{proxy}",
		Tags: tags, Summary: "Change a proxy host's upstream, TLS mode or enabled flag (owner or admin); applied at once",
	}, func(ctx context.Context, in *struct {
		ProxyHostPath
		Body ProxyHostChange
	}) (*proxyOneOut, error) {
		out := &proxyOneOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			p, err := deploy.ProxyHostByRef(ctx, q, m.TeamID, in.Proxy)
			if err != nil {
				return errNotFound
			}
			b := in.Body
			u, err := d.deploys().UpdateProxyHost(ctx, q, m.TeamID, webActor(id), p, deploy.ProxyHostInput{Upstream: b.Upstream, TLS: b.TLS,
				Enabled: b.Enabled, Websockets: b.Websockets, SkipVerify: b.SkipVerify})
			if err != nil {
				return actionErr(err, "could not change the proxy host")
			}
			out.Body = toProxyHostOut(u, "")
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-proxy-host", Method: http.MethodDelete, Path: "/api/teams/{team}/proxy-hosts/{proxy}",
		DefaultStatus: http.StatusNoContent, Tags: tags, Summary: "Remove a proxy host from Caddy and Cloudy (owner or admin)",
	}, func(ctx context.Context, in *ProxyHostPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			p, err := deploy.ProxyHostByRef(ctx, q, m.TeamID, in.Proxy)
			if err != nil {
				return errNotFound
			}
			return actionErr(d.deploys().DeleteProxyHost(ctx, q, m.TeamID, webActor(id), p), "could not remove the proxy host")
		})
		return nil, err
	})

	type proxyVerifyOut struct {
		Body struct {
			Verified  bool         `json:"verified"`
			Message   string       `json:"message"`
			ProxyHost ProxyHostOut `json:"proxy_host"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "verify-proxy-host", Method: http.MethodPost, Path: "/api/teams/{team}/proxy-hosts/{proxy}/verify",
		Tags: tags, Summary: "Check the _cloudy-verify TXT record (owner or admin). Verification gates public certificates.",
	}, func(ctx context.Context, in *ProxyHostPath) (*proxyVerifyOut, error) {
		out := &proxyVerifyOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			p, err := deploy.ProxyHostByRef(ctx, q, m.TeamID, in.Proxy)
			if err != nil {
				return errNotFound
			}
			ok, msg, err := d.deploys().VerifyProxyHost(ctx, q, m.TeamID, webActor(id), p)
			if err != nil {
				return actionErr(err, "could not verify the proxy host")
			}
			fresh, _ := deploy.ProxyHostByRef(ctx, q, m.TeamID, p.ID)
			out.Body.Verified, out.Body.Message, out.Body.ProxyHost = ok, msg, toProxyHostOut(fresh, "")
			return nil
		})
		return out, err
	})
}
