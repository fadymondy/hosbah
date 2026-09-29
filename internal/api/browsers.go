package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/dbsvc"
	"github.com/fadymondy/hosbah/internal/storage"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The "Browsers" section of a site: headless Chromium instances the site drives over CDP/WebSocket
(internal/dbsvc/browsers.go).

Read: any member. Write: whoever may manage the app (manage_apps). Imported apps have no env Hosbah
injects: every write is 422. A browser's token is never in a response, and there is no reveal route:
the app reads it from its own sealed variables.
*/

// BrowserPath addresses one browser of an app (id or name).
type BrowserPath struct {
	AppPath
	Browser string `path:"browser" maxLength:"64" doc:"Browser id or name."`
}

// BrowsersOut is a site's browsers.
type BrowsersOut struct {
	Browsers        []dbsvc.Browser `json:"browsers"`
	StatusOperation *string         `json:"status_operation" doc:"The browser.status operation that was asked; null when the app has no browsers or its server is not connected."`
}

func (d *deps) registerBrowsers(api huma.API) {
	tags := []string{"Browsers"}
	base := "/api/teams/{team}/apps/{app}/browsers"

	type browsersOut struct{ Body BrowsersOut }
	huma.Register(api, huma.Operation{OperationID: "list-app-browsers", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The app's headless browsers. running, restarts and memory_bytes are best effort — the server is asked (up to 8 s), and an " +
			"offline or silent server leaves them null, still 200. A browser's token is never returned. Any member.",
	}, func(ctx context.Context, in *AppPath) (*browsersOut, error) {
		out := &browsersOut{Body: BrowsersOut{Browsers: []dbsvc.Browser{}}}
		var op db.Operation
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if out.Body.Browsers, err = dbsvc.ListBrowsers(ctx, q, m.TeamID, a); err != nil {
				return huma.Error500InternalServerError("could not list the browsers")
			}
			op = d.dbs().QueueBrowserStatus(ctx, q, m.TeamID, webActor(id), a, out.Body.Browsers)
			return nil
		})
		if err != nil {
			return nil, err
		}
		if op.ID != "" { // after the commit: the operation exists for the dispatcher
			out.Body.StatusOperation = &op.ID
			var res agentproto.BrowserStatusResult
			if _, err := d.deploys().AwaitResult(ctx, op, dbsvc.BrowserStatusWait, &res); err == nil {
				dbsvc.WithBrowserStatus(out.Body.Browsers, res)
			}
		}
		return out, nil
	})

	type browserCreatedOut struct {
		Body struct {
			dbsvc.Browser
			Applies   string `json:"applies" enum:"next_deploy" doc:"The variables reach the app on its next deploy or restart."`
			Operation string `json:"operation" doc:"The browser.create operation; the browser's status follows it (creating, then ready or failed)."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "create-app-browser", Method: http.MethodPost, Path: base,
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Add a browser to the app, on the app's server: kind headless (default) is a bare Chromium the app drives over CDP/WebSocket, " +
			"setting <PREFIX>_WS_ENDPOINT (ws://…?token=…) and <PREFIX>_TOKEN on the app as sealed variables (env lists the names; the token " +
			"is never returned) — reachable from the app's containers only, never published. Kind desktop is a full interactive KasmVNC " +
			"desktop at its own hostname (see hostname in the response), opened by a team member (POST .../open) — no env vars, no token; " +
			"access is Hosbah's own forward_auth on that hostname. The image is fixed by Hosbah for the kind. 201 with status creating — it " +
			"turns ready or failed when the server has answered (a first image pull takes minutes). 422: a bad name, limit or env_prefix; an " +
			"imported app; no domain available for a desktop browser's hostname yet. 409: the name is taken, the app has 3, its server has no " +
			"agent, or (headless) one of the variable names is already set on the app (nothing is ever overwritten). Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			Name               string  `json:"name" minLength:"1" maxLength:"31" doc:"^[a-z][a-z0-9-]{0,30}$, unique in the app."`
			Kind               string  `json:"kind,omitempty" enum:"headless,desktop" doc:"Default headless."`
			MemoryMB           int64   `json:"memory_mb,omitempty" doc:"Headless 256-4096 (default 1024); desktop 512-8192 (default 2048)."`
			CPUs               float64 `json:"cpus,omitempty" doc:"0.25 to 4 (default 1)."`
			MaxSessions        int64   `json:"max_sessions,omitempty" doc:"Headless only: concurrent WebSocket sessions, 1 to 20 (default 5)."`
			IdleTimeoutSeconds int64   `json:"idle_timeout_seconds,omitempty" doc:"Headless only: a session silent this long is closed, 30 to 3600 (default 300)."`
			EnvPrefix          string  `json:"env_prefix,omitempty" maxLength:"21" doc:"Headless only: ^[A-Z][A-Z0-9_]{0,20}$ (default BROWSER)."`
			Hostname           string  `json:"hostname,omitempty" maxLength:"253" doc:"Desktop only: its own hostname, a subdomain of one of the team's Cloudflare zones (default browser.<zone of the app's domain>)."`
		}
	}) (*browserCreatedOut, error) {
		out := &browserCreatedOut{}
		var (
			op     db.Operation
			teamID string
		)
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			teamID = m.TeamID
			out.Body.Browser, op, err = d.dbs().CreateBrowser(ctx, q, m.TeamID, webActor(id), a, dbsvc.BrowserSpec{Name: in.Body.Name,
				Kind: in.Body.Kind, MemoryMB: in.Body.MemoryMB, CPUs: in.Body.CPUs, MaxSessions: in.Body.MaxSessions,
				IdleTimeoutSeconds: in.Body.IdleTimeoutSeconds, EnvPrefix: in.Body.EnvPrefix, Hostname: in.Body.Hostname})
			return storageErr(err, "could not create the browser")
		})
		if err != nil {
			return nil, err
		}
		d.dbs().FollowBrowser(teamID, out.Body.ID, op) // after the commit
		out.Body.Applies, out.Body.Operation = storage.AppliesNextDeploy, op.ID
		return out, nil
	})

	type browserOpenedOut struct {
		Body struct {
			URL string `json:"url" doc:"One-time; open it right away. Exchanged automatically for a session on the browser's own hostname."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "open-app-browser", Method: http.MethodPost, Path: base + "/{browser}/open", Tags: tags,
		Summary: "Mint a one-time link that opens a desktop browser: valid briefly, usable once, then exchanged automatically for a session " +
			"cookie on the browser's own hostname (Hosbah's forward_auth re-checks team membership and role on every request after that). " +
			"422: not a desktop browser, or it has no hostname yet. 409: the browser is not ready. Any member.",
	}, func(ctx context.Context, in *BrowserPath) (*browserOpenedOut, error) {
		out := &browserOpenedOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			b, err := dbsvc.FindBrowser(ctx, q, m.TeamID, a, in.Browser)
			if err != nil {
				return errNotFound
			}
			out.Body.URL, err = d.dbs().OpenDesktopBrowser(ctx, q, m.TeamID, webActor(id), b)
			return storageErr(err, "could not open the browser")
		})
		if err != nil {
			return nil, err
		}
		return out, nil
	})

	type browserRemovedOut struct {
		Body struct {
			Removed   bool     `json:"removed"`
			Unset     []string `json:"unset" doc:"The variables removed from the app."`
			Applies   string   `json:"applies" enum:"next_deploy" doc:"The app loses the variables on its next deploy or restart; the browser is already gone."`
			Operation *string  `json:"operation" doc:"The browser.remove operation."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "remove-app-browser", Method: http.MethodDelete, Path: base + "/{browser}", Tags: tags,
		Summary: "Remove a browser from the app (confirm = the browser's name): its container is stopped and removed and exactly the variables " +
			"it set are unset. Waits up to 90 s for the server; 409 (nothing changed): the server is offline or did not answer, or the " +
			"browser is still being created. 422: confirm is not the name; an imported app. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		BrowserPath
		Body struct {
			Confirm string `json:"confirm" minLength:"1" maxLength:"64" doc:"The browser's name, exactly."`
		}
	}) (*browserRemovedOut, error) {
		out := &browserRemovedOut{}
		var rm dbsvc.BrowserRemoval
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if err := dbsvc.RefuseImportedBrowsers(a); err != nil {
				return storageErr(err, "could not remove the browser")
			}
			b, err := dbsvc.FindBrowser(ctx, q, m.TeamID, a, in.Browser)
			if err != nil {
				return errNotFound
			}
			rm, err = d.dbs().BeginBrowserRemoval(ctx, q, m.TeamID, webActor(id), a, b, in.Body.Confirm)
			return storageErr(err, "could not remove the browser")
		})
		if err != nil {
			return nil, err
		}
		failure := d.dbs().AwaitBrowserRemoval(ctx, rm) // after the commit
		fin := context.WithoutCancel(ctx)               // a caller who hung up must not leave the browser 'removing'
		err = d.inTeam(fin, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(fin, q, m, in.App)
			if err != nil {
				return err
			}
			return storageErr(d.dbs().FinishBrowserRemoval(fin, q, m.TeamID, webActor(id), a, rm, failure), "could not remove the browser")
		})
		if err != nil {
			return nil, err
		}
		if failure != "" {
			return nil, storageErr(dbsvc.BrowserRemovalRefused(failure), "could not remove the browser")
		}
		out.Body.Removed, out.Body.Unset, out.Body.Applies = true, dbsvc.BrowserEnvNames(rm.Browser.EnvPrefix), storage.AppliesNextDeploy
		if rm.Operation.ID != "" {
			out.Body.Operation = &rm.Operation.ID
		}
		return out, nil
	})
}
