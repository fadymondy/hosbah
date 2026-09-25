package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/services"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Shared services: the directory of Proxmox guests classified as shared infrastructure (a node's
shared Postgres, Redis, NATS, MinIO, SearXNG, a browser box…), kept by Scan (called from the same
facts refresh as AutoLinkFacts, internal/ops/ws.go) and editable by hand. Read: any member. Scan and
edit (rename, recategorise, hide): admin or owner (AbilityManageServers — the same tier that may
change a server).

App-to-service links: what an app's env variables were detected pointing at (internal/services,
Detect). Read: any member of the app's team. Detect: deployer, admin or owner (AbilityDeploy — the
same tier that may see/save env).
*/

// SharedServiceOut is one shared service.
type SharedServiceOut struct {
	ID         string  `json:"id"`
	ServerID   string  `json:"server_id"`
	ServerName string  `json:"server_name"`
	GuestVMID  string  `json:"guest_vmid"`
	GuestType  string  `json:"guest_type" enum:"lxc,qemu"`
	Kind       string  `json:"kind"`
	Name       string  `json:"name"`
	Host       *string `json:"host"`
	Port       *int64  `json:"port"`
	Hidden     bool    `json:"hidden"`
	NameManual bool    `json:"name_manual"`
	KindManual bool    `json:"kind_manual"`
}

func toSharedServiceOut(s db.SharedService, serverName string) SharedServiceOut {
	return SharedServiceOut{
		ID: s.ID, ServerID: s.ServerID, ServerName: serverName, GuestVMID: s.GuestVmid, GuestType: s.GuestType,
		Kind: s.Kind, Name: s.Name, Host: s.Host, Port: s.Port, Hidden: s.Hidden,
		NameManual: s.NameManual, KindManual: s.KindManual,
	}
}

// AppServiceLinkOut is one app-to-service link.
type AppServiceLinkOut struct {
	Variable    string `json:"variable"`
	ServiceKind string `json:"service_kind" enum:"shared,data"`
	ServiceID   string `json:"service_id"`
	Source      string `json:"source" enum:"auto,manual"`
}

