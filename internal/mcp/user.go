package mcp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fadymondy/cloudy/internal/app"
	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/scopes"
	"github.com/fadymondy/cloudy/internal/tenancy"
	"github.com/fadymondy/cloudy/internal/tokens"
)

// UserServer is the team MCP at /api/mcp. A token belongs to one team and acts as the user who
// minted it, with that user's CURRENT role: removing someone from the team kills their tokens,
// and a viewer's token cannot invite even if it carries `write`.
func UserServer() *Server {
	return &Server{
		Name:          "cloudy",
		Version:       "0.1.0",
		Tools:         append(append(append(append(append(append(UserTools(), ServerTools()...), InfraTools()...), ProxyTools()...), AppTools()...), DatabaseTools()...), ManageTools()...),
		AllowedScopes: scopes.All,
		Authenticate:  authenticateUser,
		TxScope: func(c Caller) tenancy.Scope {
			return tenancy.Scope{UserID: c.UserID, TeamID: c.TeamID, TokenHash: c.TokenHash}
		},
	}
}

func authenticateUser(ctx context.Context, a *app.App, bearer string) (Caller, error) {
	if !strings.HasPrefix(bearer, tokens.PrefixUserMCP) || a == nil {
		return Caller{}, ErrUnauthorized
	}
	hash := tokens.Hash(bearer)
	var c Caller
	err := tenancy.Tx(ctx, a.SQLDB, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
		t, err := q.McpTokenByHash(ctx, hash)
		if err != nil || t.RevokedAt != nil || (t.ExpiresAt != nil && time.Now().After(*t.ExpiresAt)) {
			return ErrUnauthorized
		}
		if err := tenancy.SetTeam(ctx, tx, t.TeamID); err != nil {
			return err
		}
		m, err := q.MembershipFor(ctx, db.MembershipForParams{TeamID: t.TeamID, UserID: t.UserID})
		if err != nil {
			return ErrUnauthorized // the owner left or was removed: the token is dead
		}
		u, err := q.UserByID(ctx, t.UserID)
		if err != nil {
			return ErrUnauthorized
		}
		_ = q.TouchMcpToken(ctx, t.ID) // best effort
		c = Caller{TokenID: t.ID, TokenName: t.Name, TokenHash: hash, UserID: t.UserID, Email: u.Email,
			TeamID: t.TeamID, Role: m.Role, Scopes: scopes.Parse(t.Scopes)}
		return nil
	})
	if err != nil {
		return Caller{}, ErrUnauthorized
	}
	return c, nil
}

// needs refuses a call the token owner's team role does not allow, whatever the token's scopes.
func needs(ctx context.Context, env *Env, ability string) error {
	m := &tenancy.Membership{TeamID: env.Caller.TeamID, UserID: env.Caller.UserID, Role: env.Caller.Role}
	if !tenancy.Can(ctx, m, ability) {
		return fmt.Errorf("your role in this team (%s) does not allow that", env.Caller.Role)
	}
	return nil
}

// actor is how an MCP call appears in the audit log's causer column.
func actor(env *Env) string { return env.Caller.UserID }

func auditChanges(env *Env, extra map[string]any) map[string]any {
	if extra == nil {
		extra = map[string]any{}
	}
	extra["via"] = "mcp"
	extra["mcp_token_id"] = env.Caller.TokenID
	return extra
}

