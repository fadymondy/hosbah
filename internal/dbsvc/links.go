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

// Link says that one of an app's connection variables points at a data service of its server.
type Link struct {
	ServiceID string `json:"service_id"`
	Variable  string `json:"variable" doc:"DATABASE_URL, REDIS_URL or S3_URL — the variable Attach set."`
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
			out = append(out, Link{ServiceID: id, Variable: name})
		}
	}
	return out, nil
}
