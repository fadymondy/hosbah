package dbsvc

import (
	"context"
	"net"
	"net/url"
	"strconv"

	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
)

// Link says that one of an app's connection variables points at a service of its team: either a
// data service on its own server (ServiceKind "data", found here by comparing host:port for the
// three variables Attach writes) or any shared service — including ones on other servers, and
// variables beyond DATABASE_URL/REDIS_URL/S3_URL — found by internal/services.Detect and persisted
// in app_service_links (ServiceKind "shared" or "data" with Source "auto"/"manual"). Both are
// merged here, keyed by variable, so the canvas draws one edge per variable at most.
type Link struct {
	ServiceID   string `json:"service_id"`
	Variable    string `json:"variable" doc:"The connection variable that points at the service, e.g. DATABASE_URL, REDIS_URL, or an app-specific name."`
	ServiceKind string `json:"service_kind,omitempty" enum:"data,shared" doc:"data: a data service on the app's own server. shared: a Proxmox guest classified as shared infra (internal/services)."`
}

// linkVars are the variables Attach writes, one per engine family (connString).
var linkVars = []string{"DATABASE_URL", "REDIS_URL", "S3_URL"}

/*
Links finds which data services on an app's server the app is connected to: for each connection
variable Attach sets that the app has, its sealed value is opened here and only its host:port is
compared with the services'. Nothing of the value leaves this function; the answer is a service id
and a variable name, both of which any member already sees. A variable set by hand to somewhere
else, or to a service that is gone, links nothing. Team scope; the caller has checked the app.
*/
func (s *Service) Links(ctx context.Context, q *db.Queries, teamID string, a db.App) ([]Link, error) {
	out := []Link{}
	if deploy.IsImported(a) {
		return out, nil
	}
	svcs, err := q.ListDataServicesOfServer(ctx, db.ListDataServicesOfServerParams{ServerID: a.ServerID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	byAddr := map[string]string{}
	for _, svc := range svcs {
		if svc.Host != nil && svc.Port != nil && *svc.Host != "" {
			byAddr[net.JoinHostPort(*svc.Host, strconv.FormatInt(*svc.Port, 10))] = svc.ID
		}
	}
	if len(byAddr) == 0 {
		return out, nil
	}
	var dek []byte
	for _, name := range linkVars {
		v, err := q.EnvVarInApp(ctx, db.EnvVarInAppParams{AppID: a.ID, TeamID: teamID, Name: name})
		if err != nil {
			continue // not set
		}
		if dek == nil {
			if dek, err = s.ops.TeamDEK(ctx, q, teamID); err != nil {
				return nil, err
			}
		}
		pt, err := crypto.Open(dek, teamID, deploy.EnvSealContext(a.ID, name), v.ValueSealed)
		if err != nil {
			continue
		}
		u, err := url.Parse(string(pt))
		if err != nil || u.Host == "" {
			continue
		}
		if id, ok := byAddr[u.Host]; ok {
			out = append(out, Link{ServiceID: id, Variable: name, ServiceKind: "data"})
		}
	}

	// Merge in what internal/services.Detect found and persisted (any variable name, any server —
	// notably shared services, which the host:port scan above cannot see since it only looks at
	// data services of the app's own server).
	seen := map[string]bool{}
	for _, l := range out {
		seen[l.Variable] = true
	}
	links, err := q.ListAppServiceLinks(ctx, db.ListAppServiceLinksParams{AppID: a.ID, TeamID: teamID})
	if err == nil {
		for _, l := range links {
			if seen[l.Variable] {
				continue
			}
			out = append(out, Link{ServiceID: l.ServiceID, Variable: l.Variable, ServiceKind: l.ServiceKind})
			seen[l.Variable] = true
		}
	}
	return out, nil
}
