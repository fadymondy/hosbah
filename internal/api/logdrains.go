package api

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// H8: team log drains (syslog, http, loki). Read: any member. Write and test: owner or admin
// (AbilityManageServers, like connecting a server or an sftp user), since a drain can carry secrets
// and points Hosbah's containers' logs at an outside collector.

// LogDrainPath addresses one log drain of a team.
type LogDrainPath struct {
	TeamPath
	Drain string `path:"drain" maxLength:"64"`
}

// LogDrainSyslogIn/HTTPIn/LokiIn are the per-kind settings a caller gives.
type LogDrainSyslogIn struct {
	Transport string `json:"transport" enum:"tcp+tls,udp"`
	Host      string `json:"host" maxLength:"253"`
	Port      int    `json:"port" minimum:"1" maximum:"65535"`
}

type LogDrainHTTPIn struct {
	URL        string `json:"url" maxLength:"500"`
	AuthHeader string `json:"auth_header,omitempty" maxLength:"64" doc:"e.g. Authorization; the header this drain's auth_value is sent as."`
}

type LogDrainLokiIn struct {
	URL        string            `json:"url" maxLength:"500"`
	AuthHeader string            `json:"auth_header,omitempty" maxLength:"64"`
	Labels     map[string]string `json:"labels,omitempty" maxProperties:"20"`
}

// LogDrainWrite is what can be given when creating or changing a drain.
type LogDrainWrite struct {
	Name      string            `json:"name,omitempty" maxLength:"80"`
	AppID     *string           `json:"app_id,omitempty" doc:"scope to one app; \"\" = every app of the team"`
	Internal  *bool             `json:"internal,omitempty" doc:"allow a private, loopback or link-local http/loki address (an owner opts a LAN collector in)"`
	Enabled   *bool             `json:"enabled,omitempty"`
	Syslog    *LogDrainSyslogIn `json:"syslog,omitempty"`
	HTTP      *LogDrainHTTPIn   `json:"http,omitempty"`
	Loki      *LogDrainLokiIn   `json:"loki,omitempty"`
	AuthValue *string           `json:"auth_value,omitempty" doc:"the auth header's plaintext value, sealed at rest; omit to leave unchanged, \"\" to clear it"`
}

// LogDrainOut is a drain as the API shows it. Never the sealed auth value itself.
type LogDrainOut struct {
	ID            string            `json:"id"`
	TeamID        string            `json:"team_id"`
	AppID         string            `json:"app_id" doc:"\"\" = every app of the team"`
	Name          string            `json:"name"`
	Kind          string            `json:"kind" enum:"syslog,http,loki"`
	Internal      bool              `json:"internal"`
	Enabled       bool              `json:"enabled"`
	HasAuthValue  bool              `json:"has_auth_value"`
	Syslog        *LogDrainSyslogIn `json:"syslog,omitempty"`
	HTTP          *LogDrainHTTPIn   `json:"http,omitempty"`
	Loki          *LogDrainLokiIn   `json:"loki,omitempty"`
	Shipped       int64             `json:"shipped"`
	Dropped       int64             `json:"dropped"`
	LastSuccessAt *time.Time        `json:"last_success_at"`
	LastErrorAt   *time.Time        `json:"last_error_at"`
	LastError     string            `json:"last_error,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

func (d *deps) registerLogDrains(api huma.API) {
	tags := []string{"Log drains"}
	base := "/api/teams/{team}/log-drains"

	type logDrainListOut struct {
		Body struct {
			LogDrains []LogDrainOut `json:"log_drains"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-log-drains", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The team's log drains. Any member.",
	}, func(ctx context.Context, in *TeamPath) (*logDrainListOut, error) {
		out := &logDrainListOut{}
		out.Body.LogDrains = []LogDrainOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := d.deploys().ListLogDrains(ctx, q, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list log drains")
			}
			for _, r := range rows {
				out.Body.LogDrains = append(out.Body.LogDrains, toLogDrainOut(r))
			}
			return nil
		})
		return out, err
	})

	type logDrainOneOut struct{ Body LogDrainOut }
	huma.Register(api, huma.Operation{OperationID: "create-log-drain", Method: http.MethodPost, Path: base,
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Add a log drain: syslog (tcp+tls or udp), http (NDJSON batches) or loki (its push API), scoped to one app or " +
			"every app. An http or loki url must be https unless internal is set. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body struct {
			Kind string `json:"kind" enum:"syslog,http,loki"`
			LogDrainWrite
		}
	}) (*logDrainOneOut, error) {
		out := &logDrainOneOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			b := in.Body
			row, err := d.deploys().CreateLogDrain(ctx, q, m.TeamID, webActor(id), logDrainInputOf(b.Kind, b.LogDrainWrite))
			if err != nil {
				return actionErr(err, "could not create the log drain")
			}
			out.Body = toLogDrainOut(row)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "get-log-drain", Method: http.MethodGet, Path: base + "/{drain}", Tags: tags,
		Summary: "One log drain. Any member.",
	}, func(ctx context.Context, in *LogDrainPath) (*logDrainOneOut, error) {
		out := &logDrainOneOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			row, err := d.deploys().LogDrainByID(ctx, q, m.TeamID, in.Drain)
			if err != nil {
				return errNotFound
			}
			out.Body = toLogDrainOut(row)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-log-drain", Method: http.MethodPatch, Path: base + "/{drain}", Tags: tags,
		Summary: "Change a drain's settings (never its kind); re-applied at once on every server it covers. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		LogDrainPath
		Body LogDrainWrite
	}) (*logDrainOneOut, error) {
		out := &logDrainOneOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			row, err := d.deploys().LogDrainByID(ctx, q, m.TeamID, in.Drain)
			if err != nil {
				return errNotFound
			}
			updated, err := d.deploys().UpdateLogDrain(ctx, q, m.TeamID, webActor(id), row, logDrainInputOf(row.Kind, in.Body))
			if err != nil {
				return actionErr(err, "could not update the log drain")
			}
			out.Body = toLogDrainOut(updated)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-log-drain", Method: http.MethodDelete, Path: base + "/{drain}",
		DefaultStatus: http.StatusNoContent, Tags: tags, Summary: "Remove a log drain everywhere it runs. Owner or admin. Audited.",
	}, func(ctx context.Context, in *LogDrainPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			row, err := d.deploys().LogDrainByID(ctx, q, m.TeamID, in.Drain)
			if err != nil {
				return errNotFound
			}
			return actionErr(d.deploys().DeleteLogDrain(ctx, q, m.TeamID, webActor(id), row), "could not delete the log drain")
		})
		return nil, err
	})

	type logDrainTestOut struct{ Body agentproto.DrainTestResult }
	huma.Register(api, huma.Operation{OperationID: "test-log-drain", Method: http.MethodPost, Path: base + "/{drain}/test", Tags: tags,
		Summary: "Ship one synthetic line through the drain now and report whether the sink accepted it (waits up to 30s). Owner or admin.",
	}, func(ctx context.Context, in *LogDrainPath) (*logDrainTestOut, error) {
		out := &logDrainTestOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			row, err := d.deploys().LogDrainByID(ctx, q, m.TeamID, in.Drain)
			if err != nil {
				return errNotFound
			}
			res, err := d.deploys().TestLogDrain(ctx, q, m.TeamID, row)
			if err != nil {
				return actionErr(err, "could not test the log drain")
			}
			out.Body = res
			return nil
		})
		return out, err
	})
}

