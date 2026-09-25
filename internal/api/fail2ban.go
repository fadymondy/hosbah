package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// MH-580 J2: fail2ban, one sshd jail per server. fail2ban_settings holds the team's last-saved
// bantime/findtime/maxretry/enabled (so the web UI has something to show before the first install),
// but the actual jail on the server only changes when POST .../fail2ban/settings runs
// fail2ban.install through the agent.

// Fail2banSettingsOut is a server's fail2ban settings, defaulted when none has been saved yet.
type Fail2banSettingsOut struct {
	ServerID string `json:"server_id"`
	Enabled  bool   `json:"enabled"`
	Bantime  int    `json:"bantime"`
	Findtime int    `json:"findtime"`
	Maxretry int    `json:"maxretry"`
}

func defaultFail2banSettings(serverID string) Fail2banSettingsOut {
	return Fail2banSettingsOut{ServerID: serverID, Enabled: false, Bantime: 600, Findtime: 600, Maxretry: 5}
}

func toFail2banSettingsOut(s db.Fail2banSetting) Fail2banSettingsOut {
	return Fail2banSettingsOut{ServerID: s.ServerID, Enabled: s.Enabled, Bantime: int(s.Bantime), Findtime: int(s.Findtime),
		Maxretry: int(s.Maxretry)}
}

// Fail2banSettingsIn is what a caller sends to change and install a server's sshd jail.
type Fail2banSettingsIn struct {
	Enabled  bool `json:"enabled"`
	Bantime  int  `json:"bantime,omitempty" doc:"Seconds, 30-31536000. Defaults to 600."`
	Findtime int  `json:"findtime,omitempty" doc:"Seconds, 10-31536000. Defaults to 600."`
	Maxretry int  `json:"maxretry,omitempty" doc:"1-100. Defaults to 5."`
}

func (in Fail2banSettingsIn) toParams() agentproto.Fail2banInstallParams {
	p := agentproto.Fail2banInstallParams{Enabled: in.Enabled, Bantime: in.Bantime, Findtime: in.Findtime, Maxretry: in.Maxretry}
	if p.Bantime == 0 {
		p.Bantime = 600
	}
	if p.Findtime == 0 {
		p.Findtime = 600
	}
	if p.Maxretry == 0 {
		p.Maxretry = 5
	}
	return p
}

func (d *deps) registerFail2ban(api huma.API) {
	tags := []string{"Fail2ban"}
	base := "/api/teams/{team}/servers/{server}/fail2ban"

	type settingsOut struct{ Body Fail2banSettingsOut }
	huma.Register(api, huma.Operation{OperationID: "get-fail2ban-settings", Method: http.MethodGet, Path: base + "/settings", Tags: tags,
		Summary: "A server's last-saved fail2ban sshd jail settings (may not match what is actually installed until " +
			"POST .../fail2ban/settings has run). Any member.",
	}, func(ctx context.Context, in *ServerPath) (*settingsOut, error) {
		out := &settingsOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			s, err := q.Fail2banSettingOfServer(ctx, db.Fail2banSettingOfServerParams{ServerID: srv.ID, TeamID: m.TeamID})
			if err != nil {
				out.Body = defaultFail2banSettings(srv.ID)
				return nil
			}
			out.Body = toFail2banSettingsOut(s)
			return nil
		})
		return out, err
	})

	type installOut struct {
		Body agentproto.Fail2banInstallResult
	}
	huma.Register(api, huma.Operation{OperationID: "install-fail2ban", Method: http.MethodPost, Path: base + "/settings", Tags: tags,
		Summary: "Install fail2ban if needed and (re)configure its sshd jail with these settings (owner or admin, audited). " +
			"enabled=false stops and disables the jail without uninstalling the package.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body Fail2banSettingsIn
	}) (*installOut, error) {
		out := &installOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			p := in.Body.toParams()
			if err := p.Validate(); err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			if err := d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindFail2banInstall, p, &out.Body); err != nil {
				return err
			}
			if _, err := q.UpsertFail2banSetting(ctx, db.UpsertFail2banSettingParams{TeamID: m.TeamID, ServerID: srv.ID,
				Enabled: p.Enabled, Bantime: int32(p.Bantime), Findtime: int32(p.Findtime), Maxretry: int32(p.Maxretry),
				UpdatedBy: id.ID}); err != nil {
				return huma.Error500InternalServerError("could not save the fail2ban settings")
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "fail2ban.installed", "server", srv.ID,
				map[string]any{"enabled": p.Enabled, "bantime": p.Bantime, "findtime": p.Findtime, "maxretry": p.Maxretry})
		})
		return out, err
	})

	type bannedOut struct {
		Body agentproto.Fail2banBannedResult
	}
	huma.Register(api, huma.Operation{OperationID: "list-fail2ban-banned", Method: http.MethodGet, Path: base + "/banned", Tags: tags,
		Summary: "IPs currently banned in the sshd jail, read live from the server. Any member.",
	}, func(ctx context.Context, in *ServerPath) (*bannedOut, error) {
		out := &bannedOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			return d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindFail2banBanned, struct{}{}, &out.Body)
		})
		return out, err
	})

	type unbanOut struct {
		Body agentproto.Fail2banUnbanResult
	}
	huma.Register(api, huma.Operation{OperationID: "unban-fail2ban-ip", Method: http.MethodPost, Path: base + "/unban", Tags: tags,
		Summary: "Clear one IP from the sshd jail (owner or admin, audited).",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body agentproto.Fail2banUnbanParams
	}) (*unbanOut, error) {
		out := &unbanOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if err := in.Body.Validate(); err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			if err := d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindFail2banUnban, in.Body, &out.Body); err != nil {
				return err
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "fail2ban.unbanned", "server", srv.ID, map[string]any{"ip": in.Body.IP})
		})
		return out, err
	})
}
