package platform

import (
	"context"
	"net/http"
	"strings"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
)

// User is one account as the admin lists show it. Accounts have no display name (the auth
// plugin stores only an email) and no last-seen time, so neither is reported.
type User struct {
	ID            string     `json:"id"`
	Email         string     `json:"email"`
	PlatformAdmin bool       `json:"platform_admin"`
	Disabled      bool       `json:"disabled"`
	DisabledAt    *time.Time `json:"disabled_at"`
	CreatedAt     string     `json:"created_at" doc:"RFC 3339, as the auth plugin stores it"`
	TeamCount     int64      `json:"team_count"`
}

type UserPage struct {
	Items []User `json:"items"`
	Total int64  `json:"total"`
}

type UserTeam struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type UserDetail struct {
	User
	Teams              []UserTeam `json:"teams"`
	McpTokenCount      int64      `json:"mcp_token_count" doc:"Live (unrevoked, unexpired) team MCP tokens"`
	AdminMcpTokenCount int64      `json:"admin_mcp_token_count" doc:"Live platform-admin MCP tokens"`
}

func ListUsers(ctx context.Context, q *db.Queries, search string, limit, offset int) (UserPage, error) {
	lim, off := Page(limit, offset)
	search = strings.TrimSpace(search)
	rows, err := q.AdminListUsers(ctx, db.AdminListUsersParams{Q: search, Lim: lim, Off: off})
	if err != nil {
		return UserPage{}, internal("list users")
	}
	total, err := q.AdminCountUsers(ctx, search)
	if err != nil {
		return UserPage{}, internal("count users")
	}
	p := UserPage{Items: []User{}, Total: total}
	for _, r := range rows {
		p.Items = append(p.Items, User{ID: r.ID, Email: r.Email, PlatformAdmin: IsAdminRoles(r.Roles),
			Disabled: r.DisabledAt != nil, DisabledAt: r.DisabledAt, CreatedAt: r.CreatedAt, TeamCount: r.TeamCount})
	}
	return p, nil
}

// FindUser resolves an id or an email.
func FindUser(ctx context.Context, q *db.Queries, ref string) (db.UserByIDRow, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.UserByIDRow{}, ErrNotFound
	}
	if u, err := q.UserByID(ctx, ref); err == nil {
		return u, nil
	}
	u, err := q.UserByEmail(ctx, ref)
	if err != nil {
		return db.UserByIDRow{}, ErrNotFound
	}
	return db.UserByIDRow(u), nil
}

func GetUser(ctx context.Context, q *db.Queries, ref string) (UserDetail, error) {
	u, err := FindUser(ctx, q, ref)
	if err != nil {
		return UserDetail{}, err
	}
	teams, err := q.ListTeamsForUser(ctx, u.ID)
	if err != nil {
		return UserDetail{}, internal("list the user's teams")
	}
	tc, err := q.AdminUserTokenCounts(ctx, u.ID)
	if err != nil {
		return UserDetail{}, internal("count the user's tokens")
	}
	d := UserDetail{User: User{ID: u.ID, Email: u.Email, PlatformAdmin: IsAdminRoles(u.Roles), Disabled: u.DisabledAt != nil,
		DisabledAt: u.DisabledAt, CreatedAt: u.CreatedAt, TeamCount: int64(len(teams))},
		Teams: []UserTeam{}, McpTokenCount: tc.McpTokens, AdminMcpTokenCount: tc.AdminMcpTokens}
	for _, t := range teams {
		d.Teams = append(d.Teams, UserTeam{ID: t.ID, Slug: t.Slug, Name: t.Name, Role: t.Role})
	}
	return d, nil
}

// UserPatch changes an account. A nil field is left alone.
type UserPatch struct {
	PlatformAdmin *bool
	Disabled      *bool
}

