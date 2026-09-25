package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/platform"
	"github.com/fadymondy/hosbah/internal/quota"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Platform administration over REST: thin wrappers over internal/platform, which the admin MCP
// tools call too. Every route here is under /api/admin, which internal/server/authorization.go
// leaves in the default platform-admin tier; asPlatformAdmin re-checks the role in the database.

// platformErr turns a *platform.Error into the Huma error with its status (nil if err is not one).
func platformErr(err error) error {
	var pe *platform.Error
	if errors.As(err, &pe) {
		return huma.NewError(pe.Status, pe.Msg)
	}
	return nil
}

// quotaErr turns a *quota.Error into the Huma error with its status (nil if err is not one).
func quotaErr(err error) error {
	var qe *quota.Error
	if errors.As(err, &qe) {
		return huma.NewError(qe.Status, qe.Msg)
	}
	return nil
}

// asAdmin is asPlatformAdmin with platform errors mapped to their HTTP status.
func (d *deps) asAdmin(ctx context.Context, fn func(q *db.Queries, userID string) error) error {
	err := d.asPlatformAdmin(ctx, fn)
	if err == nil {
		return nil
	}
	if pe := platformErr(err); pe != nil {
		return pe
	}
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	return huma.Error500InternalServerError("platform administration failed")
}

