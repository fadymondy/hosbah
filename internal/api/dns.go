package api

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The "DNS & SSL" section of a site: the Cloudflare record of each of the app's hostnames (read: any
member; write: whoever may manage the app's domains) and the certificate the app's server holds for
each (read through the agent's caddy.certs). {hostname} is always one of the app's own domains.
*/

// DNSHostnamePath addresses the DNS record of one of the app's hostnames.
type DNSHostnamePath struct {
	AppPath
	Hostname string `path:"hostname" maxLength:"253" doc:"One of the app's own hostnames."`
}

// DNSServerOut is the server the app's hostnames should point at.
type DNSServerOut struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Address *string `json:"address" doc:"The server's address as Hosbah knows it; null when unknown."`
}

// DNSCredentialOut names the team's Cloudflare credential that holds the zone.
type DNSCredentialOut struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DNSRecordOut is the record Cloudflare holds for a hostname.
type DNSRecordOut struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	Content         string `json:"content"`
	Proxied         bool   `json:"proxied"`
	TTL             int    `json:"ttl" doc:"1 = automatic."`
	ManagedByHosbah bool   `json:"managed_by_hosbah" doc:"Hosbah created it; only such records are deleted, or rewritten without takeover."`
}

// DNSExpectedOut is what the hostname should point at.
type DNSExpectedOut struct {
	Type    string `json:"type" doc:"A or AAAA; empty when the server has no public address."`
	Content string `json:"content"`
}

// DNSRowOut is one hostname of the app.
type DNSRowOut struct {
	Hostname   string            `json:"hostname"`
	Zone       *string           `json:"zone"`
	Credential *DNSCredentialOut `json:"credential"`
	Managed    bool              `json:"managed" doc:"One of the team's Cloudflare credentials holds the hostname's zone."`
	Record     *DNSRecordOut     `json:"record"`
	Expected   DNSExpectedOut    `json:"expected"`
	Status     string            `json:"status" enum:"ok,missing,mismatch,unmanaged,private"`
	Error      *string           `json:"error" doc:"Why Cloudflare could not be read for this hostname."`
}

func toDNSRowOut(r deploy.DNSRow) DNSRowOut {
	out := DNSRowOut{Hostname: r.Hostname, Zone: optional(r.Zone), Managed: r.Zone != "", Status: r.Status, Error: optional(r.Err),
		Expected: DNSExpectedOut{Type: r.ExpectedType, Content: r.ExpectedContent}}
	if r.CredentialID != "" {
		out.Credential = &DNSCredentialOut{ID: r.CredentialID, Name: r.CredentialName}
	}
	if rec := r.Record; rec != nil {
		out.Record = &DNSRecordOut{ID: rec.ID, Type: rec.Type, Content: rec.Content, Proxied: rec.Proxied != nil && *rec.Proxied,
			TTL: rec.TTL, ManagedByHosbah: r.RecordManaged}
	}
	return out
}

// CertificateOut is the certificate the app's server holds for one hostname.
type CertificateOut struct {
	Hostname  string     `json:"hostname"`
	Found     bool       `json:"found"`
	Issuer    *string    `json:"issuer"`
	NotBefore *time.Time `json:"not_before"`
	NotAfter  *time.Time `json:"not_after"`
	DaysLeft  *int       `json:"days_left" doc:"Whole days until not_after (negative: expired); null when not found."`
	Managed   bool       `json:"managed" doc:"Caddy obtained it from a public CA (false: Caddy's internal CA, or a certificate something else presents)."`
	Error     *string    `json:"error"`
}

func toCertificatesOut(hosts []string, in []agentproto.CaddyCert, now time.Time) []CertificateOut {
	out := []CertificateOut{}
	for _, c := range deploy.CleanCerts(hosts, in) {
		o := CertificateOut{Hostname: c.Hostname, Found: c.Found, Issuer: optional(c.Issuer), NotBefore: c.NotBefore, NotAfter: c.NotAfter,
			Managed: c.Managed, Error: optional(c.Error)}
		if c.Found && c.NotAfter != nil {
			days := int(math.Floor(c.NotAfter.Sub(now).Hours() / 24))
			o.DaysLeft = &days
		}
		out = append(out, o)
	}
	return out
}

// dnsErr maps the DNS actions' errors: a Cloudflare failure is a 502.
func dnsErr(err error, fallback string) error {
	var ae *deploy.ActionError
	if errors.As(err, &ae) && ae.Status == http.StatusBadGateway {
		return huma.Error502BadGateway(ae.Msg)
	}
	return actionErr(err, fallback)
}

