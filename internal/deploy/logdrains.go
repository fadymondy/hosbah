package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// H8: team log drains. A drain's non-secret settings live in log_drains.config_json; its auth
// header value (when it has one), if any, is sealed at rest with the team DEK in auth_sealed. The
// control plane never ships a log line itself — it pushes the drain's configuration, with the auth
// value re-sealed to the target server's own agent key, to every server that runs a matching app
// (log.drains.set, one op per server). See internal/agentproto/logdrains.go and
// internal/agent/drains.go for the wire format and the agent side.

// logDrainTimeout bounds a synchronous test push.
var logDrainTimeout = 30 * time.Second

// LogDrainConfig is the shape of log_drains.config_json: every setting except the auth header's
// value (which is sealed separately, at rest with the team DEK, and re-sealed to each target
// agent's key on push).
type LogDrainConfig struct {
	Internal bool                   `json:"internal,omitempty"`
	Syslog   *agentproto.SyslogSink `json:"syslog,omitempty"`
	HTTP     *LogDrainHTTPConfig    `json:"http,omitempty"`
	Loki     *LogDrainLokiConfig    `json:"loki,omitempty"`
}

type LogDrainHTTPConfig struct {
	URL        string `json:"url"`
	AuthHeader string `json:"auth_header,omitempty"`
}

