// Package platform is Hosbah's platform administration: the overview, cross-team user, team,
// server and operation lists, account and team moderation, platform settings and their limits,
// and the platform audit log.
//
// It is the one implementation behind both the REST routes (internal/api/admin*.go, under
// /api/admin, platform-admin tier) and the admin MCP tools (internal/mcp/admin.go). Functions
// take the *db.Queries of a transaction the caller opened with tenancy.Scope{PlatformAdmin:
// true}; the *_platform_admin_select policies are what let those reads cross teams, and they
// are SELECT-only — nothing here can operate a team's servers.
//
// A refusal the caller should show as is comes back as *Error, carrying an HTTP status.
package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Error is a refusal with the HTTP status it maps to. Msg is safe to show.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func refuse(status int, format string, args ...any) error {
	return &Error{Status: status, Msg: fmt.Sprintf(format, args...)}
}

// ErrNotFound is the answer for an unknown user or team id.
var ErrNotFound = &Error{Status: http.StatusNotFound, Msg: "not found"}

func internal(what string) error {
	return &Error{Status: http.StatusInternalServerError, Msg: "could not " + what}
}

// Page normalises limit/offset: limit 1..200 (default 50), offset >= 0.
func Page(limit, offset int) (int32, int32) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return int32(limit), int32(offset) //#nosec G115 -- bounded above
}

func onlineSeconds() int32 { return int32(ops.OnlineWindow / time.Second) }

// IsAdminRoles reports whether a users.roles CSV holds the platform `admin` role.
func IsAdminRoles(csv string) bool {
	for _, r := range strings.Split(csv, ",") {
		if strings.TrimSpace(r) == "admin" {
			return true
		}
	}
	return false
}

// Record appends a platform audit entry on the caller's transaction, so it commits or rolls back
// with the change it describes. details must never hold a secret.
func Record(ctx context.Context, q *db.Queries, actorID, action, targetType, targetID string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	body, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return q.InsertPlatformAudit(ctx, db.InsertPlatformAuditParams{ActorID: actorID, Action: action,
		TargetType: targetType, TargetID: targetID, Details: string(body), Ip: tenancy.MetaFrom(ctx).IP})
}

// ---- overview ------------------------------------------------------------------------------

type ServerCounts struct {
	Total   int64 `json:"total"`
	Online  int64 `json:"online"`
	Offline int64 `json:"offline"`
}

type WindowCounts struct {
	Total  int64 `json:"total"`
	Failed int64 `json:"failed"`
}

type Signup struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	CreatedAt string `json:"created_at" doc:"RFC 3339, as the auth plugin stores it"`
}

type Overview struct {
	Users              int64        `json:"users"`
	PlatformAdmins     int64        `json:"platform_admins"`
	Teams              int64        `json:"teams"`
	Servers            ServerCounts `json:"servers"`
	Apps               int64        `json:"apps" doc:"Every app, imported ones included"`
	AppsImported       int64        `json:"apps_imported" doc:"Apps imported from what servers already run"`
	DeploymentsLast24h WindowCounts `json:"deployments_last_24h"`
	OperationsLast24h  WindowCounts `json:"operations_last_24h" doc:"failed counts failed and timed-out operations"`
	Databases          int64        `json:"databases" doc:"Data services (database servers), imported ones included"`
	DatabasesImported  int64        `json:"databases_imported" doc:"Data services imported from what servers already run"`
	RecentSignups      []Signup     `json:"recent_signups"`
}

func GetOverview(ctx context.Context, q *db.Queries) (Overview, error) {
	c, err := q.AdminOverviewCounts(ctx, onlineSeconds())
	if err != nil {
		return Overview{}, internal("count the platform")
	}
	o := Overview{Users: c.Users, PlatformAdmins: c.PlatformAdmins, Teams: c.Teams,
		Servers: ServerCounts{Total: c.ServersTotal, Online: c.ServersOnline, Offline: c.ServersTotal - c.ServersOnline},
		Apps:    c.Apps, Databases: c.Databases, AppsImported: c.AppsImported, DatabasesImported: c.DatabasesImported,
		DeploymentsLast24h: WindowCounts{Total: c.Deployments24h, Failed: c.DeploymentsFailed24h},
		OperationsLast24h:  WindowCounts{Total: c.Operations24h, Failed: c.OperationsFailed24h},
		RecentSignups:      []Signup{}}
	rows, err := q.RecentSignups(ctx)
	if err != nil {
		return Overview{}, internal("list recent sign-ups")
	}
	for _, r := range rows {
		o.RecentSignups = append(o.RecentSignups, Signup{ID: r.ID, Email: r.Email, CreatedAt: r.CreatedAt})
	}
	return o, nil
}

// ---- audit ---------------------------------------------------------------------------------

type AuditEntry struct {
	ID         string          `json:"id"`
	ActorID    string          `json:"actor_id"`
	ActorEmail string          `json:"actor_email" doc:"Empty if the account has since been deleted"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	Details    json.RawMessage `json:"details"`
	IP         string          `json:"ip"`
	CreatedAt  time.Time       `json:"created_at"`
}

type AuditPage struct {
	Items []AuditEntry `json:"items"`
	Total int64        `json:"total"`
}

func ListAudit(ctx context.Context, q *db.Queries, limit, offset int) (AuditPage, error) {
	lim, off := Page(limit, offset)
	rows, err := q.ListPlatformAudit(ctx, db.ListPlatformAuditParams{Lim: lim, Off: off})
	if err != nil {
		return AuditPage{}, internal("list the audit log")
	}
	total, err := q.CountPlatformAudit(ctx)
	if err != nil {
		return AuditPage{}, internal("count the audit log")
	}
	p := AuditPage{Items: []AuditEntry{}, Total: total}
	for _, r := range rows {
		p.Items = append(p.Items, AuditEntry{ID: r.ID, ActorID: r.ActorID, ActorEmail: r.ActorEmail, Action: r.Action,
			TargetType: r.TargetType, TargetID: r.TargetID, Details: json.RawMessage(r.Details), IP: r.Ip, CreatedAt: r.CreatedAt})
	}
	return p, nil
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
