package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/scripts"
	"github.com/fadymondy/hosbah/internal/scripts/builtin"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// The team's own script library (MH-576 D5) and its built-in catalogue (D1) and runs (D4): one
// shell body, run with bash as root on one or many servers. Reading the library and a run's status
// is any member; writing a script or starting a run is owner/admin (tenancy.AbilityManageServers —
// the same tier as connecting a server); a run is destructive, so MCP requires the same confirm
// other destructive tools do. Every run is audited.

// ScriptPath addresses one team script (id or name).
type ScriptPath struct {
	TeamPath
	Script string `path:"script" maxLength:"80"`
}

// ScriptRunPath addresses one run.
type ScriptRunPath struct {
	TeamPath
	Run string `path:"run" maxLength:"64"`
}

// ScriptOut is a team script.
type ScriptOut struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Body           string            `json:"body"`
	DefaultArgs    map[string]string `json:"default_args"`
	EnvNames       []string          `json:"env_names"`
	RunAs          string            `json:"run_as"`
	TimeoutSeconds int64             `json:"timeout_seconds"`
	CreatedBy      string            `json:"created_by"`
	CreatedAt      string            `json:"created_at"`
	UpdatedAt      string            `json:"updated_at"`
}

func toScriptOut(s db.Script) ScriptOut {
	out := ScriptOut{ID: s.ID, Name: s.Name, Description: s.Description, Body: s.Body, RunAs: s.RunAs,
		TimeoutSeconds: s.TimeoutSeconds, CreatedBy: s.CreatedBy, CreatedAt: s.CreatedAt.Format(timeLayout), UpdatedAt: s.UpdatedAt.Format(timeLayout)}
	out.DefaultArgs = map[string]string{}
	_ = json.Unmarshal([]byte(s.DefaultArgs), &out.DefaultArgs)
	out.EnvNames = []string{}
	_ = json.Unmarshal([]byte(s.EnvNames), &out.EnvNames)
	return out
}

// ScriptIn creates or updates a team script.
type ScriptIn struct {
	Name           string            `json:"name" minLength:"1" maxLength:"128"`
	Description    string            `json:"description,omitempty" maxLength:"2000"`
	Body           string            `json:"body" minLength:"1" maxLength:"1048576" doc:"Run with bash as root. Params reach it as env vars, never templated into this text."`
	DefaultArgs    map[string]string `json:"default_args,omitempty"`
	EnvNames       []string          `json:"env_names,omitempty" doc:"Documentation only: the variable names a run is expected to supply."`
	RunAs          string            `json:"run_as,omitempty" enum:"root,app" default:"root"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty" minimum:"1" maximum:"3600" default:"600"`
}

// ScriptRunIn starts a run of a team script or a built-in.
type ScriptRunIn struct {
	ScriptID    *string           `json:"script_id,omitempty" doc:"Exactly one of script_id and builtin_name."`
	BuiltinName *string           `json:"builtin_name,omitempty"`
	Servers     []string          `json:"servers" minItems:"1" maxItems:"50" doc:"Server ids to run on."`
	Params      map[string]string `json:"params,omitempty" doc:"Non-secret args (script's default_args, or a built-in's declared params)."`
	SecretEnv   map[string]string `json:"secret_env,omitempty" doc:"Sealed to each target agent's own key; never stored in plaintext."`
}

func (d *deps) scripts() *scripts.Service {
	if d.a == nil {
		return nil
	}
	s, _ := d.a.Scripts.(*scripts.Service)
	return s
}

func scriptFound(ctx context.Context, d *deps, p ScriptPath, ability string,
	then func(q *db.Queries, id *auth.Identity, m *tenancy.Membership, s db.Script) error) error {
	return d.inTeam(ctx, p.Team, ability, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
		s, err := q.GetTeamScript(ctx, db.GetTeamScriptParams{TeamID: m.TeamID, ID: p.Script})
		if err != nil {
			s, err = q.GetTeamScriptByName(ctx, db.GetTeamScriptByNameParams{TeamID: m.TeamID, Name: p.Script})
			if err != nil {
				return errNotFound
			}
		}
		return then(q, id, m, s)
	})
}

