package platform

import (
	"context"
	"net/http"
	"strings"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
)

// ErrTeamSuspended is what every team route and the team MCP answer for a suspended team.
var ErrTeamSuspended = &Error{Status: http.StatusLocked,
	Msg: "this team is suspended by the platform administrators — its servers keep running, but it cannot be managed until it is reinstated"}

type Team struct {
	ID          string     `json:"id"`
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	CreatedAt   time.Time  `json:"created_at"`
	Members     int64      `json:"members"`
	Servers     int64      `json:"servers"`
	Apps        int64      `json:"apps"`
	Suspended   bool       `json:"suspended"`
	SuspendedAt *time.Time `json:"suspended_at"`
}

type TeamPage struct {
	Items []Team `json:"items"`
	Total int64  `json:"total"`
}

func ListTeams(ctx context.Context, q *db.Queries, search string, limit, offset int) (TeamPage, error) {
	lim, off := Page(limit, offset)
	search = strings.TrimSpace(search)
	rows, err := q.AdminListTeams(ctx, db.AdminListTeamsParams{Q: search, Lim: lim, Off: off})
	if err != nil {
		return TeamPage{}, internal("list teams")
	}
	total, err := q.AdminCountTeams(ctx, search)
	if err != nil {
		return TeamPage{}, internal("count teams")
	}
	p := TeamPage{Items: []Team{}, Total: total}
	for _, r := range rows {
		p.Items = append(p.Items, Team{ID: r.ID, Slug: r.Slug, Name: r.Name, CreatedAt: r.CreatedAt, Members: r.Members,
			Servers: r.Servers, Apps: r.Apps, Suspended: r.SuspendedAt != nil, SuspendedAt: r.SuspendedAt})
	}
	return p, nil
}

type TeamMember struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

type TeamServer struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Provider   string     `json:"provider"`
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	Online     bool       `json:"online"`
	LastSeenAt *time.Time `json:"last_seen_at"`
}

type TeamApp struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ServerID   string `json:"server_id"`
	ServerName string `json:"server_name"`
}

type TeamCounts struct {
	Members   int64 `json:"members"`
	Servers   int64 `json:"servers"`
	Apps      int64 `json:"apps"`
	Databases int64 `json:"databases"`
}

type TeamDetail struct {
	Team    Team         `json:"team"`
	Members []TeamMember `json:"members"`
	Servers []TeamServer `json:"servers"`
	Apps    []TeamApp    `json:"apps"`
	Counts  TeamCounts   `json:"counts"`
}

// FindTeam resolves an id or a slug.
func FindTeam(ctx context.Context, q *db.Queries, ref string) (db.Team, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Team{}, ErrNotFound
	}
	if t, err := q.GetTeam(ctx, ref); err == nil {
		return t, nil
	}
	t, err := q.TeamBySlug(ctx, ref)
	if err != nil {
		return db.Team{}, ErrNotFound
	}
	return t, nil
}

func GetTeam(ctx context.Context, q *db.Queries, ref string) (TeamDetail, error) {
	t, err := FindTeam(ctx, q, ref)
	if err != nil {
		return TeamDetail{}, err
	}
	members, err := q.ListMembersOfTeam(ctx, t.ID)
	if err != nil {
		return TeamDetail{}, internal("list members")
	}
	servers, err := q.AdminTeamServers(ctx, t.ID)
	if err != nil {
		return TeamDetail{}, internal("list servers")
	}
	apps, err := q.AdminTeamApps(ctx, t.ID)
	if err != nil {
		return TeamDetail{}, internal("list apps")
	}
	dbs, err := q.AdminTeamDataServiceCount(ctx, t.ID)
	if err != nil {
		return TeamDetail{}, internal("count databases")
	}
	now := time.Now()
	d := TeamDetail{
		Team: Team{ID: t.ID, Slug: t.Slug, Name: t.Name, CreatedAt: t.CreatedAt, Members: int64(len(members)),
			Servers: int64(len(servers)), Apps: int64(len(apps)), Suspended: t.SuspendedAt != nil, SuspendedAt: t.SuspendedAt},
		Members: []TeamMember{}, Servers: []TeamServer{}, Apps: []TeamApp{},
		Counts: TeamCounts{Members: int64(len(members)), Servers: int64(len(servers)), Apps: int64(len(apps)), Databases: dbs},
	}
	for _, m := range members {
		d.Members = append(d.Members, TeamMember{UserID: m.UserID, Email: m.Email, Role: m.Role})
	}
	for _, s := range servers {
		d.Servers = append(d.Servers, TeamServer{ID: s.ID, Name: s.Name, Provider: s.Provider, Kind: s.Kind, Status: s.Status,
			Online: s.Status == "enrolled" && ops.Online(s.LastSeenAt, now), LastSeenAt: s.LastSeenAt})
	}
	for _, a := range apps {
		d.Apps = append(d.Apps, TeamApp{ID: a.ID, Name: a.Name, ServerID: a.ServerID, ServerName: a.ServerName})
	}
	return d, nil
}

