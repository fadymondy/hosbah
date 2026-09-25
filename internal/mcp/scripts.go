package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/scripts"
	"github.com/fadymondy/hosbah/internal/scripts/builtin"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ScriptTools are MH-576 D4/D5: the team's own script library, the built-in one-click installs, and
// running either on one or many servers. Reading the library and run history is scopes.Read; saving
// or deleting a script and starting a run is scopes.Write (running is also gated by
// tenancy.AbilityManageServers, owner/admin, the same tier the REST API uses); run_script is
// destructive — it changes the state of a real server — so it needs the same confirm every other
// destructive tool does, and its wait honours the ~25s hold other long-running tools use
// (mcp.WaitHold / wait_deployment).
func ScriptTools() []Tool {
	return []Tool{
		{
			Name: "list_scripts", Scope: scopes.Read,
			Description: "The team's own script library.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				rows, err := env.Q.ListTeamScripts(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list the scripts")
				}
				if len(rows) == 0 {
					return "No scripts yet. save_script adds one; list_builtin_scripts has the built-in installs.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d script(s):\n", len(rows))
				for _, s := range rows {
					fmt.Fprintf(&b, "  %-24s run_as %-4s timeout %ds  (id %s)\n", s.Name, s.RunAs, s.TimeoutSeconds, s.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "list_builtin_scripts", Scope: scopes.Read,
			Description: "The read-only catalogue of one-click host software installs (Caddy, PHP, Composer, Node.js, MySQL/MariaDB, Redis, phpMyAdmin, Chromium), with their params.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				var b strings.Builder
				b.WriteString("Built-in scripts:\n")
				for _, s := range builtin.List() {
					fmt.Fprintf(&b, "  %-12s %s\n", s.ID, s.Description)
					for _, p := range s.Params {
						fmt.Fprintf(&b, "      param %s: one of %s (default %s)\n", p.Name, strings.Join(p.Enum, ", "), p.Default)
					}
				}
				return b.String(), nil
			},
		},
		{
			Name: "get_script", Scope: scopes.Read,
			Description: "One team script by id or name, with its body.",
			InputSchema: schema([]string{"script"}, map[string]any{"script": str("Script id or name")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				s, err := findScript(ctx, env, args)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("%s (%s)\nrun_as %s, timeout %ds\n\n%s\n\n--- body ---\n%s\n", s.Name, s.ID, s.RunAs, s.TimeoutSeconds, s.Description, s.Body), nil
			},
		},
		{
			Name: "save_script", Scope: scopes.Write,
			Description: "Create or replace a team script. Omit script to create; give it (id or name) to replace an existing one.",
			InputSchema: schema([]string{"name", "body"}, map[string]any{
				"script": str("Existing script id or name, to replace it (optional)"),
				"name":        str("Unique in the team"),
				"description": str("optional"),
				"body":        str("Shell text, run with bash as root. Params reach it as env vars, never templated into this text."),
				"run_as":      map[string]any{"type": "string", "enum": []string{"root", "app"}, "default": "root"},
				"timeout_seconds": map[string]any{"type": "integer", "default": 600, "minimum": 1, "maximum": 3600},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				runAs := args.Str("run_as")
				if runAs == "" {
					runAs = "root"
				}
				timeout := int64(args.Int("timeout_seconds", 600, 1, 3600))
				defArgs, _ := json.Marshal(map[string]string{})
				envNames, _ := json.Marshal([]string{})
				if ref := args.Str("script"); ref != "" {
					existing, err := findScript(ctx, env, args)
					if err != nil {
						return "", err
					}
					s, err := env.Q.UpdateTeamScript(ctx, db.UpdateTeamScriptParams{TeamID: env.Caller.TeamID, ID: existing.ID, Name: args.Str("name"),
						Description: args.Str("description"), Body: args.Str("body"), DefaultArgs: string(defArgs), EnvNames: string(envNames),
						RunAs: runAs, TimeoutSeconds: timeout})
					if err != nil {
						return "", scriptSaveErr(err)
					}
					return "Updated " + s.Name + ".", nil
				}
				s, err := env.Q.CreateTeamScript(ctx, db.CreateTeamScriptParams{TeamID: env.Caller.TeamID, Name: args.Str("name"),
					Description: args.Str("description"), Body: args.Str("body"), DefaultArgs: string(defArgs), EnvNames: string(envNames),
					RunAs: runAs, TimeoutSeconds: timeout, CreatedBy: env.Caller.UserID})
				if err != nil {
					return "", scriptSaveErr(err)
				}
				return "Created " + s.Name + " (id " + s.ID + ").", nil
			},
		},
		{
			Name: "delete_script", Scope: scopes.Destructive, Destructive: true, ResourceType: "script",
			Description: "Remove a team script. Its run history is kept. confirm must equal the script's name.",
			InputSchema: schema([]string{"script", "confirm"}, map[string]any{"script": str("Script id or name"), "confirm": str("The script's name, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := findScript(ctx, env, args)
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, s.Name); err != nil {
					return "", err
				}
				if err := env.Q.DeleteTeamScript(ctx, db.DeleteTeamScriptParams{TeamID: env.Caller.TeamID, ID: s.ID}); err != nil {
					return "", errors.New("could not delete the script")
				}
				return "Deleted " + s.Name + ".", nil
			},
		},
		{
			Name: "run_script", Scope: scopes.Destructive, Destructive: true, ResourceType: "script_run",
			Description: "Run a team script or a built-in on one or more servers. confirm must equal \"run\". With wait=true, returns " +
				"when every target finishes (holding at most ~25s per call; a continue: line follows up if it takes longer).",
			InputSchema: schema([]string{"servers", "confirm"}, map[string]any{
				"script": str("Team script id or name (exactly one of script and builtin)"),
				"builtin": str("Built-in id, e.g. caddy, php, nodejs (exactly one of script and builtin)"),
				"servers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Server ids or names to run on"},
				"params": map[string]any{"type": "object", "description": "Non-secret params (a built-in's declared params, e.g. {\"version\":\"8.3\"})"},
				"secret_env": map[string]any{"type": "object", "description": "Sealed to each target agent's own key; never stored in plaintext"},
				"confirm": str(`Must equal "run"`),
				"wait":    map[string]any{"type": "boolean", "default": false},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				if err := RequireConfirm(args, "run"); err != nil {
					return "", err
				}
				scriptRef, builtinRef := args.Str("script"), args.Str("builtin")
				if (scriptRef == "") == (builtinRef == "") {
					return "", errors.New("give exactly one of script and builtin")
				}
				serverRefs := strList(args["servers"])
				if len(serverRefs) == 0 {
					return "", errors.New("servers is required: one or more server ids or names")
				}
				serverIDs := make([]string, 0, len(serverRefs))
				for _, ref := range serverRefs {
					srv, err := env.Q.ServerInTeamByRef(ctx, db.ServerInTeamByRefParams{TeamID: env.Caller.TeamID, Ref: ref})
					if err != nil {
						return "", fmt.Errorf("server %q not found", ref)
					}
					serverIDs = append(serverIDs, srv.ID)
				}
				var scriptID, builtinName *string
				if scriptRef != "" {
					s, err := findScriptByRef(ctx, env, scriptRef)
					if err != nil {
						return "", err
					}
					scriptID = &s.ID
				} else {
					builtinName = &builtinRef
				}
				name, body, _, timeoutSeconds, runArgs, err := scripts.BodyAndDefaults(ctx, env.Q, env.Caller.TeamID, scriptID, builtinName, strMap(args["params"]))
				if err != nil {
					return "", err
				}
				svc, err := scriptsOf(env)
				if err != nil {
					return "", err
				}
				targets, err := scripts.ResolveTargets(ctx, env.Q, env.Caller.TeamID, serverIDs)
				if err != nil {
					return "", err
				}
				run, rows, err := svc.StartRun(ctx, env.Q, env.Caller.TeamID, scripts.StartInput{
					ScriptID: scriptID, BuiltinName: builtinName, Name: name, Body: body, Args: runArgs, SecretEnv: strMap(args["secret_env"]),
					TimeoutSeconds: timeoutSeconds, Targets: targets, CreatedBy: env.Caller.UserID,
				})
				if err != nil {
					return "", fmt.Errorf("could not start the run: %w", err)
				}
				teamID := env.Caller.TeamID
				out := fmt.Sprintf("Started run %s on %d server(s).\n", run.ID, len(rows))
				if !args.Bool("wait") {
					out += "script_run_status " + run.ID + " to check on it.\n"
					env.Then = func(ctx context.Context) (string, error) {
						svc.Follow(teamID, rows)
						return "", nil
					}
					return out, nil
				}
				env.Then = func(ctx context.Context) (string, error) {
					svc.Follow(teamID, rows)
					until := time.Now().Add(time.Duration(args.Int("wait_seconds", 600, 10, 1800)) * time.Second)
					return waitScriptRun(ctx, env, svc, teamID, run.ID, until)
				}
				return "", nil
			},
		},
		{
			Name: "script_run_status", Scope: scopes.Read,
			Description: "The status of a run and, per server, the output tail of what the script printed.",
			InputSchema: schema([]string{"run"}, map[string]any{"run": str("Run id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				svc, err := scriptsOf(env)
				if err != nil {
					return "", err
				}
				return renderScriptRun(ctx, env, svc, env.Caller.TeamID, args.Str("run"))
			},
		},
	}
}

func scriptsOf(env *Env) (*scripts.Service, error) {
	if env.App == nil {
		return nil, errors.New("the scripts service is not running")
	}
	s, ok := env.App.Scripts.(*scripts.Service)
	if !ok || s == nil {
		return nil, errors.New("the scripts service is not running")
	}
	return s, nil
}

func findScript(ctx context.Context, env *Env, args Args) (db.Script, error) {
	return findScriptByRef(ctx, env, args.Str("script"))
}

func findScriptByRef(ctx context.Context, env *Env, ref string) (db.Script, error) {
	if ref == "" {
		return db.Script{}, errors.New("script is required (an id or a name)")
	}
	s, err := env.Q.GetTeamScript(ctx, db.GetTeamScriptParams{TeamID: env.Caller.TeamID, ID: ref})
	if err == nil {
		return s, nil
	}
	s, err = env.Q.GetTeamScriptByName(ctx, db.GetTeamScriptByNameParams{TeamID: env.Caller.TeamID, Name: ref})
	if err != nil {
		return db.Script{}, errors.New("script not found")
	}
	return s, nil
}

func scriptSaveErr(err error) error {
	if strings.Contains(err.Error(), "scripts_team_id_name_key") {
		return errors.New("a script with that name already exists")
	}
	return errors.New("could not save the script")
}

// strMap reads an object argument as a map[string]string (non-string values are dropped).
func strMap(raw any) map[string]string {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for k, v := range obj {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func renderScriptRun(ctx context.Context, env *Env, svc *scripts.Service, teamID, runID string) (string, error) {
	run, err := env.Q.GetTeamScriptRun(ctx, db.GetTeamScriptRunParams{TeamID: teamID, ID: runID})
	if err != nil {
		return "", errors.New("run not found")
	}
	rows, err := env.Q.ListTeamScriptRunServers(ctx, db.ListTeamScriptRunServersParams{TeamID: teamID, ScriptRunID: run.ID})
	if err != nil {
		return "", errors.New("could not list the run's servers")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Run %s: %s\n", run.ID, run.Status)
	for _, r := range rows {
		exit := "—"
		if r.ExitCode != nil {
			exit = fmt.Sprint(*r.ExitCode)
		}
		fmt.Fprintf(&b, "  server %-24s %-8s exit %s\n", r.ServerID, r.Status, exit)
		out, err := svc.Output(ctx, env.Q, r)
		if err == nil && out != "" {
			b.WriteString("    " + strings.ReplaceAll(strings.TrimRight(out, "\n"), "\n", "\n    ") + "\n")
		}
	}
	return b.String(), nil
}

func waitScriptRun(ctx context.Context, env *Env, svc *scripts.Service, teamID, runID string, until time.Time) (string, error) {
	hold := time.Now().Add(WaitHold)
	for {
		var run db.ScriptRun
		err := tenancy.Tx(ctx, env.App.SQLDB, env.Scope, func(q *db.Queries, _ *sql.Tx) error {
			var err error
			run, err = q.GetTeamScriptRun(ctx, db.GetTeamScriptRunParams{TeamID: teamID, ID: runID})
			return err
		})
		if err != nil {
			return "", errors.New("the run disappeared")
		}
		if run.Status == scripts.StatusOK || run.Status == scripts.StatusFailed {
			return renderScriptRun(ctx, env, svc, teamID, runID)
		}
		now := time.Now()
		if now.After(until) {
			return renderScriptRun(ctx, env, svc, teamID, runID)
		}
		if now.After(hold) {
			next, _ := json.Marshal(map[string]any{"run": runID})
			return fmt.Sprintf("Run %s is still going.\n%sscript_run_status %s\n", runID, ContinuePrefix, next), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}
