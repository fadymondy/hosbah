package dbsvc

import (
	"context"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
DB metrics and slow queries (MH-575 F6): db.insights runs fixed, agent-owned SQL only — nothing a
member types ever reaches the engine, unlike db.query's SQL console. Postgres and MySQL only, the
same preflight as the table browser (usable() plus a kind check; an imported service in a Proxmox
guest, or one with no credentials yet, answers the same way it does there). Reading insights needs
AbilityRevealSecrets — the same tier as a user's password and the table browser: long-running query
text and top statements can hold literal values a member typed, so it is treated as reveal-grade.
Cancelling a query is a step further: it needs the caller to already be owner/admin (same ability)
and is audited, with an explicit confirm required at the REST/MCP layer (this package just runs it).
*/

// Insights runs db.insights against a running Postgres/MySQL service, synchronously.
func (s *Service) Insights(ctx context.Context, q *db.Queries, teamID string, svc db.DataService) (agentproto.DBInsightsResult, error) {
	if err := preflightQuery(svc); err != nil {
		return agentproto.DBInsightsResult{}, err
	}
	var result agentproto.DBInsightsResult
	err := s.withServer(ctx, teamID, svc.ServerID, func(agentKey string, dek []byte) (any, string, error) {
		rootBox, err := s.sealRoot(agentKey, dek, teamID, svc)
		if err != nil {
			return nil, "", err
		}
		p := agentproto.DBInsightsParams{
			ServiceID: svc.ID, Kind: svc.Kind, Container: contOf(svc), TimeoutSeconds: 15, RootSecret: rootBox,
		}
		if err := p.Validate(); err != nil {
			return nil, "", invalid(err.Error())
		}
		return p, opID(svc.ID, "insights:"+newUUID()), nil
	}, agentproto.KindDBInsights, &result)
	if err != nil {
		return agentproto.DBInsightsResult{}, err
	}
	return result, nil
}

// CancelQuery cancels one running backend/query (pg_cancel_backend / KILL QUERY). Audited; the
// caller (REST/MCP) has already checked an explicit confirm equal to the service's name.
func (s *Service) CancelQuery(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, id int64) (agentproto.DBCancelQueryResult, error) {
	if err := preflightQuery(svc); err != nil {
		return agentproto.DBCancelQueryResult{}, err
	}
	if id <= 0 {
		return agentproto.DBCancelQueryResult{}, invalid("a positive process/pid id is required")
	}
	var result agentproto.DBCancelQueryResult
	err := s.withServer(ctx, teamID, svc.ServerID, func(agentKey string, dek []byte) (any, string, error) {
		rootBox, err := s.sealRoot(agentKey, dek, teamID, svc)
		if err != nil {
			return nil, "", err
		}
		p := agentproto.DBCancelQueryParams{
			ServiceID: svc.ID, Kind: svc.Kind, Container: contOf(svc), ID: id, RootSecret: rootBox,
		}
		if err := p.Validate(); err != nil {
			return nil, "", invalid(err.Error())
		}
		return p, opID(svc.ID, "cancel:"+newUUID()), nil
	}, agentproto.KindDBCancelQuery, &result)
	if err != nil {
		return agentproto.DBCancelQueryResult{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "database_query.cancel", "data_service", svc.ID,
		auditExtra(actor, map[string]any{"id": id, "ok": result.OK})); err != nil {
		return result, err
	}
	return result, nil
}
