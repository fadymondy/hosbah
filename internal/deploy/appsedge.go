package deploy

import (
	"context"
	"database/sql"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// The apps-edge reconcile: idempotent, so it is safe to run at every API startup (fixing a platform
// domain created before HOSBAH_APPS_EDGE existed, or before it was set) and on demand from the
// apps_edge_sync admin MCP tool. See the HOSBAH_APPS_EDGE doc comment in appsdomain.go.

// AppsEdgeSyncResult summarizes one run of SyncAppsEdge.
type AppsEdgeSyncResult struct {
	EdgeConfigured bool `json:"edge_configured"`
	Domains        int  `json:"domains"`   // platform domains looked at
	TLSFixed       int  `json:"tls_fixed"` // domains whose tls was not "off" and got flipped
	Routed         int  `json:"routed"`    // proxy.set (re-)applied on the edge
	Skipped        int  `json:"skipped"`   // on the edge server itself, or no server address yet
	Errors         int  `json:"errors"`
}

// SyncAppsEdge reconciles every platform hostname's tls mode and its route on the apps-edge server
// with the current HOSBAH_APPS_EDGE configuration. A no-op (EdgeConfigured: false) when there is no
// apps edge. Never fails the caller: every domain is best effort, logged, and counted in the result.
func (s *Service) SyncAppsEdge(ctx context.Context) AppsEdgeSyncResult {
	res := AppsEdgeSyncResult{EdgeConfigured: s.apps.EdgeConfigured()}
	if !res.EdgeConfigured {
		return res
	}
	var domains []db.Domain
	if err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		domains, err = q.ListPlatformDomains(ctx)
		return err
	}); err != nil {
		s.log.Warn("apps edge: could not list platform domains", "err", err)
		res.Errors++
		return res
	}
	res.Domains = len(domains)
	for _, d := range domains {
		fixed, routed, skipped, err := s.syncAppsEdgeDomain(ctx, d)
		switch {
		case err != nil:
			s.log.Warn("apps edge: could not sync a platform domain", "host", d.Hostname, "err", err)
			res.Errors++
		case skipped:
			res.Skipped++
		default:
			if fixed {
				res.TLSFixed++
			}
			if routed {
				res.Routed++
			}
		}
	}
	return res
}

// syncAppsEdgeDomain reconciles one platform domain: its tls mode (flipped to "off" if it is not
// already, and its app's Caddy route re-applied so the change takes effect without a full redeploy —
// see SyncRoutes), and its route on the edge (always re-applied: enqueueEdgeProxySet is idempotent).
func (s *Service) syncAppsEdgeDomain(ctx context.Context, d db.Domain) (fixed, routed, skipped bool, err error) {
	var (
		a   db.App
		srv db.Server
	)
	txErr := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: d.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		if a, err = q.AppInTeam(ctx, db.AppInTeamParams{ID: d.AppID, TeamID: d.TeamID}); err != nil {
			return err
		}
		if srv, err = q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: d.TeamID}); err != nil {
			return err
		}
		if d.Tls != "off" {
			if err := q.SetDomainTLS(ctx, db.SetDomainTLSParams{Tls: "off", ID: d.ID, TeamID: d.TeamID}); err != nil {
				return err
			}
			fixed = true
		}
		return nil
	})
	if txErr != nil {
		return false, false, false, txErr
	}
	if fixed {
		if err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: d.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
			return s.SyncRoutes(ctx, q, d.TeamID, a)
		}); err != nil {
			s.log.Warn("apps edge: tls fixed but could not re-apply the app's routes", "host", d.Hostname, "err", err)
		}
	}
	if srv.ID == s.apps.EdgeServerID || edgeUpstream(srv) == "" {
		return fixed, false, true, nil
	}
	s.enqueueEdgeProxySet(ctx, d.TeamID, d.Hostname, d.ID, srv, false)
	return fixed, true, false, nil
}
