package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Server updates (MH-578 C8): listing upgradable packages, running the upgrade, and toggling
// unattended/automatic security updates. Every read is any member; running the upgrade or changing
// the automatic-updates setting is owner or admin and audited. Results persist to
// server_update_statuses so the server page has something to show without dialing the agent again.

// UpdateStatusOut is one server's last known package/update state.
type UpdateStatusOut struct {
	ServerID            string            `json:"server_id"`
	OSFamily            string            `json:"os_family"`
	PackageManager      string            `json:"package_manager"`
	UpgradableCount      int               `json:"upgradable_count"`
	Packages            []agentproto.PkgInfo `json:"packages"`
	RebootRequired      bool              `json:"reboot_required"`
	AutoSecurityUpdates bool              `json:"auto_security_updates"`
	AutoSupported       bool              `json:"auto_supported"`
	LastCheckedAt       *time.Time        `json:"last_checked_at,omitempty"`
	LastUpgradedAt      *time.Time        `json:"last_upgraded_at,omitempty"`
	LastError           string            `json:"last_error,omitempty"`
}

func toUpdateStatusOut(row db.ServerUpdateStatus) UpdateStatusOut {
	out := UpdateStatusOut{
		ServerID: row.ServerID, OSFamily: row.OsFamily, PackageManager: row.PackageManager,
		UpgradableCount: int(row.UpgradableCount), RebootRequired: row.RebootRequired,
		AutoSecurityUpdates: row.AutoSecurityUpdates, AutoSupported: row.AutoSupported,
		LastCheckedAt: row.LastCheckedAt, LastUpgradedAt: row.LastUpgradedAt, LastError: row.LastError,
	}
	_ = json.Unmarshal([]byte(row.Packages), &out.Packages)
	if out.Packages == nil {
		out.Packages = []agentproto.PkgInfo{}
	}
	return out
}

// UpgradeIn is what to upgrade.
type UpgradeIn struct {
	SecurityOnly bool `json:"security_only,omitempty" doc:"dnf only: --security. apt has no portable security-only flag, so this is ignored there."`
}

// AutoUpdatesIn toggles automatic security updates.
type AutoUpdatesIn struct {
	Enabled bool `json:"enabled"`
}

func (d *deps) registerServerUpdates(api huma.API) {
	tags := []string{"Server updates"}
	base := "/api/teams/{team}/servers/{server}/updates"

	type updateStatusRespOut struct{ Body UpdateStatusOut }
	huma.Register(api, huma.Operation{OperationID: "get-server-updates", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The server's last known upgradable-package listing. Any member.",
	}, func(ctx context.Context, in *ServerPath) (*updateStatusRespOut, error) {
		out := &updateStatusRespOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			row, err := q.GetServerUpdateStatus(ctx, db.GetServerUpdateStatusParams{ServerID: srv.ID, TeamID: m.TeamID})
			if isNoRows(err) {
				out.Body = UpdateStatusOut{ServerID: srv.ID, Packages: []agentproto.PkgInfo{}}
				return nil
			}
			if err != nil {
				return actionErr(err, "could not read the server's update status")
			}
			out.Body = toUpdateStatusOut(row)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "check-server-updates", Method: http.MethodPost, Path: base + "/check", Tags: tags,
		Summary: "Ask the agent to list upgradable packages now (apt/dnf/apk aware) and record the result. Any member.",
	}, func(ctx context.Context, in *ServerPath) (*updateStatusRespOut, error) {
		out := &updateStatusRespOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			var res agentproto.PkgListResult
			if err := d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindPkgList, agentproto.PkgListParams{}, &res); err != nil {
				return err
			}
			packages, _ := json.Marshal(res.Packages)
			if len(packages) == 0 {
				packages = []byte("[]")
			}
			now := time.Now()
			row, err := q.UpsertServerUpdateStatusCheck(ctx, db.UpsertServerUpdateStatusCheckParams{
				TeamID: m.TeamID, ServerID: srv.ID, OsFamily: string(res.OSFamily), PackageManager: string(res.PackageManager),
				UpgradableCount: int64(res.UpgradableCount), Packages: string(packages), RebootRequired: res.RebootRequired,
				LastCheckedAt: &now,
			})
			if err != nil {
				return actionErr(err, "could not record the server's update status")
			}
			out.Body = toUpdateStatusOut(row)
			return nil
		})
		return out, err
	})

	type updateUpgradeRespOut struct{ Body agentproto.PkgUpgradeResult }
	huma.Register(api, huma.Operation{OperationID: "run-server-upgrade", Method: http.MethodPost, Path: base + "/upgrade", Tags: tags,
		Summary: "Run the upgrade now, streamed like any other long operation (GET/stream the generic operation endpoints). " +
			"Reports whether a reboot is now required. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body UpgradeIn
	}) (*updateUpgradeRespOut, error) {
		out := &updateUpgradeRespOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			p := agentproto.PkgUpgradeParams{SecurityOnly: in.Body.SecurityOnly}
			if err := d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindPkgUpgrade, p, &out.Body); err != nil {
				return err
			}
			now := time.Now()
			if _, err := q.RecordServerUpgradeRun(ctx, db.RecordServerUpgradeRunParams{
				TeamID: m.TeamID, ServerID: srv.ID, RebootRequired: out.Body.RebootRequired, LastUpgradedAt: &now,
			}); err != nil {
				return actionErr(err, "could not record the upgrade")
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "server_updates.upgrade", "server", srv.ID,
				map[string]any{"security_only": in.Body.SecurityOnly, "upgraded": out.Body.Upgraded, "reboot_required": out.Body.RebootRequired})
		})
		return out, err
	})

	type updateAutoRespOut struct{ Body agentproto.PkgAutoupdateSetResult }
	huma.Register(api, huma.Operation{OperationID: "set-server-auto-updates", Method: http.MethodPut, Path: base + "/auto", Tags: tags,
		Summary: "Turn automatic security updates on or off (unattended-upgrades on Debian/Ubuntu, dnf-automatic on RHEL; " +
			"reports unsupported otherwise). Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body AutoUpdatesIn
	}) (*updateAutoRespOut, error) {
		out := &updateAutoRespOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			p := agentproto.PkgAutoupdateSetParams{Enabled: in.Body.Enabled}
			if err := d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindPkgAutoupdateSet, p, &out.Body); err != nil {
				return err
			}
			if _, err := q.SetServerAutoUpdates(ctx, db.SetServerAutoUpdatesParams{
				TeamID: m.TeamID, ServerID: srv.ID,
				AutoSecurityUpdates: out.Body.Enabled, AutoSupported: out.Body.Supported,
			}); err != nil {
				return actionErr(err, "could not record the automatic-updates setting")
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "server_updates.auto_set", "server", srv.ID,
				map[string]any{"enabled": in.Body.Enabled, "supported": out.Body.Supported})
		})
		return out, err
	})
}