/*
UpdateUser grants or removes platform administration and disables or re-enables an account.

  - Nobody may demote or disable themselves (a locked-out platform is recovered only from the
    command line), and the last enabled platform admin can be neither demoted nor disabled. The
    admin rows are locked FOR UPDATE first, so two admins demoting each other at the same moment
    cannot both succeed.
  - Disabling stamps disabled_at and sessions_revoked_at and revokes every MCP token the account
    holds. The authorization middleware refuses a disabled account's session on its next
    request, and refuses any session issued before sessions_revoked_at even after re-enabling.
*/
func UpdateUser(ctx context.Context, q *db.Queries, actorID, ref string, p UserPatch) (UserDetail, error) {
	u, err := FindUser(ctx, q, ref)
	if err != nil {
		return UserDetail{}, err
	}
	wasAdmin, wasDisabled := IsAdminRoles(u.Roles), u.DisabledAt != nil
	demote := p.PlatformAdmin != nil && !*p.PlatformAdmin && wasAdmin
	disable := p.Disabled != nil && *p.Disabled && !wasDisabled
	if u.ID == actorID {
		if p.PlatformAdmin != nil && !*p.PlatformAdmin {
			return UserDetail{}, refuse(http.StatusConflict, "you cannot remove your own platform admin role — ask another admin")
		}
		if p.Disabled != nil && *p.Disabled {
			return UserDetail{}, refuse(http.StatusConflict, "you cannot disable your own account")
		}
	}
	if wasAdmin && (demote || disable) {
		admins, err := q.LockPlatformAdmins(ctx)
		if err != nil {
			return UserDetail{}, internal("check the platform admins")
		}
		others := 0
		for _, a := range admins {
			if a.ID != u.ID && a.DisabledAt == nil {
				others++
			}
		}
		if others == 0 {
			return UserDetail{}, refuse(http.StatusConflict, "%s is the last platform admin — grant someone else first", u.Email)
		}
	}

	changes := map[string]any{"email": u.Email}
	if p.PlatformAdmin != nil && *p.PlatformAdmin != wasAdmin {
		var roles []string
		for _, r := range strings.Split(u.Roles, ",") {
			if r = strings.TrimSpace(r); r != "" && r != "admin" {
				roles = append(roles, r)
			}
		}
		if *p.PlatformAdmin {
			roles = append(roles, "admin")
		}
		if err := q.SetUserRoles(ctx, db.SetUserRolesParams{ID: u.ID, Roles: strings.Join(roles, ",")}); err != nil {
			return UserDetail{}, internal("update the roles")
		}
		changes["platform_admin"] = map[string]bool{"from": wasAdmin, "to": *p.PlatformAdmin}
	}
	if p.Disabled != nil && *p.Disabled != wasDisabled {
		if err := q.SetUserDisabled(ctx, db.SetUserDisabledParams{ID: u.ID, Disabled: *p.Disabled}); err != nil {
			return UserDetail{}, internal("update the account")
		}
		if *p.Disabled {
			if err := q.RevokeMcpTokensOfUser(ctx, u.ID); err != nil {
				return UserDetail{}, internal("revoke the account's MCP tokens")
			}
			if err := q.RevokeAdminMcpTokensOfUser(ctx, u.ID); err != nil {
				return UserDetail{}, internal("revoke the account's admin MCP tokens")
			}
		}
		changes["disabled"] = map[string]bool{"from": wasDisabled, "to": *p.Disabled}
	}
	if len(changes) > 1 {
		if err := Record(ctx, q, actorID, "user.updated", "user", u.ID, changes); err != nil {
			return UserDetail{}, internal("write the audit entry")
		}
	}
	return GetUser(ctx, q, u.ID)
}

// DeleteUser removes an account, its memberships and its MCP tokens. It refuses while the account
// is the only owner of any team (the team would be left with nobody able to manage it), and
// refuses deleting yourself. Teams it co-owned pass teams.owner_id to another owner.
func DeleteUser(ctx context.Context, q *db.Queries, actorID, ref string) error {
	u, err := FindUser(ctx, q, ref)
	if err != nil {
		return err
	}
	if u.ID == actorID {
		return refuse(http.StatusConflict, "you cannot delete your own account")
	}
	sole, err := q.SoleOwnedTeams(ctx, u.ID)
	if err != nil {
		return internal("check the user's teams")
	}
	if len(sole) > 0 {
		names := make([]string, 0, len(sole))
		for _, t := range sole {
			names = append(names, t.Slug)
		}
		return refuse(http.StatusConflict, "%s is the only owner of %d team(s) (%s) — transfer ownership or delete those teams first",
			u.Email, len(sole), strings.Join(names, ", "))
	}
	if IsAdminRoles(u.Roles) {
		admins, err := q.LockPlatformAdmins(ctx)
		if err != nil {
			return internal("check the platform admins")
		}
		others := 0
		for _, a := range admins {
			if a.ID != u.ID && a.DisabledAt == nil {
				others++
			}
		}
		if others == 0 {
			return refuse(http.StatusConflict, "%s is the last platform admin — grant someone else first", u.Email)
		}
	}
	steps := []struct {
		what string
		fn   func(context.Context, string) error
	}{
		{"reassign team ownership", q.ReassignTeamOwnership},
		{"remove memberships", q.DeleteMembershipsOfUser},
		{"remove MCP tokens", q.DeleteMcpTokensOfUser},
		{"remove admin MCP tokens", q.DeleteAdminMcpTokensOfUser},
		{"remove 2FA", q.DeleteTotp},
	}
	for _, s := range steps {
		if err := s.fn(ctx, u.ID); err != nil {
			return internal(s.what)
		}
	}
	if n, err := q.DeleteUserAccount(ctx, u.ID); err != nil || n == 0 {
		return internal("delete the account")
	}
	return Record(ctx, q, actorID, "user.deleted", "user", u.ID, map[string]any{"email": u.Email})
}
