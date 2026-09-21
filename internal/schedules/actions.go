package schedules

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Schedule actions — one implementation for the REST API and the user MCP. Like internal/monitors',
every function runs in the CALLER's team-scoped transaction (q) after the caller checked the
member's role, names team_id in every statement and writes the audit entry on the same transaction.
*/

// Triggers, statuses and limits.
const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"

	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusTimeout   = "timeout"
	StatusSkipped   = "skipped"

	OverlapSkip = "skip"

	// MaxPerApp bounds an app's schedules.
	MaxPerApp = 30
	// KeepRuns is how many runs of a schedule are kept (pruned when one is added).
	KeepRuns = 200
	// MaxOutput is how much of a run's output is kept: its tail.
	MaxOutput = 64 << 10
	// MaxCommand bounds the command line.
	MaxCommand = 1000

	MinTimeout     = 10
	MaxTimeout     = 3600
	DefaultTimeout = 300
)

func invalid(msg string) error  { return &deploy.ActionError{Status: 422, Msg: msg} }
func conflict(msg string) error { return &deploy.ActionError{Status: 409, Msg: msg} }
func notFound(what string) error {
	return &deploy.ActionError{Status: 404, Msg: what + " not found"}
}

// refuseImported: a schedule runs inside a release Hosbah deployed. An imported app has none.
func refuseImported(a db.App) error {
	if deploy.IsImported(a) {
		return invalid("schedules run inside a release Hosbah deployed — " + a.Name + " is an imported app and has none")
	}
	return nil
}

// LastRun is the newest run of a schedule, in short.
type LastRun struct {
	ID         string    `json:"id"`
	Status     string    `json:"status" enum:"queued,running,succeeded,failed,timeout,skipped"`
	StartedAt  time.Time `json:"started_at"`
	DurationMs *int64    `json:"duration_ms"`
}

