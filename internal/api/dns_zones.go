package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The DNS zone manager (MH-578 E8): unlike dns.go, which only ever sets the one A/AAAA/CNAME record
an app's own hostname needs, this browses a whole zone of the team's Cloudflare and edits any
record in it — MX, TXT, SRV, CAA included. Reading is any member; writing (create/update/delete
a record) needs the same ability as managing a proxy host or a Cloudflare credential
(AbilityManageServers) since it can just as easily point a hostname somewhere else, or remove a
record the whole team depends on.
*/

// DNSZoneOut is one zone the team's Cloudflare credentials can see.
type DNSZoneOut struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	CredentialID   string `json:"credential_id"`
	CredentialName string `json:"credential_name"`
}

func toDNSZoneOut(z deploy.DNSZone) DNSZoneOut {
	return DNSZoneOut{ID: z.ID, Name: z.Name, Status: z.Status, CredentialID: z.CredentialID, CredentialName: z.CredentialName}
}

// DNSZoneRecordOut is one record of a zone.
type DNSZoneRecordOut struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	Name            string `json:"name"`
	Content         string `json:"content"`
	TTL             int    `json:"ttl" doc:"1 = automatic."`
	Priority        int    `json:"priority,omitempty" doc:"MX (and SRV) only."`
	Proxied         bool   `json:"proxied"`
	ManagedByHosbah bool   `json:"managed_by_hosbah" doc:"Hosbah created this record through the zone manager."`
	ManagedKind     string `json:"managed_kind,omitempty" enum:",app_domain,proxy_host,mail" doc:"Hosbah already manages this hostname elsewhere — warn before editing or deleting it."`
}

func toDNSZoneRecordOut(r deploy.DNSZoneRecord) DNSZoneRecordOut {
	return DNSZoneRecordOut{ID: r.ID, Type: r.Type, Name: r.Name, Content: r.Content, TTL: r.TTL, Priority: r.Priority,
		Proxied: r.Proxied != nil && *r.Proxied, ManagedByHosbah: r.ManagedByHosbah, ManagedKind: r.ManagedKind}
}

// DNSZonePath addresses one zone of one Cloudflare credential of the team.
type DNSZonePath struct {
	TeamPath
	Credential string `path:"credential" maxLength:"64" doc:"Cloudflare credential id or name."`
	Zone       string `path:"zone" maxLength:"253" doc:"Zone id or name."`
}

// DNSRecordPath addresses one record of a zone.
type DNSRecordPath struct {
	DNSZonePath
	Record string `path:"record" maxLength:"64" doc:"Record id."`
}

// DNSZoneRecordIn is what a caller sets when creating or changing a record.
type DNSZoneRecordIn struct {
	Type     string `json:"type,omitempty" enum:",A,AAAA,CNAME,TXT,MX,SRV,CAA"`
	Name     string `json:"name,omitempty" maxLength:"253" doc:"The hostname, e.g. www or www.example.com."`
	Content  string `json:"content,omitempty" maxLength:"2000"`
	TTL      int    `json:"ttl,omitempty" doc:"Seconds; 1 or omitted = automatic."`
	Priority int    `json:"priority,omitempty" doc:"MX (and SRV) only."`
	Proxied  *bool  `json:"proxied,omitempty"`
}

func (d *deps) registerDNSZones(api huma.API) {
	tags := []string{"DNS zone manager"}
	base := "/api/teams/{team}/dns/zones"

	type zonesOut struct {
		Body struct {
			Zones []DNSZoneOut `json:"zones"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-dns-zones", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "Every DNS zone the team's Cloudflare credentials can see. Any member.",
	}, func(ctx context.Context, in *TeamPath) (*zonesOut, error) {
		out := &zonesOut{}
		out.Body.Zones = []DNSZoneOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			zones, err := d.deploys().ListDNSZones(ctx, q, m.TeamID)
			if err != nil {
				return actionErr(err, "could not list DNS zones")
			}
			for _, z := range zones {
				out.Body.Zones = append(out.Body.Zones, toDNSZoneOut(z))
			}
			return nil
		})
		return out, err
	})

	type recordsOut struct {
		Body struct {
			Zone    DNSZoneOut         `json:"zone"`
			Records []DNSZoneRecordOut `json:"records"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-dns-zone-records", Method: http.MethodGet, Path: base + "/{credential}/{zone}/records",
		Tags: tags, Summary: "Every record of a zone, flagged where Hosbah already manages the hostname (app domain, proxy host, or mail " +
			"DNS) so editing or deleting it can be warned about. Any member.",
	}, func(ctx context.Context, in *DNSZonePath) (*recordsOut, error) {
		out := &recordsOut{}
		out.Body.Records = []DNSZoneRecordOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			z, recs, err := d.deploys().ListDNSZoneRecords(ctx, q, m.TeamID, in.Credential, in.Zone)
			if err != nil {
				return dnsErr(err, "could not list the zone's records")
			}
			out.Body.Zone = toDNSZoneOut(z)
			for _, r := range recs {
				out.Body.Records = append(out.Body.Records, toDNSZoneRecordOut(r))
			}
			return nil
		})
		return out, err
	})

	type recordOut struct {
		Body struct {
			Zone   DNSZoneOut       `json:"zone"`
			Record DNSZoneRecordOut `json:"record"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "create-dns-zone-record", Method: http.MethodPost, Path: base + "/{credential}/{zone}/records",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Add a record to a zone: A, AAAA, CNAME, TXT, MX (priority required), SRV or CAA. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		DNSZonePath
		Body DNSZoneRecordIn
	}) (*recordOut, error) {
		out := &recordOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			b := in.Body
			z, rec, err := d.deploys().CreateDNSZoneRecord(ctx, q, m.TeamID, webActor(id), in.Credential, in.Zone, deploy.DNSZoneRecordInput{
				Type: b.Type, Name: b.Name, Content: b.Content, TTL: b.TTL, Priority: b.Priority, Proxied: b.Proxied})
			if err != nil {
				return dnsErr(err, "could not create the record")
			}
			out.Body.Zone, out.Body.Record = toDNSZoneOut(z), toDNSZoneRecordOut(rec)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-dns-zone-record", Method: http.MethodPatch,
		Path: base + "/{credential}/{zone}/records/{record}", Tags: tags,
		Summary: "Change a record's content, TTL, priority or proxied flag. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		DNSRecordPath
		Body DNSZoneRecordIn
	}) (*recordOut, error) {
		out := &recordOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			b := in.Body
			z, rec, err := d.deploys().UpdateDNSZoneRecord(ctx, q, m.TeamID, webActor(id), in.Credential, in.Zone, in.Record, deploy.DNSZoneRecordInput{
				Content: b.Content, TTL: b.TTL, Priority: b.Priority, Proxied: b.Proxied})
			if err != nil {
				return dnsErr(err, "could not change the record")
			}
			out.Body.Zone, out.Body.Record = toDNSZoneOut(z), toDNSZoneRecordOut(rec)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-dns-zone-record", Method: http.MethodDelete,
		Path: base + "/{credential}/{zone}/records/{record}", DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Remove a record of a zone. If it is one Hosbah manages elsewhere (an app domain, proxy host or mail DNS record) it is " +
			"still removed — confirm on the client first. Owner or admin. Audited.",
	}, func(ctx context.Context, in *DNSRecordPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			return dnsErr(d.deploys().DeleteDNSZoneRecord(ctx, q, m.TeamID, webActor(id), in.Credential, in.Zone, in.Record), "could not remove the record")
		})
		return nil, err
	})
}
