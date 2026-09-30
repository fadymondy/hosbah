package circlexo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	circlexo "github.com/circlexo/circlexo-go"
	"github.com/google/uuid"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Org -> team, and member sync.

A hub org maps to exactly one team (circlexo_org_links). Install (the org.app_installed webhook, or
the provision endpoint; both land in Provision) creates the team or reuses the one already linked,
adds the hub's members, and confirms the tenant to the hub. Nothing here deletes a team: removing
the app marks the link inactive (the team keeps its data and members, and stops being governed by the
hub), and removing a member removes that membership only.

Roles. The hub knows owner, admin, billing and member; a Hosbah team has owner, admin, deployer and
viewer. The hub decides the top of the ladder and the two below it map by intent:

	hub owner -> owner     hub admin -> admin
	hub member -> deployer (they run deploys)     hub billing -> viewer (read only)

For someone already in the team, a hub owner or admin sets that role; otherwise a Hosbah deployer or
viewer keeps theirs (a team lead may have narrowed a hub member), and an owner or admin the hub no
longer says is one is moved to the mapped role. The last owner of a team is never demoted or removed
by a sync.
*/

// mapRole is the Hosbah role a hub role asks for.
func mapRole(hubRole string) string {
	switch strings.ToLower(hubRole) {
	case "owner":
		return tenancy.RoleOwner
	case "admin":
		return tenancy.RoleAdmin
	case "billing":
		return tenancy.RoleViewer
	}
	return tenancy.RoleDeployer
}

// ProvisionInput is what an install carries. The org's own details and members are read from the
// hub (Client.Org); the event's copies are the fallback.
type ProvisionInput struct {
	OrgID  string
	Slug   string
	Name   string
	Locale string
}

// Provision is idempotent: any number of calls for one org leave one team, one link and the current
// members, and always end by confirming the tenant.
func (s *Service) Provision(ctx context.Context, in ProvisionInput) (*OrgLink, error) {
	if in.OrgID == "" {
		return nil, errors.New("circlexo: an org id is required")
	}
	org, err := s.Client.Org(ctx, in.OrgID)
	if err != nil {
		return nil, fmt.Errorf("circlexo: reading the org from the hub: %w", err)
	}
	in.Slug = firstNonEmpty(org.Slug, in.Slug)
	in.Name = firstNonEmpty(org.Name, in.Name)
	in.Locale = firstNonEmpty(org.DefaultLocale, in.Locale)

	link, err := s.OrgByID(ctx, in.OrgID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		owner, err := s.ownerFor(ctx, org.Members)
		if err != nil {
			return nil, err
		}
		if link, err = s.createLinked(ctx, in, owner); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	case !link.Active || (in.Slug != "" && link.OrgSlug != in.Slug):
		slug := firstNonEmpty(in.Slug, link.OrgSlug)
		if _, err := s.DB.ExecContext(ctx, `UPDATE circlexo_org_links SET status = 'active', org_slug = $2, updated_at = now() WHERE org_id = $1`, in.OrgID, slug); err != nil {
			return nil, err
		}
		link.Active, link.OrgSlug = true, slug
	}

	if err := s.syncMembers(ctx, link, org.Members); err != nil {
		return nil, err
	}
	if _, err := s.Client.ConfirmTenant(ctx, in.OrgID, link.TeamID, link.TeamSlug); err != nil {
		return nil, fmt.Errorf("circlexo: confirming the tenant: %w", err)
	}
	s.Ents.Invalidate(in.OrgID, 0)
	return link, nil
}