// SuspendTeam suspends or reinstates a team. Suspension is enforced where a team is resolved
// (internal/api inTeam, the team MCP's authentication); agents, the dispatcher and the deploy
// worker never consult it, so the team's servers and apps keep running.
func SuspendTeam(ctx context.Context, q *db.Queries, actorID, ref string, suspended bool) (TeamDetail, error) {
	t, err := FindTeam(ctx, q, ref)
	if err != nil {
		return TeamDetail{}, err
	}
	was := t.SuspendedAt != nil
	if was != suspended {
		if _, err := q.SetTeamSuspended(ctx, db.SetTeamSuspendedParams{ID: t.ID, Suspended: suspended}); err != nil {
			return TeamDetail{}, internal("update the team")
		}
		action := "team.reinstated"
		if suspended {
			action = "team.suspended"
		}
		if err := Record(ctx, q, actorID, action, "team", t.ID, map[string]any{"slug": t.Slug, "name": t.Name}); err != nil {
			return TeamDetail{}, internal("write the audit entry")
		}
	}
	return GetTeam(ctx, q, t.ID)
}

// ---- servers and operations across teams ---------------------------------------------------

type Server struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	TeamID       string     `json:"team_id"`
	TeamSlug     string     `json:"team_slug"`
	TeamName     string     `json:"team_name"`
	Provider     string     `json:"provider"`
	Kind         string     `json:"kind"`
	Status       string     `json:"status" doc:"Lifecycle: pending, provisioning, enrolled, failed"`
	Online       bool       `json:"online" doc:"Enrolled and heartbeated within the online window"`
	IP           *string    `json:"ip"`
	Hostname     *string    `json:"hostname"`
	AgentVersion *string    `json:"agent_version"`
	LastSeenAt   *time.Time `json:"last_seen_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

type ServerPage struct {
	Items []Server `json:"items"`
	Total int64    `json:"total"`
}

// ServerStatuses are the accepted status filters: a lifecycle status, or online/offline
// (enrolled servers by heartbeat).
var ServerStatuses = []string{"pending", "provisioning", "enrolled", "failed", "online", "offline"}

func ListServers(ctx context.Context, q *db.Queries, search, status string, limit, offset int) (ServerPage, error) {
	lim, off := Page(limit, offset)
	search, status = strings.TrimSpace(search), strings.TrimSpace(status)
	if status != "" && !contains(ServerStatuses, status) {
		return ServerPage{}, refuse(http.StatusUnprocessableEntity, "status must be one of %s", strings.Join(ServerStatuses, ", "))
	}
	rows, err := q.AdminListServers(ctx, db.AdminListServersParams{Q: search, Status: status, OnlineSeconds: onlineSeconds(), Lim: lim, Off: off})
	if err != nil {
		return ServerPage{}, internal("list servers")
	}
	total, err := q.AdminCountServers(ctx, db.AdminCountServersParams{Q: search, Status: status, OnlineSeconds: onlineSeconds()})
	if err != nil {
		return ServerPage{}, internal("count servers")
	}
	now := time.Now()
	p := ServerPage{Items: []Server{}, Total: total}
	for _, r := range rows {
		p.Items = append(p.Items, Server{ID: r.ID, Name: r.Name, TeamID: r.TeamID, TeamSlug: r.TeamSlug, TeamName: r.TeamName,
			Provider: r.Provider, Kind: r.Kind, Status: r.Status, Online: r.Status == "enrolled" && ops.Online(r.LastSeenAt, now),
			IP: r.IpAddress, Hostname: r.Hostname, AgentVersion: r.AgentVersion, LastSeenAt: r.LastSeenAt, CreatedAt: r.CreatedAt})
	}
	return p, nil
}

type Operation struct {
	ID         string     `json:"id"`
	TeamID     string     `json:"team_id"`
	TeamSlug   string     `json:"team_slug"`
	TeamName   string     `json:"team_name"`
	ServerID   string     `json:"server_id"`
	ServerName string     `json:"server_name"`
	Kind       string     `json:"kind"`
	Executor   string     `json:"executor"`
	Status     string     `json:"status"`
	ExitCode   *int64     `json:"exit_code"`
	Error      string     `json:"error" doc:"First 300 characters of the error, empty if none"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

type OperationPage struct {
	Items []Operation `json:"items"`
	Total int64       `json:"total"`
}

var OperationStatuses = []string{"pending", "sent", "running", "ok", "failed", "timeout"}

// ListOperations never reads params or result: params can carry values re-sealed to an agent.
func ListOperations(ctx context.Context, q *db.Queries, status, kind string, limit, offset int) (OperationPage, error) {
	lim, off := Page(limit, offset)
	status, kind = strings.TrimSpace(status), strings.TrimSpace(kind)
	if status != "" && !contains(OperationStatuses, status) {
		return OperationPage{}, refuse(http.StatusUnprocessableEntity, "status must be one of %s", strings.Join(OperationStatuses, ", "))
	}
	rows, err := q.AdminListOperations(ctx, db.AdminListOperationsParams{Status: status, Kind: kind, Lim: lim, Off: off})
	if err != nil {
		return OperationPage{}, internal("list operations")
	}
	total, err := q.AdminCountOperations(ctx, db.AdminCountOperationsParams{Status: status, Kind: kind})
	if err != nil {
		return OperationPage{}, internal("count operations")
	}
	p := OperationPage{Items: []Operation{}, Total: total}
	for _, r := range rows {
		p.Items = append(p.Items, Operation{ID: r.ID, TeamID: r.TeamID, TeamSlug: r.TeamSlug, TeamName: r.TeamName,
			ServerID: r.ServerID, ServerName: r.ServerName, Kind: r.Kind, Executor: r.Executor, Status: r.Status,
			ExitCode: r.ExitCode, Error: r.ErrorSummary, CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt})
	}
	return p, nil
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
