package circlexo

import (
	"context"
	"database/sql"
	"errors"

	"github.com/circlexo/circlexo-go/entitlements"

	"github.com/fadymondy/hosbah/internal/quota"
)

// Hub feature keys, as declared in the manifest: counted limits.
const (
	featServers      = "servers"
	featApps         = "apps"
	featDataServices = "data_services"
	featMembers      = "members"
)

// overlay is the quota.Overlay: for a team with an ACTIVE org link the hub's plan decides the
// server, app, data service and member limits (through the SDK's entitlements cache, which is what
// gives the fail-closed grace behaviour). A team with no link, or an inactive one, is not touched:
// it keeps the limits a platform admin set.
//
// Fail closed: when the hub cannot confirm the org's entitlements the limits are 0 and the team is
// read-only (nothing can be added, everything keeps working). In the SDK's grace window (a stale
// copy while the hub is down) the last known limits apply but the team is read-only too. An org the
// hub says has no plan gets limits of 0 (a plain "plan allows at most 0").
func (s *Service) overlay(ctx context.Context, teamID string, q *quota.Quota) error {
	link, err := s.OrgByTeam(ctx, teamID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !link.Active) {
		return nil
	}
	if err != nil {
		return err
	}
	q.Hub = true
	set, err := s.Ents.Get(ctx, link.OrgID, 0)
	if err != nil {
		set = nil // unreachable with no usable copy, or the hub says the org does not have the app
		q.ReadOnly = true
	} else if set == nil || set.ReadOnly {
		q.ReadOnly = true
	}
	q.MaxServers = hubLimit(set, featServers)
	q.MaxApps = hubLimit(set, featApps)
	q.MaxDataServices = hubLimit(set, featDataServices)
	q.MaxMembers = hubLimit(set, featMembers)
	return nil
}

// hubLimit is the limit a hub feature stands for: off (or no entitlements at all) is 0, a feature
// with no limit is unlimited (nil), otherwise the limit. Always a hard cap; there is no overage
// billing in Hosbah.
func hubLimit(set *entitlements.Set, feature string) *int64 {
	if set == nil || !set.Has(feature) {
		zero := int64(0)
		return &zero
	}
	f, _ := set.Feature(feature)
	return f.Limit
}