func (d *deps) registerScripts(api huma.API) {
	tags := []string{"Scripts"}
	base := "/api/teams/{team}/scripts"

	type scriptsListOut struct {
		Body struct {
			Scripts []ScriptOut `json:"scripts"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-scripts", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The team's own script library. Any member.",
	}, func(ctx context.Context, in *TeamPath) (*scriptsListOut, error) {
		out := &scriptsListOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListTeamScripts(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list the scripts")
			}
			out.Body.Scripts = make([]ScriptOut, 0, len(rows))
			for _, r := range rows {
				out.Body.Scripts = append(out.Body.Scripts, toScriptOut(r))
			}
			return nil
		})
		return out, err
	})

	type builtinListOut struct {
		Body struct {
			Scripts []builtin.Script `json:"scripts"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-builtin-scripts", Method: http.MethodGet, Path: base + "/builtin", Tags: tags,
		Summary: "The read-only catalogue of one-click host software installs (MH-576 D1). Any member.",
	}, func(ctx context.Context, in *TeamPath) (*builtinListOut, error) {
		out := &builtinListOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(*db.Queries, *sql.Tx, *auth.Identity, *tenancy.Membership) error {
			out.Body.Scripts = builtin.List()
			return nil
		})
		return out, err
	})

	type scriptOut struct{ Body ScriptOut }
	huma.Register(api, huma.Operation{OperationID: "create-script", Method: http.MethodPost, Path: base, DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Add a script to the team's library. 409: the name is taken. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body ScriptIn
	}) (*scriptOut, error) {
		out := &scriptOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			b := in.Body
			if b.RunAs == "" {
				b.RunAs = "root"
			}
			if b.TimeoutSeconds == 0 {
				b.TimeoutSeconds = 600
			}
			defArgs, _ := json.Marshal(nonNilMap(b.DefaultArgs))
			envNames, _ := json.Marshal(nonNilSlice(b.EnvNames))
			s, err := q.CreateTeamScript(ctx, db.CreateTeamScriptParams{TeamID: m.TeamID, Name: b.Name, Description: b.Description, Body: b.Body,
				DefaultArgs: string(defArgs), EnvNames: string(envNames), RunAs: b.RunAs, TimeoutSeconds: int64(b.TimeoutSeconds), CreatedBy: id.ID})
			if err != nil {
				return scriptsConflictErr(err, "could not create the script")
			}
			out.Body = toScriptOut(s)
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "script.create", "script", s.ID, map[string]any{"name": s.Name, "via": "web"})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "get-script", Method: http.MethodGet, Path: base + "/{script}", Tags: tags,
		Summary: "One script by id or name. Any member.",
	}, func(ctx context.Context, in *ScriptPath) (*scriptOut, error) {
		out := &scriptOut{}
		err := scriptFound(ctx, d, *in, tenancy.AbilityView, func(_ *db.Queries, _ *auth.Identity, _ *tenancy.Membership, s db.Script) error {
			out.Body = toScriptOut(s)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-script", Method: http.MethodPut, Path: base + "/{script}", Tags: tags,
		Summary: "Replace a script's body and settings. 409: the new name is taken. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		ScriptPath
		Body ScriptIn
	}) (*scriptOut, error) {
		out := &scriptOut{}
		err := scriptFound(ctx, d, in.ScriptPath, tenancy.AbilityManageServers, func(q *db.Queries, id *auth.Identity, m *tenancy.Membership, s db.Script) error {
			b := in.Body
			if b.RunAs == "" {
				b.RunAs = "root"
			}
			if b.TimeoutSeconds == 0 {
				b.TimeoutSeconds = 600
			}
			defArgs, _ := json.Marshal(nonNilMap(b.DefaultArgs))
			envNames, _ := json.Marshal(nonNilSlice(b.EnvNames))
			updated, err := q.UpdateTeamScript(ctx, db.UpdateTeamScriptParams{TeamID: m.TeamID, ID: s.ID, Name: b.Name, Description: b.Description,
				Body: b.Body, DefaultArgs: string(defArgs), EnvNames: string(envNames), RunAs: b.RunAs, TimeoutSeconds: int64(b.TimeoutSeconds)})
			if err != nil {
				return scriptsConflictErr(err, "could not update the script")
			}
			out.Body = toScriptOut(updated)
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "script.update", "script", s.ID, map[string]any{"name": updated.Name, "via": "web"})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-script", Method: http.MethodDelete, Path: base + "/{script}", DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Remove a script. Its run history is kept. Owner or admin. Audited.",
	}, func(ctx context.Context, in *ScriptPath) (*struct{}, error) {
		err := scriptFound(ctx, d, *in, tenancy.AbilityManageServers, func(q *db.Queries, id *auth.Identity, m *tenancy.Membership, s db.Script) error {
			if err := q.DeleteTeamScript(ctx, db.DeleteTeamScriptParams{TeamID: m.TeamID, ID: s.ID}); err != nil {
				return huma.Error500InternalServerError("could not delete the script")
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "script.delete", "script", s.ID, map[string]any{"name": s.Name, "via": "web"})
		})
		return nil, err
	})

	// ---- runs ------------------------------------------------------------------------------

	type runOut struct {
		Body struct {
			Run     db.ScriptRun         `json:"run"`
			Servers []db.ScriptRunServer `json:"servers"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "run-script", Method: http.MethodPost, Path: base + "/run", DefaultStatus: http.StatusAccepted, Tags: tags,
		Summary: "Run a team script or a built-in on one or more servers: 202 with the run and one row per server, each already " +
			"carrying its operation id — poll GET …/scripts/runs/{run} for status and output. 404: a server not in the team, or " +
			"no agent enrolled on it. 422: params fail the script's own allow list (a built-in's version enum, for example). " +
			"Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body ScriptRunIn
	}) (*runOut, error) {
		out := &runOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			b := in.Body
			if (b.ScriptID == nil) == (b.BuiltinName == nil) {
				return huma.Error422UnprocessableEntity("exactly one of script_id and builtin_name")
			}
			name, body, _, timeoutSeconds, args, err := scripts.BodyAndDefaults(ctx, q, m.TeamID, b.ScriptID, b.BuiltinName, b.Params)
			if err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			targets, err := scripts.ResolveTargets(ctx, q, m.TeamID, b.Servers)
			if err != nil {
				return huma.Error404NotFound(err.Error())
			}
			run, rows, err := d.scripts().StartRun(ctx, q, m.TeamID, scripts.StartInput{
				ScriptID: b.ScriptID, BuiltinName: b.BuiltinName, Name: name, Body: body, Args: args, SecretEnv: b.SecretEnv,
				TimeoutSeconds: timeoutSeconds, Targets: targets, CreatedBy: id.ID,
			})
			if err != nil {
				return huma.Error500InternalServerError("could not start the run: " + err.Error())
			}
			out.Body.Run, out.Body.Servers = run, rows
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "script.run", "script_run", run.ID,
				map[string]any{"script_id": b.ScriptID, "builtin_name": b.BuiltinName, "servers": b.Servers, "via": "web"})
		})
		if err != nil {
			return nil, err
		}
		d.scripts().Follow(out.Body.Run.TeamID, out.Body.Servers)
		return out, nil
	})

	type runsOut struct {
		Body struct {
			Runs []db.ScriptRun `json:"runs"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-script-runs", Method: http.MethodGet, Path: base + "/runs", Tags: tags,
		Summary: "The team's script runs, newest first. Any member.",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Limit  int32 `query:"limit" default:"50" minimum:"1" maximum:"100"`
		Offset int32 `query:"offset" default:"0" minimum:"0"`
	}) (*runsOut, error) {
		out := &runsOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListTeamScriptRuns(ctx, db.ListTeamScriptRunsParams{TeamID: m.TeamID, Limit: in.Limit, Offset: in.Offset})
			if err != nil {
				return huma.Error500InternalServerError("could not list the runs")
			}
			out.Body.Runs = rows
			return nil
		})
		return out, err
	})

	type runDetailOut struct {
		Body struct {
			Run     db.ScriptRun    `json:"run"`
			Servers []runServerView `json:"servers"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "get-script-run", Method: http.MethodGet, Path: base + "/runs/{run}", Tags: tags,
		Summary: "One run: its status and, per server, the output tail (up to 1 MiB) of what the script printed. Like a schedule's " +
			"output this may hold secrets: owner, admin or deployer (a viewer gets 403).",
	}, func(ctx context.Context, in *ScriptRunPath) (*runDetailOut, error) {
		out := &runDetailOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityDeploy, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			run, err := q.GetTeamScriptRun(ctx, db.GetTeamScriptRunParams{TeamID: m.TeamID, ID: in.Run})
			if err != nil {
				return errNotFound
			}
			rows, err := q.ListTeamScriptRunServers(ctx, db.ListTeamScriptRunServersParams{TeamID: m.TeamID, ScriptRunID: run.ID})
			if err != nil {
				return huma.Error500InternalServerError("could not list the run's servers")
			}
			out.Body.Run = run
			out.Body.Servers = make([]runServerView, 0, len(rows))
			for _, r := range rows {
				output, err := d.scripts().Output(ctx, q, r)
				if err != nil {
					return huma.Error500InternalServerError("could not open a server's output")
				}
				out.Body.Servers = append(out.Body.Servers, runServerView{ScriptRunServer: r, Output: output})
			}
			return nil
		})
		return out, err
	})
}

// runServerView is one target's row plus its opened output.
type runServerView struct {
	db.ScriptRunServer
	Output string `json:"output"`
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func nonNilSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// scriptsConflictErr: the scripts_team_id_name_key unique violation is this section's 409;
// anything else falls through.
func scriptsConflictErr(err error, fallback string) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "scripts_team_id_name_key") {
		return huma.Error409Conflict("a script with that name already exists")
	}
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	return huma.Error500InternalServerError(fallback)
}