func (d *deps) registerSharedServices(api huma.API) {
	tags := []string{"Shared Services"}
	basePath := "/api/teams/{team}/shared-services"

	serverNames := func(ctx context.Context, q *db.Queries, teamID string, rows []db.SharedService) map[string]string {
		out := map[string]string{}
		for _, r := range rows {
			if _, ok := out[r.ServerID]; ok {
				continue
			}
			if s, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: r.ServerID, TeamID: teamID}); err == nil {
				out[r.ServerID] = s.Name
			}
		}
		return out
	}

	type sharedServicesListOut struct {
		Body struct {
			Services []SharedServiceOut `json:"services"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-shared-services", Method: http.MethodGet, Path: basePath, Tags: tags,
		Summary: "The team's shared services: Proxmox guests classified as shared infra (databases, caches, search, a browser box…). Any member.",
	}, func(ctx context.Context, in *TeamPath) (*sharedServicesListOut, error) {
		out := &sharedServicesListOut{}
		out.Body.Services = []SharedServiceOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListSharedServicesOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list shared services")
			}
			names := serverNames(ctx, q, m.TeamID, rows)
			for _, r := range rows {
				out.Body.Services = append(out.Body.Services, toSharedServiceOut(r, names[r.ServerID]))
			}
			return nil
		})
		return out, err
	})

	// SharedServicePath addresses one shared service of a team.
	type SharedServicePath struct {
		TeamPath
		Service string `path:"service" maxLength:"64"`
	}
	sharedServiceIn := func(ctx context.Context, q *db.Queries, teamID, id string) (db.SharedService, error) {
		s, err := q.SharedServiceInTeam(ctx, db.SharedServiceInTeamParams{ID: id, TeamID: teamID})
		if err != nil {
			return db.SharedService{}, errNotFound
		}
		return s, nil
	}

	type sharedServicePutOut struct{ Body SharedServiceOut }
	huma.Register(api, huma.Operation{OperationID: "update-shared-service", Method: http.MethodPut, Path: basePath + "/{service}", Tags: tags,
		Summary: "Rename, recategorise or hide a shared service; the change sticks (a scan never overwrites it). Admin or owner.",
	}, func(ctx context.Context, in *struct {
		SharedServicePath
		Body struct {
			Name   *string `json:"name,omitempty" maxLength:"64"`
			Kind   *string `json:"kind,omitempty" enum:"postgres,mysql,redis,nats,minio,searxng,firecrawl,browser,meilisearch,rabbitmq,elasticsearch,opensearch,clickhouse,mongodb,other"`
			Hidden *bool   `json:"hidden,omitempty"`
		}
	}) (*sharedServicePutOut, error) {
		out := &sharedServicePutOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			if err := need(ctx, m, tenancy.AbilityManageServers); err != nil {
				return err
			}
			if _, err := sharedServiceIn(ctx, q, m.TeamID, in.Service); err != nil {
				return err
			}
			hidden := sql.NullBool{}
			if in.Body.Hidden != nil {
				hidden = sql.NullBool{Bool: *in.Body.Hidden, Valid: true}
			}
			row, err := q.UpdateSharedService(ctx, db.UpdateSharedServiceParams{
				ID: in.Service, TeamID: m.TeamID, Name: in.Body.Name, Kind: in.Body.Kind, Hidden: hidden,
			})
			if err != nil {
				return huma.Error500InternalServerError("could not update the shared service")
			}
			names := serverNames(ctx, q, m.TeamID, []db.SharedService{row})
			out.Body = toSharedServiceOut(row, names[row.ServerID])
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-shared-service", Method: http.MethodDelete, Path: basePath + "/{service}", Tags: tags,
		Summary: "Forget a shared service (a rescan of its guest adds it back, unclassified changes lost). Admin or owner.",
	}, func(ctx context.Context, in *SharedServicePath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			if err := need(ctx, m, tenancy.AbilityManageServers); err != nil {
				return err
			}
			n, err := q.DeleteSharedServiceInTeam(ctx, db.DeleteSharedServiceInTeamParams{ID: in.Service, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not delete the shared service")
			}
			if n == 0 {
				return errNotFound
			}
			return nil
		})
		return nil, err
	})

	type sharedServicesScanOut struct {
		Body struct {
			Services []SharedServiceOut `json:"services"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "scan-shared-services", Method: http.MethodPost, Path: basePath + "/scan", Tags: tags,
		Summary: "Rescan every Proxmox node server's guests for shared services (also runs automatically on every facts refresh). Admin or owner.",
	}, func(ctx context.Context, in *TeamPath) (*sharedServicesScanOut, error) {
		out := &sharedServicesScanOut{}
		out.Body.Services = []SharedServiceOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			if err := need(ctx, m, tenancy.AbilityManageServers); err != nil {
				return err
			}
			nodes, err := q.ListServersOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list servers")
			}
			var all []db.SharedService
			for _, s := range nodes {
				if s.Facts == nil {
					continue
				}
				rows, err := services.ScanFromFacts(ctx, q, m.TeamID, s.ID, []byte(*s.Facts))
				if err != nil {
					return huma.Error500InternalServerError("could not scan " + s.Name)
				}
				all = append(all, rows...)
			}
			names := serverNames(ctx, q, m.TeamID, all)
			for _, r := range all {
				out.Body.Services = append(out.Body.Services, toSharedServiceOut(r, names[r.ServerID]))
			}
			return nil
		})
		return out, err
	})
}

func (d *deps) registerAppServiceLinks(api huma.API) {
	tags := []string{"Shared Services"}
	basePath := "/api/teams/{team}/apps/{app}/service-links"

	type appServiceLinksOut struct {
		Body struct {
			Links []AppServiceLinkOut `json:"links"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-app-service-links", Method: http.MethodGet, Path: basePath, Tags: tags,
		Summary: "What an app's env variables were detected pointing at: a shared service or a data service. Any member.",
	}, func(ctx context.Context, in *AppPath) (*appServiceLinksOut, error) {
		out := &appServiceLinksOut{}
		out.Body.Links = []AppServiceLinkOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			rows, err := q.ListAppServiceLinks(ctx, db.ListAppServiceLinksParams{AppID: a.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not list the app's service links")
			}
			for _, r := range rows {
				out.Body.Links = append(out.Body.Links, AppServiceLinkOut{Variable: r.Variable, ServiceKind: r.ServiceKind, ServiceID: r.ServiceID, Source: r.Source})
			}
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "detect-app-service-links", Method: http.MethodPost, Path: basePath + "/detect", Tags: tags,
		Summary: "Re-parse the app's env variables for connection endpoints and re-match them to shared and data services. Deployer, admin or owner.",
	}, func(ctx context.Context, in *AppPath) (*appServiceLinksOut, error) {
		out := &appServiceLinksOut{}
		out.Body.Links = []AppServiceLinkOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if err := need(ctx, m, tenancy.AbilityDeploy); err != nil {
				return err
			}
			links, err := services.Detect(ctx, q, d.ops, m.TeamID, a)
			if err != nil {
				return huma.Error500InternalServerError("could not detect service links")
			}
			for _, l := range links {
				out.Body.Links = append(out.Body.Links, AppServiceLinkOut{Variable: l.Variable, ServiceKind: l.ServiceKind, ServiceID: l.ServiceID, Source: l.Source})
			}
			return nil
		})
		return out, err
	})
}