func logDrainInputOf(kind string, b LogDrainWrite) deploy.LogDrainInput {
	in := deploy.LogDrainInput{Name: b.Name, Kind: kind, AuthValue: b.AuthValue, Enabled: b.Enabled}
	if b.AppID != nil {
		in.AppID = *b.AppID
		in.AppIDSet = true
	}
	if b.Internal != nil {
		in.Internal = *b.Internal
	}
	if b.Syslog != nil {
		in.Syslog = &agentproto.SyslogSink{Transport: b.Syslog.Transport, Host: b.Syslog.Host, Port: b.Syslog.Port}
	}
	if b.HTTP != nil {
		in.HTTP = &deploy.LogDrainHTTPConfig{URL: b.HTTP.URL, AuthHeader: b.HTTP.AuthHeader}
	}
	if b.Loki != nil {
		in.Loki = &deploy.LogDrainLokiConfig{URL: b.Loki.URL, AuthHeader: b.Loki.AuthHeader, Labels: b.Loki.Labels}
	}
	return in
}

func toLogDrainOut(r db.LogDrain) LogDrainOut {
	cfg := deploy.DecodeLogDrainConfig(r.ConfigJson)
	out := LogDrainOut{ID: r.ID, TeamID: r.TeamID, AppID: r.AppID, Name: r.Name, Kind: r.Kind, Internal: r.Internal, Enabled: r.Enabled,
		HasAuthValue: r.AuthSealed != "", Shipped: r.Shipped, Dropped: r.Dropped, LastSuccessAt: r.LastSuccessAt, LastErrorAt: r.LastErrorAt,
		LastError: r.LastError, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if cfg.Syslog != nil {
		out.Syslog = &LogDrainSyslogIn{Transport: cfg.Syslog.Transport, Host: cfg.Syslog.Host, Port: cfg.Syslog.Port}
	}
	if cfg.HTTP != nil {
		out.HTTP = &LogDrainHTTPIn{URL: cfg.HTTP.URL, AuthHeader: cfg.HTTP.AuthHeader}
	}
	if cfg.Loki != nil {
		out.Loki = &LogDrainLokiIn{URL: cfg.Loki.URL, AuthHeader: cfg.Loki.AuthHeader, Labels: cfg.Loki.Labels}
	}
	return out
}
