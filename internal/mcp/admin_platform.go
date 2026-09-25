package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fadymondy/hosbah/internal/platform"
	"github.com/fadymondy/hosbah/internal/quota"
)

// PlatformTools are the admin server's platform-administration tools. Each is a thin wrapper
// over internal/platform — the same functions behind /api/admin/* — and answers with the same
// JSON the REST route returns, so the rules (self-demotion, last admin, sole owner, limits) and
// the platform audit entries are identical on both surfaces.
func PlatformTools() []Tool {
	page := func(def int) map[string]any {
		return map[string]any{"type": "integer", "default": def, "minimum": 1, "maximum": 200}
	}
	offset := map[string]any{"type": "integer", "default": 0, "minimum": 0}
	boolean := func(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
	return []Tool{
		{
			Name:        "admin_overview",
			Description: "Platform counts: users, platform admins, teams, servers (total/online/offline), apps, deployments and operations in the last 24h (total/failed), databases, and the 5 newest sign-ups.",
			Scope:       ScopeAdmin,
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				return asJSON(platform.GetOverview(ctx, env.Q))
			},
		},
		{
			Name:        "list_all_servers",
			Description: "Every server across all teams, with its team, provider, kind, status, online state, IP, agent version and last heartbeat.",
			Scope:       ScopeAdmin,
			InputSchema: schema(nil, map[string]any{
				"q":      str("Matches name, hostname, IP or team slug"),
				"status": map[string]any{"type": "string", "enum": platform.ServerStatuses},
				"limit":  page(50), "offset": offset,
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return asJSON(platform.ListServers(ctx, env.Q, args.Str("q"), args.Str("status"), args.Int("limit", 50, 1, 200), args.Int("offset", 0, 0, 1<<30)))
			},
		},
		{
			Name:        "list_operations",
			Description: "Recent operations across all teams (kind, status, server, team, exit code, error summary). Never shows operation parameters.",
			Scope:       ScopeAdmin,
			InputSchema: schema(nil, map[string]any{
				"status": map[string]any{"type": "string", "enum": platform.OperationStatuses},
				"kind":   str("e.g. system.info, deploy.release"),
				"limit":  page(50), "offset": offset,
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return asJSON(platform.ListOperations(ctx, env.Q, args.Str("status"), args.Str("kind"), args.Int("limit", 50, 1, 200), args.Int("offset", 0, 0, 1<<30)))
			},
		},
		{
			Name: "update_user",
			Description: "Grant/remove platform admin or disable/enable an account (by id or email). Omitted fields are left alone. " +
				"You cannot demote or disable yourself, nor the last enabled platform admin. Disabling ends the account's sessions and revokes its MCP tokens.",
			Scope: ScopeAdmin,
			InputSchema: schema([]string{"user"}, map[string]any{
				"user": str("Id or email"), "platform_admin": boolean(""), "disabled": boolean(""),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				p := platform.UserPatch{PlatformAdmin: optBool(args, "platform_admin"), Disabled: optBool(args, "disabled")}
				if p.PlatformAdmin == nil && p.Disabled == nil {
					return "", errors.New("pass platform_admin and/or disabled")
				}
				return asJSON(platform.UpdateUser(ctx, env.Q, env.Caller.UserID, args.Str("user"), p))
			},
		},
		{
			Name: "delete_user",
			Description: "Delete an account, its team memberships and its MCP tokens. Refused while it is the only owner of any team. " +
				"Destructive: confirm must equal the account's email.",
			Scope:        ScopeAdmin,
			Destructive:  true,
			ResourceType: "user",
			InputSchema: schema([]string{"user", "confirm"}, map[string]any{
				"user": str("Id or email"), "confirm": str("The account's email, exactly"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				u, err := platform.FindUser(ctx, env.Q, args.Str("user"))
				if err != nil {
					return "", errors.New("user not found")
				}
				if err := RequireConfirm(args, u.Email); err != nil {
					return "", err
				}
				if err := platform.DeleteUser(ctx, env.Q, env.Caller.UserID, u.ID); err != nil {
					return "", err
				}
				return fmt.Sprintf("Deleted %s.", u.Email), nil
			},
		},
		{
			Name: "suspend_team",
			Description: "Suspend (or, with suspended=false, reinstate) a team by id or slug. A suspended team's routes and MCP tokens answer 423; " +
				"its servers keep running and its agents stay connected.",
			Scope: ScopeAdmin,
			InputSchema: schema([]string{"team", "suspended"}, map[string]any{
				"team": str("Id or slug"), "suspended": boolean("true to suspend, false to reinstate"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				s := optBool(args, "suspended")
				if s == nil {
					return "", errors.New("suspended (true or false) is required")
				}
				return asJSON(platform.SuspendTeam(ctx, env.Q, env.Caller.UserID, args.Str("team"), *s))
			},
		},
		{
			Name:        "get_settings",
			Description: "Platform settings: signup_mode, announcement, announcement_level, support_email, max_teams_per_user, max_servers_per_team (0 = unlimited).",
			Scope:       ScopeAdmin,
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				return asJSON(platform.GetSettings(ctx, env.Q))
			},
		},
		{
			Name:        "update_settings",
			Description: "Change platform settings. Omitted fields are left alone.",
			Scope:       ScopeAdmin,
			InputSchema: schema(nil, map[string]any{
				"signup_mode":          map[string]any{"type": "string", "enum": []string{"open", "invite_only", "closed"}},
				"announcement":         map[string]any{"type": "string", "maxLength": platform.MaxAnnouncement, "description": "Empty clears it"},
				"announcement_level":   map[string]any{"type": "string", "enum": []string{"info", "warning"}},
				"support_email":        str("Empty clears it"),
				"max_teams_per_user":   map[string]any{"type": "integer", "minimum": 0, "description": "0 = unlimited"},
				"max_servers_per_team": map[string]any{"type": "integer", "minimum": 0, "description": "0 = unlimited"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				p := platform.SettingsPatch{SignupMode: optString(args, "signup_mode"), Announcement: optString(args, "announcement"),
					AnnouncementLevel: optString(args, "announcement_level"), SupportEmail: optString(args, "support_email"),
					MaxTeamsPerUser: optInt(args, "max_teams_per_user"), MaxServersPerTeam: optInt(args, "max_servers_per_team")}
				return asJSON(platform.UpdateSettings(ctx, env.Q, env.Caller.UserID, p))
			},
		},
		{
			Name:        "get_team_quota",
			Description: "A team's quota (max_servers, max_apps, max_data_services; null = unlimited), by id or slug.",
			Scope:       ScopeAdmin,
			InputSchema: schema([]string{"team"}, map[string]any{"team": str("Id or slug")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				t, err := platform.FindTeam(ctx, env.Q, args.Str("team"))
				if err != nil {
					return "", errors.New("team not found")
				}
				return asJSON(quota.Get(ctx, env.Q, t.ID))
			},
		},
		{
			Name: "set_team_quota",
			Description: "Set a team's quota, by id or slug — a platform admin's own guardrail on top of the platform-wide settings. " +
				"Omitted fields are left alone; send 0 to make a field unlimited again.",
			Scope: ScopeAdmin,
			InputSchema: schema([]string{"team"}, map[string]any{
				"team":              str("Id or slug"),
				"max_servers":       map[string]any{"type": "integer", "minimum": 0, "description": "0 = unlimited"},
				"max_apps":          map[string]any{"type": "integer", "minimum": 0, "description": "0 = unlimited"},
				"max_data_services": map[string]any{"type": "integer", "minimum": 0, "description": "0 = unlimited"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				t, err := platform.FindTeam(ctx, env.Q, args.Str("team"))
				if err != nil {
					return "", errors.New("team not found")
				}
				q := quota.Quota{MaxServers: unlimitedIfZero(optInt(args, "max_servers")), MaxApps: unlimitedIfZero(optInt(args, "max_apps")),
					MaxDataServices: unlimitedIfZero(optInt(args, "max_data_services"))}
				return asJSON(quota.Set(ctx, env.Q, t.ID, env.Caller.UserID, q))
			},
		},
		{
			Name:        "list_platform_audit",
			Description: "The platform audit log (user, team and settings changes by platform admins), newest first, with the actor's email.",
			Scope:       ScopeAdmin,
			InputSchema: schema(nil, map[string]any{"limit": page(50), "offset": offset}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return asJSON(platform.ListAudit(ctx, env.Q, args.Int("limit", 50, 1, 200), args.Int("offset", 0, 0, 1<<30)))
			},
		},
	}
}

// asJSON renders a platform result as indented JSON — the same shape the REST route returns.
func asJSON(v any, err error) (string, error) {
	if err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", errors.New("could not render the result")
	}
	return string(b), nil
}

// optBool / optString / optInt distinguish "not passed" (nil) from a zero value.
func optBool(a Args, k string) *bool {
	if v, ok := a[k].(bool); ok {
		return &v
	}
	return nil
}

func optString(a Args, k string) *string {
	if v, ok := a[k].(string); ok {
		return &v
	}
	return nil
}

// unlimitedIfZero turns the update_settings/set_team_quota convention (0 means "clear back to
// unlimited") into the quota package's own (nil means unlimited).
func unlimitedIfZero(v *int64) *int64 {
	if v == nil || *v == 0 {
		return nil
	}
	return v
}

func optInt(a Args, k string) *int64 {
	if v, ok := a[k].(float64); ok {
		n := int64(v)
		return &n
	}
	return nil
}