// UserTools is the user server's registry. P0 covers the resources that exist (team, members,
// invitations, audit, tokens); servers/apps/deploys arrive in P1+ with the same rules.
func UserTools() []Tool {
	return []Tool{
		{
			Name:        "whoami",
			Description: "Who this token acts as: user, team, role, the token's scopes and expiry.",
			Scope:       scopes.Read,
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				t, err := env.Q.GetTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("team not found")
				}
				return fmt.Sprintf("user: %s\nteam: %s (%s)\nrole: %s\ntoken: %s\nscopes: %s\n",
					env.Caller.Email, t.Name, t.ID, env.Caller.Role, env.Caller.TokenName,
					strings.Join(env.Caller.Scopes, ", ")), nil
			},
		},
		{
			Name:        "get_team",
			Description: "The token's team: name, slug, member count.",
			Scope:       scopes.Read,
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				t, err := env.Q.GetTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("team not found")
				}
				n, _ := env.Q.CountMembers(ctx, t.ID)
				return fmt.Sprintf("%s  (slug %s, id %s)\nmembers: %d\ncreated: %s\n",
					t.Name, t.Slug, t.ID, n, t.CreatedAt.Format(time.RFC3339)), nil
			},
		},
		{
			Name:        "list_members",
			Description: "Members of the token's team with their roles.",
			Scope:       scopes.Read,
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				rows, err := env.Q.ListMembersOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list members")
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d member(s):\n", len(rows))
				for _, r := range rows {
					fmt.Fprintf(&b, "  %-9s %s  (member id %s)\n", r.Role, r.Email, r.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name:        "list_invitations",
			Description: "The team's invitations and their status (needs an owner/admin role).",
			Scope:       scopes.Read,
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMembers); err != nil {
					return "", err
				}
				rows, err := env.Q.ListInvitationsOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list invitations")
				}
				if len(rows) == 0 {
					return "No invitations.", nil
				}
				var b strings.Builder
				for _, r := range rows {
					status := r.Status
					if status == "pending" && time.Now().After(r.ExpiresAt) {
						status = "expired"
					}
					fmt.Fprintf(&b, "  %-8s %-9s %s  (id %s)\n", status, r.Role, r.Email, r.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name:        "list_audit",
			Description: "Recent entries of the team's audit log (needs an owner/admin role).",
			Scope:       scopes.Read,
			InputSchema: schema(nil, map[string]any{"limit": map[string]any{"type": "integer", "default": 20, "maximum": 200}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityViewAudit); err != nil {
					return "", err
				}
				tid := env.Caller.TeamID
				rows, err := env.Q.ListTeamActivity(ctx, db.ListTeamActivityParams{TeamID: &tid, Limit: int32(args.Int("limit", 20, 1, 200))})
				if err != nil {
					return "", errors.New("could not read the audit log")
				}
				var b strings.Builder
				for _, r := range rows {
					fmt.Fprintf(&b, "  %s  %-24s %s  by %s\n", r.CreatedAt, r.Action, r.SubjectType, firstNonEmpty(r.CauserEmail, r.CauserID))
				}
				if b.Len() == 0 {
					return "The audit log is empty.", nil
				}
				return b.String(), nil
			},
		},
		{
			Name:        "list_mcp_tokens",
			Description: "The team's MCP tokens: names, scopes, last use. Never the secret.",
			Scope:       scopes.Read,
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageTokens); err != nil {
					return "", err
				}
				rows, err := env.Q.ListMcpTokensOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list tokens")
				}
				var b strings.Builder
				for _, r := range rows {
					state := "active"
					if r.RevokedAt != nil {
						state = "revoked"
					}
					fmt.Fprintf(&b, "  %-8s %s  [%s]  (id %s)\n", state, r.Name, r.Scopes, r.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name:        "invite_member",
			Description: "Invite someone to the team by email. Returns the one-time accept link (there is no email delivery yet).",
			Scope:       scopes.Write,
			InputSchema: schema([]string{"email", "role"}, map[string]any{
				"email": str("Invitee's email"),
				"role":  map[string]any{"type": "string", "enum": []string{"admin", "deployer", "viewer"}},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMembers); err != nil {
					return "", err
				}
				email := strings.ToLower(args.Str("email"))
				role := args.Str("role")
				if !strings.Contains(email, "@") || len(email) > 254 {
					return "", errors.New("a valid email is required")
				}
				if role != tenancy.RoleAdmin && role != tenancy.RoleDeployer && role != tenancy.RoleViewer {
					return "", errors.New("role must be admin, deployer or viewer")
				}
				raw := make([]byte, 32)
				_, _ = rand.Read(raw)
				token := hex.EncodeToString(raw)
				inv, err := env.Q.InsertInvitation(ctx, db.InsertInvitationParams{
					TeamID: env.Caller.TeamID, Email: email, Role: role, TokenHash: tokens.Hash(token),
					InvitedBy: env.Caller.UserID, ExpiresAt: time.Now().Add(7 * 24 * time.Hour).UTC(),
				})
				if err != nil {
					return "", errors.New("could not create the invitation")
				}
				if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "invitation.created", "invitation", inv.ID,
					auditChanges(env, map[string]any{"email": email, "role": role})); err != nil {
					return "", err
				}
				origin := strings.TrimRight(os.Getenv("CLOUDY_WEB_ORIGIN"), "/")
				return fmt.Sprintf("Invited %s as %s. Accept link (shown once): %s/invite/%s", email, role, origin, token), nil
			},
		},
		{
			Name:         "revoke_invitation",
			Description:  "Revoke a pending invitation. Destructive: confirm must equal the invitee's email.",
			Scope:        scopes.Destructive,
			Destructive:  true,
			ResourceType: "invitation",
			InputSchema: schema([]string{"id", "confirm"}, map[string]any{
				"id":      str("Invitation id (from list_invitations)"),
				"confirm": str("The invitee's email, exactly"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMembers); err != nil {
					return "", err
				}
				inv, err := env.Q.GetInvitation(ctx, args.Str("id"))
				if err != nil || inv.TeamID != env.Caller.TeamID {
					return "", errors.New("invitation not found")
				}
				if err := RequireConfirm(args, inv.Email); err != nil {
					return "", err
				}
				n, err := env.Q.RevokeInvitation(ctx, db.RevokeInvitationParams{ID: inv.ID, TeamID: env.Caller.TeamID})
				if err != nil || n == 0 {
					return "", errors.New("that invitation is not pending")
				}
				if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "invitation.revoked", "invitation", inv.ID,
					auditChanges(env, map[string]any{"email": inv.Email})); err != nil {
					return "", err
				}
				return "Revoked the invitation for " + inv.Email + ".", nil
			},
		},
		{
			Name:         "remove_member",
			Description:  "Remove a member from the team. Destructive: confirm must equal the member's email.",
			Scope:        scopes.Destructive,
			Destructive:  true,
			ResourceType: "member",
			InputSchema: schema([]string{"member_id", "confirm"}, map[string]any{
				"member_id": str("Member id (from list_members)"),
				"confirm":   str("The member's email, exactly"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMembers); err != nil {
					return "", err
				}
				target, err := env.Q.MemberInTeam(ctx, db.MemberInTeamParams{ID: args.Str("member_id"), TeamID: env.Caller.TeamID})
				if err != nil {
					return "", errors.New("member not found")
				}
				u, err := env.Q.UserByID(ctx, target.UserID)
				if err != nil {
					return "", errors.New("member not found")
				}
				if err := RequireConfirm(args, u.Email); err != nil {
					return "", err
				}
				if target.Role == tenancy.RoleOwner {
					if env.Caller.Role != tenancy.RoleOwner {
						return "", errors.New("only an owner can remove an owner")
					}
					if n, _ := env.Q.CountOwners(ctx, env.Caller.TeamID); n <= 1 {
						return "", errors.New("a team needs at least one owner")
					}
				}
				if n, err := env.Q.DeleteMemberInTeam(ctx, db.DeleteMemberInTeamParams{ID: target.ID, TeamID: env.Caller.TeamID}); err != nil || n == 0 {
					return "", errors.New("member not found")
				}
				if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "member.removed", "team_member", target.ID,
					auditChanges(env, map[string]any{"user_id": target.UserID, "role": target.Role})); err != nil {
					return "", err
				}
				return "Removed " + u.Email + " from the team.", nil
			},
		},
	}
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