func (d *deps) registerDNS(api huma.API) {
	tags := []string{"DNS & SSL"}
	base := "/api/teams/{team}/apps/{app}"

	type dnsOut struct {
		Body struct {
			Server  DNSServerOut `json:"server"`
			Records []DNSRowOut  `json:"records" doc:"One per hostname of the app."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "get-app-dns", Method: http.MethodGet, Path: base + "/dns", Tags: tags,
		Summary: "The Cloudflare DNS record of each of the app's hostnames, set against the app's server's public address. status: ok, " +
			"missing, mismatch, unmanaged (no Cloudflare credential of the team holds the zone) or private (the server has no public " +
			"address). A Cloudflare failure is that row's error, never the whole list's. Any member.",
	}, func(ctx context.Context, in *AppPath) (*dnsOut, error) {
		out := &dnsOut{}
		out.Body.Records = []DNSRowOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			res, err := d.deploys().DNSOfApp(ctx, q, m.TeamID, a)
			if err != nil {
				return actionErr(err, "could not read the app's DNS")
			}
			out.Body.Server = DNSServerOut{ID: res.Server.ID, Name: res.Server.Name, Address: res.Server.IpAddress}
			for _, r := range res.Rows {
				out.Body.Records = append(out.Body.Records, toDNSRowOut(r))
			}
			return nil
		})
		return out, err
	})

	type dnsRowOut struct{ Body DNSRowOut }
	huma.Register(api, huma.Operation{OperationID: "set-app-dns-record", Method: http.MethodPut, Path: base + "/dns/{hostname}", Tags: tags,
		Summary: "Point one of the app's hostnames at its server in the team's Cloudflare: creates the record when missing, repairs it when " +
			"it points elsewhere, applies proxied. 409: a record exists, points elsewhere and was not created by Hosbah (pass takeover), " +
			"or the server has only a private address. 404: not one of the app's hostnames. 422: no Cloudflare credential of the team " +
			"holds the zone. 502: Cloudflare refused. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		DNSHostnamePath
		Body struct {
			Proxied  *bool `json:"proxied,omitempty" doc:"Put the record behind Cloudflare's proxy. Omitted: a new record is DNS-only, an existing one keeps its setting."`
			Takeover bool  `json:"takeover,omitempty" doc:"Replace a record that Hosbah did not create."`
		}
	}) (*dnsRowOut, error) {
		out := &dnsRowOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			row, err := d.deploys().SetDNSRecord(ctx, q, m.TeamID, webActor(id), a, in.Hostname, in.Body.Proxied, in.Body.Takeover)
			if err != nil {
				return dnsErr(err, "could not set the DNS record")
			}
			out.Body = toDNSRowOut(row)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "remove-app-dns-record", Method: http.MethodDelete, Path: base + "/dns/{hostname}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Remove the Cloudflare record of one of the app's hostnames — only a record Hosbah created (409 otherwise). 404: not one " +
			"of the app's hostnames, or it has no record. Owner or admin. Audited.",
	}, func(ctx context.Context, in *DNSHostnamePath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			return dnsErr(d.deploys().RemoveDNSRecord(ctx, q, m.TeamID, webActor(id), a, in.Hostname), "could not remove the DNS record")
		})
		return nil, err
	})

	type certsOut struct {
		Body struct {
			Certificates []CertificateOut `json:"certificates" doc:"One per hostname of the app."`
			Operation    *string          `json:"operation" doc:"The caddy.certs operation; null when the app has no hostnames."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-app-certificates", Method: http.MethodGet, Path: base + "/certificates", Tags: tags,
		Summary: "The TLS certificate the app's server holds for each of the app's hostnames: issuer, validity, days left, and whether " +
			"Caddy obtained it from a public CA. Any member; waits up to 20 s for the server. 409: the server is offline or not " +
			"enrolled; 504: its agent did not answer.",
	}, func(ctx context.Context, in *AppPath) (*certsOut, error) {
		out := &certsOut{}
		out.Body.Certificates = []CertificateOut{}
		var (
			op    db.Operation
			hosts []string
		)
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			op, hosts, err = d.deploys().AppCertificates(ctx, q, m.TeamID, webActor(id), a)
			return actionErr(err, "could not queue the operation")
		})
		if err != nil {
			return nil, err
		}
		if op.ID == "" {
			return out, nil
		}
		var res agentproto.CaddyCertsResult
		if _, err := d.githubAwait(ctx, op, 20*time.Second, &res); err != nil {
			return nil, err
		}
		out.Body.Certificates, out.Body.Operation = toCertificatesOut(hosts, res.Certificates, time.Now()), &op.ID
		return out, nil
	})
}