// Schedule is a schedule as the API shows it.
type Schedule struct {
	ID             string      `json:"id"`
	AppID          string      `json:"app_id"`
	Name           string      `json:"name"`
	Cron           string      `json:"cron" doc:"5 fields: minute hour day-of-month month day-of-week."`
	Timezone       string      `json:"timezone" doc:"The IANA zone the expression is read in."`
	Command        string      `json:"command" doc:"A shell line, run as sh -lc inside the live release's web container."`
	TimeoutSeconds int         `json:"timeout_seconds"`
	Enabled        bool        `json:"enabled"`
	Overlap        string      `json:"overlap" enum:"skip" doc:"skip: a fire while the previous run is still going is recorded as skipped."`
	NextRunAt      *time.Time  `json:"next_run_at" doc:"When it fires next; null while disabled."`
	LastRunAt      *time.Time  `json:"last_run_at"`
	LastStatus     *string     `json:"last_status" enum:"queued,running,succeeded,failed,timeout,skipped"`
	LastRun        *LastRun    `json:"last_run"`
	NextRuns       []time.Time `json:"next_runs" doc:"The next 3 fire times of the expression (computed; also shown while disabled)."`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

func view(s db.Schedule, last *db.ScheduleRun, now time.Time) Schedule {
	out := Schedule{ID: s.ID, AppID: s.AppID, Name: s.Name, Cron: s.Cron, Timezone: s.Timezone, Command: s.Command,
		TimeoutSeconds: int(s.TimeoutSeconds), Enabled: s.Enabled, Overlap: s.Overlap, NextRunAt: s.NextRunAt, LastRunAt: s.LastRunAt,
		LastStatus: s.LastStatus, NextRuns: []time.Time{}, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
	if last != nil {
		out.LastRun = &LastRun{ID: last.ID, Status: last.Status, StartedAt: last.StartedAt, DurationMs: last.DurationMs}
	}
	if spec, err := ParseSpec(s.Cron, s.Timezone); err == nil {
		out.NextRuns = spec.NextN(now, 3)
	}
	return out
}

// Input is a schedule's settings as a caller gives them. nil = not given (create: the default;
// update: unchanged).
type Input struct {
	Name           *string
	Cron           *string
	Timezone       *string
	Command        *string
	TimeoutSeconds *int
	Enabled        *bool
}

type settings struct {
	Name, Cron, Timezone, Command string
	Timeout                       int
	Enabled                       bool
}

func (in Input) over(s settings) settings {
	if in.Name != nil {
		s.Name = *in.Name
	}
	if in.Cron != nil {
		s.Cron = *in.Cron
	}
	if in.Timezone != nil {
		s.Timezone = *in.Timezone
	}
	if in.Command != nil {
		s.Command = *in.Command
	}
	if in.TimeoutSeconds != nil {
		s.Timeout = *in.TimeoutSeconds
	}
	if in.Enabled != nil {
		s.Enabled = *in.Enabled
	}
	return s
}

// CheckCommand is the rule of a schedule's command: 1 to 1000 characters of text on one line.
func CheckCommand(c string) (string, error) {
	c = strings.TrimSpace(c)
	if n := utf8.RuneCountInString(c); n < 1 || n > MaxCommand || !utf8.ValidString(c) {
		return "", fmt.Errorf("command must be 1 to %d characters", MaxCommand)
	}
	if strings.IndexFunc(c, func(r rune) bool { return r != '\t' && unicode.IsControl(r) }) >= 0 {
		return "", errors.New("command must be one line, without control characters")
	}
	return c, nil
}

func validate(s settings) (settings, Spec, error) {
	s.Name = strings.TrimSpace(s.Name)
	if n := utf8.RuneCountInString(s.Name); n < 1 || n > 80 {
		return s, Spec{}, invalid("name must be 1 to 80 characters")
	}
	if strings.IndexFunc(s.Name, unicode.IsControl) >= 0 {
		return s, Spec{}, invalid("name must not contain control characters")
	}
	spec, err := ParseSpec(s.Cron, s.Timezone)
	if err != nil {
		return s, Spec{}, invalid(err.Error())
	}
	s.Cron, s.Timezone = spec.Cron, spec.Timezone
	if s.Command, err = CheckCommand(s.Command); err != nil {
		return s, Spec{}, invalid(err.Error())
	}
	if s.Timeout == 0 {
		s.Timeout = DefaultTimeout
	}
	if s.Timeout < MinTimeout || s.Timeout > MaxTimeout {
		return s, Spec{}, invalid(fmt.Sprintf("timeout_seconds must be between %d and %d", MinTimeout, MaxTimeout))
	}
	return s, spec, nil
}

// Preview validates an expression without saving anything: its next n fire times after now.
func Preview(cron, timezone string, now time.Time, n int) ([]time.Time, error) {
	spec, err := ParseSpec(cron, timezone)
	if err != nil {
		return nil, invalid(err.Error())
	}
	return spec.NextN(now, n), nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "schedules_app_name_key")
}

const nameTaken = "the app already has a schedule with that name"

// List is the app's schedules, each with its newest run.
func List(ctx context.Context, q *db.Queries, teamID string, a db.App, now time.Time) ([]Schedule, error) {
	rows, err := q.ListSchedulesOfApp(ctx, db.ListSchedulesOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	runs, err := q.LatestRunsOfApp(ctx, db.LatestRunsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	by := map[string]*db.ScheduleRun{}
	for i := range runs {
		by[runs[i].ScheduleID] = &runs[i]
	}
	out := make([]Schedule, 0, len(rows))
	for _, s := range rows {
		out = append(out, view(s, by[s.ID], now))
	}
	return out, nil
}

// Find resolves ref (id or name) to one of the app's schedules.
func Find(ctx context.Context, q *db.Queries, teamID string, a db.App, ref string) (db.Schedule, error) {
	s, err := q.ScheduleOfApp(ctx, db.ScheduleOfAppParams{AppID: a.ID, TeamID: teamID, Ref: strings.TrimSpace(ref)})
	if err != nil {
		return db.Schedule{}, notFound("schedule")
	}
	return s, nil
}

// View is s with its newest run.
func View(ctx context.Context, q *db.Queries, s db.Schedule, now time.Time) (Schedule, error) {
	last, err := q.LatestRunOfSchedule(ctx, db.LatestRunOfScheduleParams{ScheduleID: s.ID, TeamID: s.TeamID})
	if errors.Is(err, sql.ErrNoRows) {
		return view(s, nil, now), nil
	}
	if err != nil {
		return Schedule{}, err
	}
	return view(s, &last, now), nil
}

// Create adds a schedule to the app. Audited.
func Create(ctx context.Context, q *db.Queries, teamID string, actor deploy.Actor, a db.App, in Input, now time.Time) (Schedule, error) {
	if err := refuseImported(a); err != nil {
		return Schedule{}, err
	}
	s, spec, err := validate(in.over(settings{Timezone: "UTC", Timeout: DefaultTimeout, Enabled: true}))
	if err != nil {
		return Schedule{}, err
	}
	if n, err := q.CountSchedulesOfApp(ctx, db.CountSchedulesOfAppParams{AppID: a.ID, TeamID: teamID}); err != nil {
		return Schedule{}, err
	} else if n >= MaxPerApp {
		return Schedule{}, conflict(fmt.Sprintf("an app holds at most %d schedules", MaxPerApp))
	}
	// A savepoint-free uniqueness check: the unique index is the authority, this is the message.
	if _, err := q.ScheduleOfApp(ctx, db.ScheduleOfAppParams{AppID: a.ID, TeamID: teamID, Ref: s.Name}); err == nil {
		return Schedule{}, conflict(nameTaken)
	}
	row, err := q.InsertSchedule(ctx, db.InsertScheduleParams{TeamID: teamID, AppID: a.ID, Name: s.Name, Cron: s.Cron, Timezone: s.Timezone,
		Command: s.Command, TimeoutSeconds: int64(s.Timeout), Enabled: s.Enabled, NextRunAt: nextOf(spec, s.Enabled, now)})
	if isUnique(err) {
		return Schedule{}, conflict(nameTaken)
	}
	if err != nil {
		return Schedule{}, err
	}
	return view(row, nil, now), tenancy.Record(ctx, q, teamID, actor.UserID, "schedule.created", "app", a.ID,
		auditOf(actor, map[string]any{"schedule": row.ID, "name": row.Name, "cron": row.Cron, "timezone": row.Timezone}))
}

func nextOf(spec Spec, enabled bool, now time.Time) *time.Time {
	if !enabled {
		return nil
	}
	n := spec.Next(now)
	if n.IsZero() {
		return nil
	}
	return &n
}

// Update changes a schedule's settings. A changed expression or zone, or switching it back on,
// recomputes next_run_at from now; switching it off clears it. Audited.
func Update(ctx context.Context, q *db.Queries, teamID string, actor deploy.Actor, a db.App, cur db.Schedule, in Input, now time.Time) (Schedule, error) {
	if err := refuseImported(a); err != nil {
		return Schedule{}, err
	}
	s, spec, err := validate(in.over(settings{Name: cur.Name, Cron: cur.Cron, Timezone: cur.Timezone, Command: cur.Command,
		Timeout: int(cur.TimeoutSeconds), Enabled: cur.Enabled}))
	if err != nil {
		return Schedule{}, err
	}
	if s.Name != cur.Name {
		if other, err := q.ScheduleOfApp(ctx, db.ScheduleOfAppParams{AppID: a.ID, TeamID: teamID, Ref: s.Name}); err == nil && other.ID != cur.ID {
			return Schedule{}, conflict(nameTaken)
		}
	}
	next := cur.NextRunAt
	if !s.Enabled || !cur.Enabled || next == nil || s.Cron != cur.Cron || s.Timezone != cur.Timezone {
		next = nextOf(spec, s.Enabled, now)
	}
	up, err := q.UpdateScheduleInTeam(ctx, db.UpdateScheduleInTeamParams{ID: cur.ID, TeamID: teamID, Name: s.Name, Cron: s.Cron,
		Timezone: s.Timezone, Command: s.Command, TimeoutSeconds: int64(s.Timeout), Enabled: s.Enabled, NextRunAt: next})
	if isUnique(err) {
		return Schedule{}, conflict(nameTaken)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Schedule{}, notFound("schedule")
	}
	if err != nil {
		return Schedule{}, err
	}
	out, err := View(ctx, q, up, now)
	if err != nil {
		return Schedule{}, err
	}
	return out, tenancy.Record(ctx, q, teamID, actor.UserID, "schedule.updated", "app", a.ID,
		auditOf(actor, map[string]any{"schedule": up.ID, "name": up.Name, "cron": up.Cron, "enabled": up.Enabled,
			"command_changed": up.Command != cur.Command}))
}

// Delete removes a schedule and its runs. Audited.
func Delete(ctx context.Context, q *db.Queries, teamID string, actor deploy.Actor, a db.App, s db.Schedule) error {
	n, err := q.DeleteScheduleInTeam(ctx, db.DeleteScheduleInTeamParams{ID: s.ID, TeamID: teamID})
	if err != nil {
		return err
	}
	if n == 0 {
		return notFound("schedule")
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "schedule.deleted", "app", a.ID,
		auditOf(actor, map[string]any{"schedule": s.ID, "name": s.Name}))
}

func auditOf(a deploy.Actor, extra map[string]any) map[string]any {
	if a.Via != "" && a.Via != "web" {
		extra["via"] = a.Via
	}
	if a.TokenID != "" {
		extra["mcp_token_id"] = a.TokenID
	}
	return extra
}

// ---- runs ------------------------------------------------------------------------------------------

// Run is one firing of a schedule. It never carries the output: see RunDetail.
type Run struct {
	ID          string     `json:"id"`
	ScheduleID  string     `json:"schedule_id"`
	AppID       string     `json:"app_id"`
	OperationID *string    `json:"operation_id" doc:"Its app.exec operation; null for a skipped run or one that failed before anything was queued."`
	Trigger     string     `json:"trigger" enum:"schedule,manual"`
	Status      string     `json:"status" enum:"queued,running,succeeded,failed,timeout,skipped"`
	ExitCode    *int64     `json:"exit_code" doc:"The command's exit code (124 when it was killed at its timeout); null when it never ran."`
	StartedAt   time.Time  `json:"started_at" doc:"When it fired."`
	FinishedAt  *time.Time `json:"finished_at"`
	DurationMs  *int64     `json:"duration_ms"`
	Error       string     `json:"error"`
}

// RunDetail is a run with what its command printed.
type RunDetail struct {
	Run
	Output string `json:"output" doc:"The tail (64 KiB) of stdout and stderr. Stored sealed; opened for whoever may read the app's runtime logs."`
}

// RunFeed is a page of a schedule's runs.
type RunFeed struct {
	Runs       []Run   `json:"runs"`
	NextCursor *string `json:"next_cursor" doc:"Pass as before= for the next page; null on the last page."`
}

// RunView is r without its output.
func RunView(r db.ScheduleRun) Run {
	return Run{ID: r.ID, ScheduleID: r.ScheduleID, AppID: r.AppID, OperationID: r.OperationID, Trigger: r.Trigger, Status: r.Status,
		ExitCode: r.ExitCode, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, DurationMs: r.DurationMs, Error: r.Error}
}

func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", deploy.ErrBadCursor
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	at, err := time.Parse(time.RFC3339Nano, ts)
	if !ok || err != nil || id == "" {
		return time.Time{}, "", deploy.ErrBadCursor
	}
	return at, id, nil
}

// Runs is a schedule's runs, newest first (team scope). A bad cursor is deploy.ErrBadCursor.
func Runs(ctx context.Context, q *db.Queries, s db.Schedule, limit int, before string) (RunFeed, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	p := db.ScheduleRunFeedParams{ScheduleID: s.ID, TeamID: s.TeamID, LimitRows: int32(limit + 1)} //nolint:gosec // bounded above
	if before != "" {
		at, id, err := decodeCursor(before)
		if err != nil {
			return RunFeed{}, err
		}
		p.BeforeAt, p.BeforeID = &at, id
	}
	rows, err := q.ScheduleRunFeed(ctx, p)
	if err != nil {
		return RunFeed{}, err
	}
	out := RunFeed{Runs: []Run{}}
	for i, r := range rows {
		if i == limit {
			last := rows[limit-1]
			c := encodeCursor(last.StartedAt, last.ID)
			out.NextCursor = &c
			break
		}
		out.Runs = append(out.Runs, RunView(r))
	}
	return out, nil
}

// FindRun is one run of a schedule.
func FindRun(ctx context.Context, q *db.Queries, s db.Schedule, id string) (db.ScheduleRun, error) {
	r, err := q.ScheduleRunOfSchedule(ctx, db.ScheduleRunOfScheduleParams{ID: strings.TrimSpace(id), ScheduleID: s.ID, TeamID: s.TeamID})
	if err != nil {
		return db.ScheduleRun{}, notFound("run")
	}
	return r, nil
}