// Deprovision marks an org's link inactive. The team, its members and its data stay; it just stops
// being governed by the hub. An unknown org is a success (idempotent).
func (s *Service) Deprovision(ctx context.Context, orgID string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE circlexo_org_links SET status = 'inactive', updated_at = now() WHERE org_id = $1`, orgID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.Ents.Invalidate(orgID, 0)
	}
	return nil
}

// ownerFor picks the Hosbah user who owns a new team: the first hub owner, else the first admin,
// else the first member. A person with no Hosbah account yet gets a passwordless one (the account
// they land in when they sign in with CircleXO).
func (s *Service) ownerFor(ctx context.Context, members []circlexo.Member) (string, error) {
	pick := func(role string) *circlexo.Member {
		for i := range members {
			if strings.EqualFold(members[i].Role, role) {
				return &members[i]
			}
		}
		return nil
	}
	m := pick("owner")
	if m == nil {
		m = pick("admin")
	}
	if m == nil && len(members) > 0 {
		m = &members[0]
	}
	if m == nil {
		return "", errors.New("circlexo: the org has no members to own the team")
	}
	id, ok, err := s.resolveMember(ctx, *m, true)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("circlexo: the owner's Hosbah account cannot be used automatically (unconfirmed address)")
	}
	return id, nil
}

var slugJunk = regexp.MustCompile(`[^a-z0-9]+`)

// teamSlug is a URL-safe slug with a random suffix. Under row-level security a new team cannot see
// other teams' slugs, so the suffix is what makes it unique and the unique index is the arbiter.
func teamSlug(name string) string {
	base := strings.Trim(slugJunk.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(base) > 40 {
		base = strings.Trim(base[:40], "-")
	}
	if base == "" {
		base = "team"
	}
	return base + "-" + randomHex(3)
}

// createLinked makes the team and its link in one transaction, under an advisory lock on the org,
// so two concurrent installs of one org cannot make two teams. The team is created in the new
// owner's scope, exactly as POST /api/teams does (the insert policy wants id = app.team_id and
// owner = app.user_id), so no elevated scope is needed.
func (s *Service) createLinked(ctx context.Context, in ProvisionInput, ownerID string) (*OrgLink, error) {
	name := firstNonEmpty(in.Name, in.Slug, "CircleXO team")
	if len(name) > 80 {
		name = name[:80]
	}
	locale := "en"
	if strings.HasPrefix(strings.ToLower(in.Locale), "ar") {
		locale = "ar"
	}
	teamID := uuid.NewString()
	_, wrapped, err := s.Keys.NewTeamKey(teamID)
	if err != nil {
		return nil, fmt.Errorf("circlexo: creating the team key: %w", err)
	}

	err = tenancy.Tx(ctx, s.DB, tenancy.Scope{UserID: ownerID, TeamID: teamID}, func(q *db.Queries, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "circlexo:org:"+in.OrgID); err != nil {
			return err
		}
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT team_id FROM circlexo_org_links WHERE org_id = $1`, in.OrgID).Scan(&existing)
		if err == nil {
			// another request won: reuse its team
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var slug string
		for i := 0; ; i++ {
			slug = teamSlug(firstNonEmpty(in.Slug, name))
			// A savepoint per attempt: a unique violation must not poison the transaction.
			if _, err := tx.ExecContext(ctx, `SAVEPOINT slug`); err != nil {
				return err
			}
			_, err := q.InsertTeam(ctx, db.InsertTeamParams{ID: teamID, Name: name, Slug: slug, OwnerID: ownerID, DekWrapped: wrapped})
			if err == nil {
				break
			}
			if !isUnique(err) || i > 5 {
				return err
			}
			if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT slug`); err != nil {
				return err
			}
		}
		if _, err := q.InsertTeamMember(ctx, db.InsertTeamMemberParams{TeamID: teamID, UserID: ownerID, Role: tenancy.RoleOwner}); err != nil {
			return err
		}
		if err := tenancy.Record(ctx, q, teamID, "circlexo:"+in.OrgID, "team.created", "team", teamID,
			map[string]any{"name": name, "source": "circlexo", "org_id": in.OrgID}); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO circlexo_org_links (org_id, team_id, team_slug, org_slug, locale, status) VALUES ($1, $2, $3, $4, $5, 'active')`,
			in.OrgID, teamID, slug, in.Slug, locale)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.OrgByID(ctx, in.OrgID)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// resolveMember finds the Hosbah user for a hub member: by the CircleXO link, else by e-mail (only
// an address Hosbah verified is enough), else, with create, a new passwordless account. ok is false
// when the member cannot be placed automatically; they are picked up when they sign in with
// CircleXO. Resolving by e-mail does NOT create the link: that needs the person to prove the
// address by signing in.
func (s *Service) resolveMember(ctx context.Context, m circlexo.Member, create bool) (userID string, ok bool, err error) {
	if m.UserID != "" {
		id, err := s.UserBySubject(ctx, m.UserID)
		if err == nil {
			return id, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", false, err
		}
	}
	email := norm(m.Email)
	if email == "" {
		return "", false, nil
	}
	id, usable, err := s.userByEmail(ctx, email)
	switch {
	case err == nil:
		return id, usable, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", false, err
	case !create:
		return "", false, nil
	}
	id, err = s.createUser(ctx, email)
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// syncMembers adds every hub member the integration can place and applies the role mapping. It never
// removes anyone (a member added in Hosbah alone stays); removal is member.removed. It is not gated
// by the plan's member cap: the hub enforces seats when it adds a member.
func (s *Service) syncMembers(ctx context.Context, link *OrgLink, members []circlexo.Member) error {
	var first error
	for _, m := range members {
		id, ok, err := s.resolveMember(ctx, m, true)
		if err == nil && ok {
			err = s.setMember(ctx, link, id, m.Role)
		}
		if err != nil {
			s.Log.Error("circlexo: member sync", "team", link.TeamID, "err", err)
			if first == nil {
				first = err
			}
		}
	}
	return first
}

// setMember makes userID a member of the team with the role the hub role maps to, following the
// rules at the top of this file. Everything runs in the team's scope under an advisory lock, so
// concurrent syncs cannot both demote the last two owners.
func (s *Service) setMember(ctx context.Context, link *OrgLink, userID, hubRole string) error {
	target := mapRole(hubRole)
	return tenancy.Tx(ctx, s.DB, tenancy.Scope{TeamID: link.TeamID}, func(q *db.Queries, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "circlexo:members:"+link.TeamID); err != nil {
			return err
		}
		cur, err := q.MembershipFor(ctx, db.MembershipForParams{TeamID: link.TeamID, UserID: userID})
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := q.InsertTeamMember(ctx, db.InsertTeamMemberParams{TeamID: link.TeamID, UserID: userID, Role: target}); err != nil {
				return err
			}
			return tenancy.Record(ctx, q, link.TeamID, "circlexo:"+link.OrgID, "member.added", "team_member", userID,
				map[string]any{"role": target, "source": "circlexo"})
		}
		if err != nil {
			return err
		}
		role := cur.Role
		switch {
		case target == tenancy.RoleOwner || target == tenancy.RoleAdmin:
			role = target
		case cur.Role == tenancy.RoleOwner || cur.Role == tenancy.RoleAdmin:
			role = target // the hub took it away
		}
		if cur.Role == tenancy.RoleOwner && role != tenancy.RoleOwner {
			n, err := q.CountOwners(ctx, link.TeamID)
			if err != nil {
				return err
			}
			if n <= 1 {
				role = tenancy.RoleOwner // never leave a team without an owner
			}
		}
		if role == cur.Role {
			return nil
		}
		if _, err := q.UpdateMemberRole(ctx, db.UpdateMemberRoleParams{ID: cur.ID, TeamID: link.TeamID, Role: role}); err != nil {
			return err
		}
		return tenancy.Record(ctx, q, link.TeamID, "circlexo:"+link.OrgID, "member.role_changed", "team_member", userID,
			map[string]any{"from": cur.Role, "role": role, "source": "circlexo"})
	})
}

// removeMember removes a hub member's membership (their data stays), except the last owner's.
func (s *Service) removeMember(ctx context.Context, link *OrgLink, userID string) error {
	return tenancy.Tx(ctx, s.DB, tenancy.Scope{TeamID: link.TeamID}, func(q *db.Queries, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "circlexo:members:"+link.TeamID); err != nil {
			return err
		}
		cur, err := q.MembershipFor(ctx, db.MembershipForParams{TeamID: link.TeamID, UserID: userID})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if cur.Role == tenancy.RoleOwner {
			if n, err := q.CountOwners(ctx, link.TeamID); err != nil {
				return err
			} else if n <= 1 {
				s.Log.Warn("circlexo: not removing the last owner of a team", "team", link.TeamID)
				return nil
			}
		}
		if _, err := q.DeleteMemberInTeam(ctx, db.DeleteMemberInTeamParams{ID: cur.ID, TeamID: link.TeamID}); err != nil {
			return err
		}
		return tenancy.Record(ctx, q, link.TeamID, "circlexo:"+link.OrgID, "member.removed", "team_member", userID,
			map[string]any{"role": cur.Role, "source": "circlexo"})
	})
}