type AdminPageQuery struct {
	Limit  int `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset int `query:"offset" default:"0" minimum:"0"`
}

type AdminIDPath struct {
	ID string `path:"id" maxLength:"320" doc:"An id (users: or an email; teams: or a slug)"`
}

func (d *deps) registerPlatformAdmin(api huma.API) {
	tags := []string{"Admin"}

	type overviewOut struct{ Body platform.Overview }
	huma.Register(api, huma.Operation{
		OperationID: "admin-overview", Method: http.MethodGet, Path: "/api/admin/overview",
		Summary: "Platform counts and recent sign-ups", Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*overviewOut, error) {
		out := &overviewOut{}
		return out, d.asAdmin(ctx, func(q *db.Queries, _ string) (err error) {
			out.Body, err = platform.GetOverview(ctx, q)
			return err
		})
	})

	// ---- users ----
	type usersOut struct{ Body platform.UserPage }
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-users", Method: http.MethodGet, Path: "/api/admin/users",
		Summary: "Every account; q matches the email (case-insensitive)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Q string `query:"q" maxLength:"200"`
		AdminPageQuery
	}) (*usersOut, error) {
		out := &usersOut{}
		return out, d.asAdmin(ctx, func(q *db.Queries, _ string) (err error) {
			out.Body, err = platform.ListUsers(ctx, q, in.Q, in.Limit, in.Offset)
			return err
		})
	})

	type userOut struct{ Body platform.UserDetail }
	huma.Register(api, huma.Operation{
		OperationID: "admin-get-user", Method: http.MethodGet, Path: "/api/admin/users/{id}",
		Summary: "One account with its teams and token counts", Tags: tags,
	}, func(ctx context.Context, in *AdminIDPath) (*userOut, error) {
		out := &userOut{}
		return out, d.asAdmin(ctx, func(q *db.Queries, _ string) (err error) {
			out.Body, err = platform.GetUser(ctx, q, in.ID)
			return err
		})
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-update-user", Method: http.MethodPatch, Path: "/api/admin/users/{id}",
		Summary: "Grant/remove platform admin, disable/enable an account",
		Description: "Omitted fields are left alone. You cannot demote or disable yourself, and the last enabled platform admin " +
			"cannot be demoted or disabled (409). Disabling refuses the account's sessions from their next request and revokes its MCP tokens.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		AdminIDPath
		Body struct {
			PlatformAdmin *bool `json:"platform_admin,omitempty"`
			Disabled      *bool `json:"disabled,omitempty"`
		}
	}) (*userOut, error) {
		out := &userOut{}
		return out, d.asAdmin(ctx, func(q *db.Queries, actor string) (err error) {
			out.Body, err = platform.UpdateUser(ctx, q, actor, in.ID, platform.UserPatch{PlatformAdmin: in.Body.PlatformAdmin, Disabled: in.Body.Disabled})
			return err
		})
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-delete-user", Method: http.MethodDelete, Path: "/api/admin/users/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Delete an account (409 while it is the only owner of a team)", Tags: tags,
	}, func(ctx context.Context, in *AdminIDPath) (*struct{}, error) {
		return nil, d.asAdmin(ctx, func(q *db.Queries, actor string) error {
			return platform.DeleteUser(ctx, q, actor, in.ID)
		})
	})

	// ---- teams ----
	type teamsOut struct{ Body platform.TeamPage }
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-teams", Method: http.MethodGet, Path: "/api/admin/teams",
		Summary: "Every team; q matches the name or slug", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Q string `query:"q" maxLength:"200"`
		AdminPageQuery
	}) (*teamsOut, error) {
		out := &teamsOut{}
		return out, d.asAdmin(ctx, func(q *db.Queries, _ string) (err error) {
			out.Body, err = platform.ListTeams(ctx, q, in.Q, in.Limit, in.Offset)
			return err
		})
	})

	type teamOut struct{ Body platform.TeamDetail }
	huma.Register(api, huma.Operation{
		OperationID: "admin-get-team", Method: http.MethodGet, Path: "/api/admin/teams/{id}",
		Summary: "One team with its members, servers and apps", Tags: tags,
	}, func(ctx context.Context, in *AdminIDPath) (*teamOut, error) {
		out := &teamOut{}
		return out, d.asAdmin(ctx, func(q *db.Queries, _ string) (err error) {
			out.Body, err = platform.GetTeam(ctx, q, in.ID)
			return err
		})
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-update-team", Method: http.MethodPatch, Path: "/api/admin/teams/{id}",
		Summary: "Suspend or reinstate a team",
		Description: "A suspended team's /api/teams/{team}/... routes and its MCP tokens answer 423 (platform admins who are members may still read). " +
			"Its agents keep connecting and its apps keep running.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		AdminIDPath
		Body struct {
			Suspended *bool `json:"suspended,omitempty"`
		}
	}) (*teamOut, error) {
		out := &teamOut{}
		return out, d.asAdmin(ctx, func(q *db.Queries, actor string) (err error) {
			if in.Body.Suspended == nil {
				out.Body, err = platform.GetTeam(ctx, q, in.ID)
				return err
			}
			out.Body, err = platform.SuspendTeam(ctx, q, actor, in.ID, *in.Body.Suspended)
			return err
		})
	})

	// ---- servers, operations ----
	type serversOut struct{ Body platform.ServerPage }
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-servers", Method: http.MethodGet, Path: "/api/admin/servers",
		Summary: "Every server across teams", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Q      string `query:"q" maxLength:"200" doc:"Matches name, hostname, IP or team slug"`
		Status string `query:"status" enum:"pending,provisioning,enrolled,failed,online,offline" doc:"A lifecycle status, or online/offline (enrolled servers by heartbeat)"`
		AdminPageQuery
	}) (*serversOut, error) {
		out := &serversOut{}
		return out, d.asAdmin(ctx, func(q *db.Queries, _ string) (err error) {
			out.Body, err = platform.ListServers(ctx, q, in.Q, in.Status, in.Limit, in.Offset)
			return err
		})
	})

	type operationsOut struct{ Body platform.OperationPage }
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-operations", Method: http.MethodGet, Path: "/api/admin/operations",
		Summary: "Recent operations across teams (never their params)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Status string `query:"status" enum:"pending,sent,running,ok,failed,timeout"`
		Kind   string `query:"kind" maxLength:"64"`
		AdminPageQuery
	}) (*operationsOut, error) {
		out := &operationsOut{}
		return out, d.asAdmin(ctx, func(q *db.Queries, _ string) (err error) {
			out.Body, err = platform.ListOperations(ctx, q, in.Status, in.Kind, in.Limit, in.Offset)
			return err
		})
	})

	// ---- settings ----
	type settingsOut struct{ Body platform.Settings }
	huma.Register(api, huma.Operation{
		OperationID: "admin-get-settings", Method: http.MethodGet, Path: "/api/admin/settings",
		Summary: "Platform settings", Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*settingsOut, error) {
		out := &settingsOut{}
		return out, d.asAdmin(ctx, func(q *db.Queries, _ string) (err error) {
			out.Body, err = platform.GetSettings(ctx, q)
			return err
		})
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-update-settings", Method: http.MethodPut, Path: "/api/admin/settings",
		Summary: "Change platform settings (omitted fields are left alone)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Body struct {
			SignupMode        *string `json:"signup_mode,omitempty" enum:"open,invite_only,closed"`
			Announcement      *string `json:"announcement,omitempty" maxLength:"500"`
			AnnouncementLevel *string `json:"announcement_level,omitempty" enum:"info,warning"`
			SupportEmail      *string `json:"support_email,omitempty" maxLength:"254"`
			MaxTeamsPerUser   *int64  `json:"max_teams_per_user,omitempty" minimum:"0" maximum:"100000" doc:"0 = unlimited"`
			MaxServersPerTeam *int64  `json:"max_servers_per_team,omitempty" minimum:"0" maximum:"100000" doc:"0 = unlimited"`
		}
	}) (*settingsOut, error) {
		out := &settingsOut{}
		b := in.Body
		return out, d.asAdmin(ctx, func(q *db.Queries, actor string) (err error) {
			out.Body, err = platform.UpdateSettings(ctx, q, actor, platform.SettingsPatch{SignupMode: b.SignupMode, Announcement: b.Announcement,
				AnnouncementLevel: b.AnnouncementLevel, SupportEmail: b.SupportEmail, MaxTeamsPerUser: b.MaxTeamsPerUser, MaxServersPerTeam: b.MaxServersPerTeam})
			return err
		})
	})

	// ---- quotas ----
	type quotaOut struct{ Body quota.Quota }
	huma.Register(api, huma.Operation{
		OperationID: "admin-get-team-quota", Method: http.MethodGet, Path: "/api/admin/teams/{id}/quota",
		Summary: "A team's quota (max servers/apps/data services; null = unlimited)", Tags: tags,
	}, func(ctx context.Context, in *AdminIDPath) (*quotaOut, error) {
		out := &quotaOut{}
		return out, d.asAdmin(ctx, func(q *db.Queries, _ string) error {
			t, err := platform.FindTeam(ctx, q, in.ID)
			if err != nil {
				return huma.Error404NotFound("team not found")
			}
			out.Body, err = quota.Get(ctx, q, t.ID)
			return err
		})
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-set-team-quota", Method: http.MethodPut, Path: "/api/admin/teams/{id}/quota",
		Summary: "Set a team's quota (a platform admin's own guardrail on top of the platform-wide settings; null clears a field back to unlimited)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		AdminIDPath
		Body struct {
			MaxServers      *int64 `json:"max_servers,omitempty" minimum:"0" maximum:"100000"`
			MaxApps         *int64 `json:"max_apps,omitempty" minimum:"0" maximum:"100000"`
			MaxDataServices *int64 `json:"max_data_services,omitempty" minimum:"0" maximum:"100000"`
		}
	}) (*quotaOut, error) {
		out := &quotaOut{}
		return out, d.asAdmin(ctx, func(q *db.Queries, actor string) error {
			t, err := platform.FindTeam(ctx, q, in.ID)
			if err != nil {
				return huma.Error404NotFound("team not found")
			}
			out.Body, err = quota.Set(ctx, q, t.ID, actor, quota.Quota{MaxServers: in.Body.MaxServers, MaxApps: in.Body.MaxApps, MaxDataServices: in.Body.MaxDataServices})
			return err
		})
	})

	// ---- audit ----
	type auditOut struct{ Body platform.AuditPage }
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-audit", Method: http.MethodGet, Path: "/api/admin/audit",
		Summary: "The platform audit log (what platform admins changed), newest first", Tags: tags,
	}, func(ctx context.Context, in *AdminPageQuery) (*auditOut, error) {
		out := &auditOut{}
		return out, d.asAdmin(ctx, func(q *db.Queries, _ string) (err error) {
			out.Body, err = platform.ListAudit(ctx, q, in.Limit, in.Offset)
			return err
		})
	})

	// ---- public ----
	type publicOut struct{ Body platform.PublicSettings }
	huma.Register(api, huma.Operation{
		OperationID: "public-settings", Method: http.MethodGet, Path: "/api/settings/public",
		Summary: "Sign-up mode, announcement and support email (no sign-in needed)", Tags: []string{"Platform"},
	}, func(ctx context.Context, _ *struct{}) (*publicOut, error) {
		out := &publicOut{}
		err := d.tx(ctx, tenancy.Scope{}, func(q *db.Queries, _ *sql.Tx) error {
			s, err := platform.GetSettings(ctx, q)
			if err != nil {
				return err
			}
			out.Body = s.Public()
			return nil
		})
		if pe := platformErr(err); pe != nil {
			return nil, pe
		}
		return out, err
	})
}
