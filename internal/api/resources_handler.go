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

/*
Resource limits (MH-574 E13): memory and CPU caps carried in StartParams (apps) and DBProvisionParams
(data services), applied as docker --memory/--memory-swap/--cpus on the next start, and live-applied
to the running container(s) with app.limits / db.limits so saving here takes effect without a
redeploy. Read: any member. Save: the same ability as deploy config / data service management.
*/

// ResourceLimitsIn/Out mirrors agentproto.ResourceLimits over the wire. A zero MemoryMB/CPUs means
// "no limit set" on that dimension.
type ResourceLimitsIn struct {
	MemoryMB int     `json:"memory_mb,omitempty" minimum:"0" maximum:"1048576" doc:"MiB, 32-1048576 (1 TiB); 0: no limit"`
	CPUs     float64 `json:"cpus,omitempty" minimum:"0" maximum:"256" doc:"fractional CPUs, 0.05-256; 0: no limit"`
}

func toResourceLimitsOut(r agentproto.ResourceLimits) ResourceLimitsIn {
	return ResourceLimitsIn{MemoryMB: r.MemoryMB, CPUs: r.CPUs}
}
func fromResourceLimitsIn(r ResourceLimitsIn) agentproto.ResourceLimits {
	return agentproto.ResourceLimits{MemoryMB: r.MemoryMB, CPUs: r.CPUs}
}

// AppResourcesOut is GET/PUT …/apps/{app}/resources.
type AppResourcesOut struct {
	Resources ResourceLimitsIn            `json:"resources" doc:"The app's base limit, every process not overridden below."`
	Processes map[string]ResourceLimitsIn `json:"processes,omitempty" doc:"Per-process overrides, keyed by \"web\" or \"worker:<name>\"."`
}

func toAppResourcesOut(r deploy.AppResources) AppResourcesOut {
	out := AppResourcesOut{Resources: toResourceLimitsOut(r.Base)}
	if len(r.Processes) > 0 {
		out.Processes = map[string]ResourceLimitsIn{}
		for k, v := range r.Processes {
			out.Processes[k] = toResourceLimitsOut(v)
		}
	}
	return out
}

func (d *deps) registerResources(api huma.API) {
	tags := []string{"Apps"}

	type appOut struct{ Body AppResourcesOut }
	huma.Register(api, huma.Operation{OperationID: "get-app-resources", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}/resources", Tags: tags,
		Summary: "An app's memory and CPU limits: a base limit and, optionally, per-process overrides. Any member.",
	}, func(ctx context.Context, in *AppPath) (*appOut, error) {
		out := &appOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			out.Body = toAppResourcesOut(deploy.Resources(a))
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "put-app-resources", Method: http.MethodPut, Path: "/api/teams/{team}/apps/{app}/resources", Tags: tags,
		Summary: "Set an app's memory and CPU limits. Applies live to the running containers (no redeploy needed) and to the next " +
			"deploy or restart. Owner, admin or deployer.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			Resources ResourceLimitsIn            `json:"resources"`
			Processes map[string]ResourceLimitsIn `json:"processes,omitempty"`
		}
	}) (*appOut, error) {
		out := &appOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityDeploy, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			procs := map[string]agentproto.ResourceLimits{}
			for k, v := range in.Body.Processes {
				procs[k] = fromResourceLimitsIn(v)
			}
			updated, err := d.deploys().SetResources(ctx, q, m.TeamID, webActor(id), a, deploy.AppResources{
				Base: fromResourceLimitsIn(in.Body.Resources), Processes: procs,
			})
			if err != nil {
				return actionErr(err, "could not save resource limits")
			}
			out.Body = toAppResourcesOut(deploy.Resources(updated))
			return nil
		})
		return out, err
	})

	type svcOut struct{ Body ResourceLimitsIn }
	huma.Register(api, huma.Operation{OperationID: "get-data-service-resources", Method: http.MethodGet, Path: "/api/teams/{team}/data-services/{service}/resources",
		Tags: []string{"Databases"}, Summary: "A data service's memory and CPU limits. Any member.",
	}, func(ctx context.Context, in *ServicePath) (*svcOut, error) {
		out := &svcOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			out.Body = toResourceLimitsOut(d.dbs().Resources(svc))
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "put-data-service-resources", Method: http.MethodPut, Path: "/api/teams/{team}/data-services/{service}/resources",
		Tags: []string{"Databases"},
		Summary: "Set a Hosbah-managed data service's memory and CPU limits. Applies live to the running container (no restart needed). " +
			"Refused for an imported service. Owner or admin.",
	}, func(ctx context.Context, in *struct {
		ServicePath
		Body struct {
			Resources ResourceLimitsIn `json:"resources"`
		}
	}) (*svcOut, error) {
		out := &svcOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			updated, err := d.dbs().SetResources(ctx, q, m.TeamID, webActor(id), svc, fromResourceLimitsIn(in.Body.Resources))
			if err != nil {
				return dbErr(err, "could not save resource limits")
			}
			out.Body = toResourceLimitsOut(d.dbs().Resources(updated))
			return nil
		})
		return out, err
	})
}
