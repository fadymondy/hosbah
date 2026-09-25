package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/certcheck"
	"github.com/fadymondy/hosbah/internal/scopes"
)

// CertCheckTools are MH-580 J6: the team's TLS certificate watch. Both tools are read/act-only
// (nothing is ever deleted), so ssl_check_now needs no confirm.
func CertCheckTools() []Tool {
	return []Tool{
		{
			Name: "ssl_certificates", Scope: scopes.Read,
			Description: "The team's TLS certificates (every app domain and proxy host), soonest-to-expire and failing first.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				rows, err := env.Q.ListTeamCertificates(ctx, env.Caller.TeamID)
				if err != nil {
					return "", err
				}
				if len(rows) == 0 {
					return "No certificates tracked yet.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d certificate(s):\n", len(rows))
				for _, r := range rows {
					status := "ok"
					if !r.Ok {
						status = "FAIL: " + r.Error
					}
					days := "?"
					if r.NotAfter != nil {
						days = fmt.Sprintf("%d", int(time.Until(*r.NotAfter).Hours()/24))
					}
					fmt.Fprintf(&b, "  %-40s %-6s %s days left  issuer=%s  (%s)\n", r.Hostname, r.Source, days, r.Issuer, status)
				}
				return b.String(), nil
			},
		},
		{
			Name: "ssl_check_now", Scope: scopes.Read,
			Description: "TLS-dial every one of the team's hostnames right now instead of waiting for the next sweep, then report the results.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				cc, _ := env.App.CertCheck.(*certcheck.Service)
				if cc == nil {
					return "", fmt.Errorf("the certificate watch is not configured")
				}
				n, err := cc.CheckNow(ctx, env.Caller.TeamID)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Checked %d hostname(s). Use ssl_certificates for the results.", n), nil
			},
		},
	}
}
