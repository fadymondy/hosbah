package api

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/certcheck"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Team TLS certificates (MH-580 J6): the last reading of every app domain / proxy host whose tls is
// 'auto', kept fresh by internal/certcheck's sweep. Any member reads; "check now" is any member too
// (it only dials public hostnames the team already owns, nothing destructive) and re-runs the same
// sweep for this team alone.

// SSLCertificateOut is one hostname's last TLS reading.
type SSLCertificateOut struct {
	ID            string     `json:"id"`
	Hostname      string     `json:"hostname"`
	Source        string     `json:"source"` // "app" | "proxy"
	OK            bool       `json:"ok"`
	Issuer        string     `json:"issuer,omitempty"`
	NotBefore     *time.Time `json:"not_before,omitempty"`
	NotAfter      *time.Time `json:"not_after,omitempty"`
	DaysLeft      *int       `json:"days_left,omitempty"`
	SANMatch      bool       `json:"san_match"`
	Error         string     `json:"error,omitempty"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
}

func toSSLCertificateOut(row db.SslCertificate) SSLCertificateOut {
	out := SSLCertificateOut{
		ID: row.ID, Hostname: row.Hostname, Source: row.Source, OK: row.Ok, Issuer: row.Issuer,
		NotBefore: row.NotBefore, NotAfter: row.NotAfter, SANMatch: row.SanMatch, Error: row.Error,
		LastCheckedAt: row.LastCheckedAt,
	}
	if row.NotAfter != nil {
		d := int(time.Until(*row.NotAfter).Hours() / 24)
		out.DaysLeft = &d
	}
	return out
}

func (d *deps) certCheck() *certcheck.Service {
	if d.a == nil {
		return nil
	}
	s, _ := d.a.CertCheck.(*certcheck.Service)
	return s
}

func (d *deps) registerSSLCertificates(api huma.API) {
	tags := []string{"Certificates"}
	base := "/api/teams/{team}/certificates"

	type sslListOut struct {
		Body struct {
			Certificates []SSLCertificateOut `json:"certificates"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-certificates", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The team's TLS certificates (every app domain and proxy host), soonest-to-expire and failing first. Any member.",
	}, func(ctx context.Context, in *TeamPath) (*sslListOut, error) {
		out := &sslListOut{}
		out.Body.Certificates = []SSLCertificateOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListTeamCertificates(ctx, m.TeamID)
			if err != nil {
				return actionErr(err, "could not list the team's certificates")
			}
			for _, r := range rows {
				out.Body.Certificates = append(out.Body.Certificates, toSSLCertificateOut(r))
			}
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "check-certificates-now", Method: http.MethodPost, Path: base + "/check", Tags: tags,
		Summary: "TLS-dial every one of the team's hostnames right now instead of waiting for the next sweep. Any member.",
	}, func(ctx context.Context, in *TeamPath) (*sslListOut, error) {
		out := &sslListOut{}
		out.Body.Certificates = []SSLCertificateOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			cc := d.certCheck()
			if cc == nil {
				return huma.Error503ServiceUnavailable("the certificate watch is not configured")
			}
			if _, err := cc.CheckNow(ctx, m.TeamID); err != nil {
				return actionErr(err, "could not check the team's certificates")
			}
			rows, err := q.ListTeamCertificates(ctx, m.TeamID)
			if err != nil {
				return actionErr(err, "could not list the team's certificates")
			}
			for _, r := range rows {
				out.Body.Certificates = append(out.Body.Certificates, toSSLCertificateOut(r))
			}
			return nil
		})
		return out, err
	})
}
