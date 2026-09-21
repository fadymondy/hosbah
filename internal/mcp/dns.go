package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// DNSTools: the "DNS & SSL" section of an app — the Cloudflare record of each of its hostnames and
// the certificate its server holds for each.
func DNSTools() []Tool {
	appArg := str("App name or id")
	return []Tool{
		{
			Name: "get_app_dns", Scope: scopes.Read,
			Description: "The Cloudflare DNS record of each of an app's hostnames, set against the app's server's public address. Status: ok, " +
				"missing, mismatch, unmanaged (no Cloudflare credential of the team holds the zone) or private (the server has no public address).",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				res, err := s.DNSOfApp(ctx, env.Q, env.Caller.TeamID, a)
				if err != nil {
					return "", err
				}
				if len(res.Rows) == 0 {
					return a.Name + " has no hostnames. add_domain routes one.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "server: %s  %s\n", res.Server.Name, orDash(res.Server.IpAddress))
				for _, r := range res.Rows {
					b.WriteString(dnsLine(r))
				}
				return b.String(), nil
			},
		},
		{
			Name: "set_app_dns_record", Scope: scopes.Write,
			Description: "Point one of an app's hostnames at its server in the team's Cloudflare: creates the record when missing, repairs it " +
				"when it points elsewhere, applies proxied. A record that points elsewhere and was not created by Hosbah is only replaced " +
				"with takeover=true. Owner or admin.",
			InputSchema: schema([]string{"app", "hostname"}, map[string]any{"app": appArg, "hostname": str("One of the app's own hostnames"),
				"proxied":  map[string]any{"type": "boolean", "description": "Put the record behind Cloudflare's proxy (omitted: a new record is DNS-only, an existing one keeps its setting)"},
				"takeover": map[string]any{"type": "boolean", "description": "Replace a record Hosbah did not create"}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				var proxied *bool
				if v, ok := args["proxied"].(bool); ok {
					proxied = &v
				}
				row, err := s.SetDNSRecord(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, args.Str("hostname"), proxied, args.Bool("takeover"))
				if err != nil {
					return "", err
				}
				return dnsLine(row), nil
			},
		},
		{
			Name: "get_app_certificates", Scope: scopes.Read,
			Description: "The TLS certificate an app's server holds for each of the app's hostnames: issuer, validity, days left, and whether " +
				"Caddy obtained it from a public CA. Read through the server's agent (it must be online).",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				op, hosts, err := s.AppCertificates(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a)
				if err != nil {
					return "", err
				}
				if op.ID == "" {
					return a.Name + " has no hostnames. add_domain routes one.", nil
				}
				env.Then = func(ctx context.Context) (string, error) {
					var res agentproto.CaddyCertsResult
					if _, err := s.AwaitResult(ctx, op, 20*time.Second, &res); err != nil {
						if errors.Is(err, deploy.ErrStillRunning) {
							return fmt.Sprintf("The server has not answered yet — is its agent connected? (operation %s)", op.ID), nil
						}
						return "", err
					}
					var b strings.Builder
					now := time.Now()
					for _, c := range deploy.CleanCerts(hosts, res.Certificates) {
						switch {
						case !c.Found && c.Error != "":
							fmt.Fprintf(&b, "  %-40s no certificate (%s)\n", c.Hostname, c.Error)
						case !c.Found || c.NotAfter == nil:
							fmt.Fprintf(&b, "  %-40s no certificate\n", c.Hostname)
						default:
							kind := "not Caddy-managed / internal CA"
							if c.Managed {
								kind = "Caddy-managed"
							}
							fmt.Fprintf(&b, "  %-40s %s  until %s (%d days)  %s\n", c.Hostname, c.Issuer, c.NotAfter.UTC().Format("2006-01-02"),
								int(math.Floor(c.NotAfter.Sub(now).Hours()/24)), kind)
						}
					}
					return b.String(), nil
				}
				return "", nil
			},
		},
	}
}

func dnsLine(r deploy.DNSRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %-40s %-9s", r.Hostname, r.Status)
	if r.Zone != "" {
		fmt.Fprintf(&b, " zone %s (%s)", r.Zone, r.CredentialName)
	}
	if rec := r.Record; rec != nil {
		owner := "not Hosbah's"
		if r.RecordManaged {
			owner = "managed by Hosbah"
		}
		proxied := "DNS only"
		if rec.Proxied != nil && *rec.Proxied {
			proxied = "proxied"
		}
		fmt.Fprintf(&b, "  %s %s  %s, %s", rec.Type, rec.Content, proxied, owner)
	}
	if r.ExpectedContent != "" && r.Status != deploy.DNSOK {
		fmt.Fprintf(&b, "  expected %s %s", r.ExpectedType, r.ExpectedContent)
	}
	if r.Err != "" {
		fmt.Fprintf(&b, "  error: %s", r.Err)
	}
	b.WriteString("\n")
	return b.String()
}
