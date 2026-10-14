package platform

import (
	"context"
	"database/sql"
	"fmt"
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
		if err := q.SetUserDisabled(ctx, db.SetUserDisabledParams{ID: u.ID, Disabled: *p.Disabled, RevokedAt: time.Now().UTC()}); err != nil {
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

// Refusal codes of DeleteOwnAccount.
const (
	CodeSoleOwner     = "sole_owner"      // a team with other members has nobody else able to manage it
	CodeTeamHasServer = "team_has_servers" // a team only you are in still manages servers
	CodeLastAdmin     = "last_admin"
)

/*
DeleteOwnAccount is self-service account deletion (DELETE /api/me; App Store guideline 5.1.1(v)).
The caller has re-authenticated the user and opened tx with tenancy.Scope{UserID: userID,
PlatformAdmin: true} (the same scope the admin deletion runs in: the statements below name the user,
or a team only they are in, and nothing else).

The teams the account is the only owner of decide what happens:

  - A team with other members is refused (sole_owner): deleting it would take their servers and apps
    away from them, and leaving it would leave nobody able to manage it. Make another member an owner
    (or remove them) first.
  - A team only this account is in, still managing servers, is refused (team_has_servers): those
    machines keep running whatever happens here, and deleting the team would leave them running with
    an agent nobody controls. Remove the servers first (Servers → Remove), which also uninstalls the
    agent.
  - A team only this account is in, with no servers left, is deleted with the account; everything in
    it (apps go with their servers, so what is left is settings, tokens, domains and the like) cascades.

Then exactly the admin deletion's steps: the last platform admin cannot go, co-owned teams pass
teams.owner_id to another owner, memberships, MCP tokens and two-factor go, and the user row (its
identities, devices, sessions' anchor) is deleted.
*/
func DeleteOwnAccount(ctx context.Context, q *db.Queries, tx *sql.Tx, userID string) error {
	u, err := q.UserByID(ctx, userID)
	if err != nil {
		return ErrNotFound
	}
	sole, err := q.SoleOwnedTeamsForDeletion(ctx, userID)
	if err != nil {
		return internal("check your teams")
	}
	var shared, busy []string
	for _, t := range sole {
		switch {
		case t.OtherMembers > 0:
			shared = append(shared, t.Name)
		case t.Servers > 0:
			busy = append(busy, t.Name)
		}
	}
	if len(shared) > 0 {
		return &Error{Status: http.StatusConflict, Code: CodeSoleOwner, Msg: fmt.Sprintf(
			"you are the only owner of %s — make another member an owner, or remove the other members, first", strings.Join(shared, ", "))}
	}
	if len(busy) > 0 {
		return &Error{Status: http.StatusConflict, Code: CodeTeamHasServer, Msg: fmt.Sprintf(
			"%s still manages servers — remove them from the team first, so none is left running unmanaged", strings.Join(busy, ", "))}
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
			return &Error{Status: http.StatusConflict, Code: CodeLastAdmin, Msg: "you are the last platform admin — make someone else an admin first"}
		}
	}
	for _, t := range sole {
		// A savepoint, so a team that cannot go (something still restricts it) is a clear refusal
		// rather than an aborted transaction.
		if _, err := tx.ExecContext(ctx, "SAVEPOINT delete_team"); err != nil {
			return internal("delete your teams")
		}
		if n, err := q.DeleteTeamRow(ctx, t.ID); err != nil || n != 1 {
			_, _ = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT delete_team")
			return &Error{Status: http.StatusConflict, Code: CodeTeamHasServer, Msg: fmt.Sprintf(
				"%s still has resources that must be removed first", t.Name)}
		}
		if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT delete_team"); err != nil {
			return internal("delete your teams")
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
	teams := make([]string, 0, len(sole))
	for _, t := range sole {
		teams = append(teams, t.Slug)
	}
	// Recorded before the row goes (the audit entry names the actor, who is about to disappear).
	if err := Record(ctx, q, u.ID, "user.self_deleted", "user", u.ID, map[string]any{"email": u.Email, "teams_deleted": teams}); err != nil {
		return internal("write the audit entry")
	}
	if n, err := q.DeleteUserAccount(ctx, u.ID); err != nil || n == 0 {
		return internal("delete the account")
	}
	return nil
}