type LogDrainLokiConfig struct {
	URL        string            `json:"url"`
	AuthHeader string            `json:"auth_header,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

// LogDrainInput creates or updates a drain. AuthValue is the auth header's plaintext value; nil
// leaves it unchanged (update) or absent (create); "" clears it.
type LogDrainInput struct {
	Name      string
	AppID     string // "" = every app of the team
	AppIDSet  bool   // update only: AppID is meant to change (including clearing it to team-wide); unset leaves the drain's current scope alone
	Kind      string // syslog | http | loki
	Internal  bool
	Syslog    *agentproto.SyslogSink
	HTTP      *LogDrainHTTPConfig
	Loki      *LogDrainLokiConfig
	AuthValue *string
	Enabled   *bool
}

// LogDrainAuthContext binds the at-rest seal (the team DEK) to the drain row whose auth header
// value it holds — distinct from agentproto.DrainSecretContext, which binds the box re-sealed to
// each target agent's own key inside the signed operation.
func LogDrainAuthContext(id string) string { return "log-drain-auth:" + id }

func (in LogDrainInput) toConfig() LogDrainConfig {
	return LogDrainConfig{Internal: in.Internal, Syslog: in.Syslog, HTTP: in.HTTP, Loki: in.Loki}
}

func DecodeLogDrainConfig(raw string) LogDrainConfig {
	var c LogDrainConfig
	_ = json.Unmarshal([]byte(raw), &c)
	return c
}

// ListLogDrains lists a team's drains, newest first.
func (s *Service) ListLogDrains(ctx context.Context, q *db.Queries, teamID string) ([]db.LogDrain, error) {
	return q.ListLogDrainsOfTeam(ctx, teamID)
}

// LogDrainByID resolves one of a team's drains.
func (s *Service) LogDrainByID(ctx context.Context, q *db.Queries, teamID, id string) (db.LogDrain, error) {
	row, err := q.LogDrainInTeam(ctx, db.LogDrainInTeamParams{ID: id, TeamID: teamID})
	if err != nil {
		return db.LogDrain{}, notFound("log drain")
	}
	return row, nil
}

// CreateLogDrain adds a drain and pushes it to every server it now covers.
func (s *Service) CreateLogDrain(ctx context.Context, q *db.Queries, teamID string, actor Actor, in LogDrainInput) (db.LogDrain, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return db.LogDrain{}, invalid("name is required")
	}
	if in.AppID != "" {
		if _, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: in.AppID, TeamID: teamID}); err != nil {
			return db.LogDrain{}, notFound("app")
		}
	}
	cfg := agentproto.DrainConfig{DrainID: "placeholder", TeamID: teamID, AppID: in.AppID, Kind: in.Kind, Internal: in.Internal,
		Syslog: in.Syslog, HTTP: httpSinkOf(in.HTTP), Loki: lokiSinkOf(in.Loki)}
	if err := validateLogDrainKind(cfg); err != nil {
		return db.LogDrain{}, err
	}
	configJSON, _ := json.Marshal(in.toConfig())
	row, err := q.InsertLogDrain(ctx, db.InsertLogDrainParams{TeamID: teamID, AppID: in.AppID, Name: name, Kind: in.Kind,
		ConfigJson: string(configJSON), AuthSealed: "", Internal: in.Internal, Enabled: boolOr(in.Enabled, true), CreatedBy: actor.UserID})
	if err != nil {
		if strings.Contains(err.Error(), "log_drains_kind_check") {
			return db.LogDrain{}, invalid("kind is syslog, http or loki")
		}
		return db.LogDrain{}, err
	}
	if in.AuthValue != nil && *in.AuthValue != "" {
		row, err = s.sealLogDrainAuth(ctx, q, teamID, row, *in.AuthValue)
		if err != nil {
			return db.LogDrain{}, err
		}
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "log_drain.created", "log_drain", row.ID,
		actor.audit(map[string]any{"name": name, "kind": in.Kind, "app_id": in.AppID})); err != nil {
		return db.LogDrain{}, err
	}
	if row.Enabled {
		s.pushLogDrain(context.WithoutCancel(ctx), teamID, actor, row)
	}
	return row, nil
}

// UpdateLogDrain changes a drain's settings (its kind never changes) and re-pushes it.
func (s *Service) UpdateLogDrain(ctx context.Context, q *db.Queries, teamID string, actor Actor, row db.LogDrain, in LogDrainInput) (db.LogDrain, error) {
	name := strings.TrimSpace(orDefault(in.Name, row.Name))
	appID := row.AppID
	if in.AppIDSet {
		appID = in.AppID
	}
	if appID != "" {
		if _, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: appID, TeamID: teamID}); err != nil {
			return db.LogDrain{}, notFound("app")
		}
	}
	internal := in.Internal
	cur := DecodeLogDrainConfig(row.ConfigJson)
	syslog, httpCfg, lokiCfg := cur.Syslog, cur.HTTP, cur.Loki
	if in.Syslog != nil {
		syslog = in.Syslog
	}
	if in.HTTP != nil {
		httpCfg = in.HTTP
	}
	if in.Loki != nil {
		lokiCfg = in.Loki
	}
	cfg := agentproto.DrainConfig{DrainID: row.ID, TeamID: teamID, AppID: appID, Kind: row.Kind, Internal: internal,
		Syslog: syslog, HTTP: httpSinkOf(httpCfg), Loki: lokiSinkOf(lokiCfg)}
	if err := validateLogDrainKind(cfg); err != nil {
		return db.LogDrain{}, err
	}
	configJSON, _ := json.Marshal(LogDrainConfig{Internal: internal, Syslog: syslog, HTTP: httpCfg, Loki: lokiCfg})
	updated, err := q.UpdateLogDrainInTeam(ctx, db.UpdateLogDrainInTeamParams{ID: row.ID, TeamID: teamID, Name: name, AppID: appID,
		ConfigJson: string(configJSON), AuthSealed: row.AuthSealed, Internal: internal, Enabled: boolOr(in.Enabled, row.Enabled)})
	if err != nil {
		return db.LogDrain{}, notFound("log drain")
	}
	if in.AuthValue != nil {
		if *in.AuthValue == "" {
			updated.AuthSealed = ""
			updated, err = q.UpdateLogDrainInTeam(ctx, db.UpdateLogDrainInTeamParams{ID: updated.ID, TeamID: teamID, Name: updated.Name,
				AppID: updated.AppID, ConfigJson: updated.ConfigJson, AuthSealed: "", Internal: updated.Internal, Enabled: updated.Enabled})
			if err != nil {
				return db.LogDrain{}, err
			}
		} else {
			updated, err = s.sealLogDrainAuth(ctx, q, teamID, updated, *in.AuthValue)
			if err != nil {
				return db.LogDrain{}, err
			}
		}
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "log_drain.updated", "log_drain", updated.ID,
		actor.audit(map[string]any{"name": updated.Name})); err != nil {
		return db.LogDrain{}, err
	}
	if updated.Enabled {
		s.pushLogDrain(context.WithoutCancel(ctx), teamID, actor, updated)
	} else {
		s.removeLogDrain(context.WithoutCancel(ctx), teamID, actor, updated)
	}
	return updated, nil
}

// DeleteLogDrain removes a drain everywhere it runs and forgets it.
func (s *Service) DeleteLogDrain(ctx context.Context, q *db.Queries, teamID string, actor Actor, row db.LogDrain) error {
	s.removeLogDrain(context.WithoutCancel(ctx), teamID, actor, row)
	if n, err := q.DeleteLogDrainInTeam(ctx, db.DeleteLogDrainInTeamParams{ID: row.ID, TeamID: teamID}); err != nil || n == 0 {
		return notFound("log drain")
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "log_drain.deleted", "log_drain", row.ID, actor.audit(map[string]any{"name": row.Name}))
}

// TestLogDrain ships one synthetic line through the drain right now (the persisted config, or —
// while still being created — whatever the caller has typed so far) and reports whether the sink
// accepted it. It targets one server: the app's, when the drain is scoped to an app, else the
// first enrolled server of the team.
func (s *Service) TestLogDrain(ctx context.Context, q *db.Queries, teamID string, row db.LogDrain) (agentproto.DrainTestResult, error) {
	srv, err := s.logDrainTestServer(ctx, q, teamID, row.AppID)
	if err != nil {
		return agentproto.DrainTestResult{}, err
	}
	cfg, err := s.sealedDrainConfig(ctx, q, teamID, row, *srv.AgentPublicKey)
	if err != nil {
		return agentproto.DrainTestResult{}, err
	}
	raw, _ := json.Marshal(agentproto.DrainTestParams{Drain: cfg})
	op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: srv.ID, Kind: agentproto.KindLogDrainTest, Params: raw, CreatedBy: "system"})
	if err != nil {
		return agentproto.DrainTestResult{}, err
	}
	var result agentproto.DrainTestResult
	if _, err := s.AwaitResult(ctx, op, logDrainTimeout, &result); err != nil {
		return agentproto.DrainTestResult{OK: false, Detail: err.Error()}, nil
	}
	return result, nil
}

// logDrainTestServer picks the server a test (or a push) targets: the app's own server when the
// drain is scoped to one app, else the team's first enrolled server (a team-wide drain targets
// every enrolled server when actually pushed; a test only needs one to prove the sink works).
func (s *Service) logDrainTestServer(ctx context.Context, q *db.Queries, teamID, appID string) (db.Server, error) {
	if appID != "" {
		a, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: appID, TeamID: teamID})
		if err != nil {
			return db.Server{}, notFound("app")
		}
		srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
		if err != nil || srv.AgentPublicKey == nil {
			return db.Server{}, conflict("that app's server has no agent enrolled yet")
		}
		return srv, nil
	}
	servers, err := q.ListServersOfTeam(ctx, teamID)
	if err != nil {
		return db.Server{}, err
	}
	for _, srv := range servers {
		if srv.AgentPublicKey != nil {
			return srv, nil
		}
	}
	return db.Server{}, conflict("this team has no server with an agent enrolled yet")
}

// pushLogDrain pushes a drain's current configuration, in the background, to every server it now
// covers: the app's server when scoped to one app, else every enrolled server of the team.
func (s *Service) pushLogDrain(ctx context.Context, teamID string, actor Actor, row db.LogDrain) {
	go func() {
		_ = tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
			servers, err := s.logDrainTargets(ctx, q, teamID, row.AppID)
			if err != nil {
				return nil
			}
			for _, srv := range servers {
				cfg, err := s.sealedDrainConfig(ctx, q, teamID, row, *srv.AgentPublicKey)
				if err != nil {
					s.log.Warn("log drain: could not seal auth for push", "drain", row.ID, "server", srv.ID, "err", err)
					continue
				}
				raw, _ := json.Marshal(cfg)
				if _, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: srv.ID, Kind: agentproto.KindLogDrainSet, Params: raw, CreatedBy: actor.UserID}); err != nil {
					s.log.Warn("log drain: could not enqueue log.drains.set", "drain", row.ID, "server", srv.ID, "err", err)
				}
			}
			return nil
		})
	}()
}

// removeLogDrain tells every server that might run it to forget it (best-effort: a server that
// never had it configured simply ignores the remove).
func (s *Service) removeLogDrain(ctx context.Context, teamID string, actor Actor, row db.LogDrain) {
	go func() {
		_ = tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
			servers, err := q.ListServersOfTeam(ctx, teamID)
			if err != nil {
				return nil
			}
			for _, srv := range servers {
				if srv.AgentPublicKey == nil {
					continue
				}
				raw, _ := json.Marshal(agentproto.DrainConfig{DrainID: row.ID, TeamID: teamID, Kind: row.Kind, Remove: true})
				if _, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: srv.ID, Kind: agentproto.KindLogDrainSet, Params: raw, CreatedBy: actor.UserID}); err != nil {
					s.log.Warn("log drain: could not enqueue removal", "drain", row.ID, "server", srv.ID, "err", err)
				}
			}
			return nil
		})
	}()
}

// logDrainTargets resolves the servers a drain covers.
func (s *Service) logDrainTargets(ctx context.Context, q *db.Queries, teamID, appID string) ([]db.Server, error) {
	if appID != "" {
		a, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: appID, TeamID: teamID})
		if err != nil {
			return nil, notFound("app")
		}
		srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
		if err != nil || srv.AgentPublicKey == nil {
			return nil, nil
		}
		return []db.Server{srv}, nil
	}
	all, err := q.ListServersOfTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	out := make([]db.Server, 0, len(all))
	for _, srv := range all {
		if srv.AgentPublicKey != nil {
			out = append(out, srv)
		}
	}
	return out, nil
}

// sealLogDrainAuth seals a drain's auth header value at rest with the team DEK.
func (s *Service) sealLogDrainAuth(ctx context.Context, q *db.Queries, teamID string, row db.LogDrain, plain string) (db.LogDrain, error) {
	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return db.LogDrain{}, err
	}
	sealed, err := crypto.Seal(dek, teamID, LogDrainAuthContext(row.ID), []byte(plain))
	if err != nil {
		return db.LogDrain{}, err
	}
	return q.UpdateLogDrainInTeam(ctx, db.UpdateLogDrainInTeamParams{ID: row.ID, TeamID: teamID, Name: row.Name, AppID: row.AppID,
		ConfigJson: row.ConfigJson, AuthSealed: sealed, Internal: row.Internal, Enabled: row.Enabled})
}

// sealedDrainConfig builds the wire DrainConfig for one server: the row's settings, with the auth
// header value (opened with the team DEK, when there is one) re-sealed to that agent's own key.
func (s *Service) sealedDrainConfig(ctx context.Context, q *db.Queries, teamID string, row db.LogDrain, agentPubKey string) (agentproto.DrainConfig, error) {
	cur := DecodeLogDrainConfig(row.ConfigJson)
	cfg := agentproto.DrainConfig{DrainID: row.ID, TeamID: teamID, AppID: row.AppID, Kind: row.Kind, Internal: cur.Internal,
		Syslog: cur.Syslog, HTTP: httpSinkOf(cur.HTTP), Loki: lokiSinkOf(cur.Loki)}
	if row.AuthSealed == "" {
		return cfg, nil
	}
	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return agentproto.DrainConfig{}, err
	}
	plain, err := crypto.Open(dek, teamID, LogDrainAuthContext(row.ID), row.AuthSealed)
	if err != nil {
		return agentproto.DrainConfig{}, err
	}
	box, err := agentproto.SealBox(agentPubKey, agentproto.DrainSecretContext(row.ID), plain)
	if err != nil {
		return agentproto.DrainConfig{}, err
	}
	switch row.Kind {
	case agentproto.DrainKindHTTP:
		if cfg.HTTP != nil {
			cfg.HTTP.AuthSealed = box
		}
	case agentproto.DrainKindLoki:
		if cfg.Loki != nil {
			cfg.Loki.AuthSealed = box
		}
	}
	return cfg, nil
}

func httpSinkOf(c *LogDrainHTTPConfig) *agentproto.HTTPSink {
	if c == nil {
		return nil
	}
	return &agentproto.HTTPSink{URL: c.URL, AuthHeader: c.AuthHeader}
}

func lokiSinkOf(c *LogDrainLokiConfig) *agentproto.LokiSink {
	if c == nil {
		return nil
	}
	return &agentproto.LokiSink{URL: c.URL, AuthHeader: c.AuthHeader, Labels: c.Labels}
}

func validateLogDrainKind(cfg agentproto.DrainConfig) error {
	switch cfg.Kind {
	case agentproto.DrainKindSyslog:
		if cfg.Syslog == nil {
			return invalid("a syslog drain needs syslog settings")
		}
		if err := cfg.Syslog.Validate(); err != nil {
			return invalid(err.Error())
		}
	case agentproto.DrainKindHTTP:
		if cfg.HTTP == nil {
			return invalid("an http drain needs http settings")
		}
		if err := cfg.HTTP.Validate(cfg.Internal); err != nil {
			return invalid(err.Error())
		}
	case agentproto.DrainKindLoki:
		if cfg.Loki == nil {
			return invalid("a loki drain needs loki settings")
		}
		if err := cfg.Loki.Validate(cfg.Internal); err != nil {
			return invalid(err.Error())
		}
	default:
		return invalid("kind is syslog, http or loki")
	}
	return nil
}
