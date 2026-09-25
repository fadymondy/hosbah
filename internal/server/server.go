// Package server wires the togo kernel and returns a ready App. It lives in its own package
// so cmd/api (serve) and the integration tests boot exactly the same stack.
package server

import (
	"context"
	"fmt"
	"github.com/fadymondy/hosbah/catalog"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/auth"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/hosbah/internal/account"
	"github.com/fadymondy/hosbah/internal/api"
	"github.com/fadymondy/hosbah/internal/app"
	"github.com/fadymondy/hosbah/internal/config"
	"github.com/fadymondy/hosbah/internal/crypto"
	"github.com/fadymondy/hosbah/internal/dbsvc"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/mail"
	"github.com/fadymondy/hosbah/internal/mcp"
	"github.com/fadymondy/hosbah/internal/monitors"
	"github.com/fadymondy/hosbah/internal/notifications"
	"github.com/fadymondy/hosbah/internal/ops"
	_ "github.com/fadymondy/hosbah/internal/plugins" // blank-imports installed plugins (togo install)
	"github.com/fadymondy/hosbah/internal/provisioning"
	"github.com/fadymondy/hosbah/internal/schedules"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Boot builds the kernel + app and mounts Hosbah's routes. It does not listen.
//
// What is deliberately NOT mounted:
//   - internal/rest (generated CRUD from make:resource) and /graphql (generated resolvers):
//     both read and write whole tables with no team scope. Tenant data is reachable only
//     through internal/api's team-scoped handlers. They still compile, and `togo generate`
//     still regenerates them, so the resources stay generator-first.
//   - /events (SSE): App.Emit broadcasts payloads to every subscriber, with no per-team
//     channel — a cross-tenant leak waiting for its first Emit.
//   - /api/_meta/resources: the admin dashboard's schema dump; there is no such dashboard.
func Boot() *app.App {
	config.LoadDotEnv()

	k := togo.New()
	a := app.New(context.Background(), k)
	if a.SQLDB == nil {
		panic("hosbah: database unavailable — check DATABASE_URL")
	}

	keys, err := crypto.LoadKeyring()
	if err != nil {
		// Refuse to run rather than store team secrets without envelope encryption.
		panic("hosbah: " + err.Error())
	}
	a.Keys = keys

	authSvc, ok := auth.FromKernel(k)
	if !ok {
		panic("hosbah: auth service not registered")
	}
	// Email verification, password reset, sign-in by code and two-factor (internal/account).
	accounts := account.New(a.SQLDB, keys, authSvc, a.Log, mailSender)
	if n, err := accounts.ImportPluginTOTP(context.Background()); err != nil {
		a.Log.Error("could not carry over the plugin's two-factor secrets", "error", err)
	} else if n > 0 {
		a.Log.Info("two-factor secrets carried over from auth_totp, now sealed", "accounts", n)
	}

	// Outermost first.
	k.UseMiddleware(securityHeaders)
	k.UseMiddleware(tenancy.MetaMiddleware)
	k.UseMiddleware(accountGate(a))      // sign-up mode (closed / invite-only)
	k.UseMiddleware(accounts.Middleware) // register withholds the session until verified; login: disabled, unverified, 2FA
	requireAuthorization(k, a)
	accounts.Mount(
		func(p string, h http.HandlerFunc) { k.Router.Post(p, h) },
		func(p string, h http.HandlerFunc) { k.Router.Get(p, h) },
		func(p string, h http.HandlerFunc) { k.Router.Delete(p, h) },
	)
	if configureAccounts != nil {
		configureAccounts(accounts)
	}

	svc, err := ops.New(a, ops.ConfigFromEnv())
	if err != nil {
		panic("hosbah: " + err.Error())
	}
	svc.RegisterRunner(ops.KindProviderCreate, provisioning.Runner(a, svc))
	svc.RegisterRunner(ops.KindServerConnect, provisioning.ConnectRunner(a, svc))
	svc.Mount(k.Router) // /install, /agent/* — outside /api, authenticated by token or signature
	svc.Start()
	a.Ops = svc

	deploys := deploy.New(a, svc)
	deploys.MountPublic(k.Router) // /hooks/*, /internal/tls/allow — outside /api, token / HMAC / yes-no
	deploys.Start()
	a.Deploy = deploys

	if _, err := catalog.Builtin(); err != nil { // a bad embedded recipe never starts (and never passes the tests)
		panic("hosbah: recipe catalog: " + err.Error())
	}
	databases := dbsvc.New(a, svc, deploys)
	databases.StartScheduler()
	a.DBSvc = databases
	// Servers connected before workload import existed: convert what their facts already list.
	if _, err := importer.Backfill(context.Background(), a.SQLDB, a.Log); err != nil {
		a.Log.Warn("imported workloads backfill failed", "err", err)
	}
	// Alerts and monitors: the evaluator loop (internal/monitors). It mails through the same sender.
	watch := monitors.New(a, mailSender)
	// Team notification channels (email, Slack, Discord, webhook — MH-575 H2). The same SSRF-safe
	// dialer the monitor checks themselves use (internal/monitors/safehttp.go), so a channel's
	// slack/discord/webhook URL is resolved and dialed exactly as carefully as a monitor's target.
	notify := notifications.New(a, svc, monitors.SafeTransport(nil))
	a.Notifications = notify
	watch.SetNotifier(notify)
	watch.Start()
	watch.MountPublic(k.Router) // /hooks/heartbeat/{token} — outside /api, ping-token authenticated
	a.Monitors = watch
	// Schedules: the cron loop (internal/schedules). Its runs are app.exec operations of the app's server.
	crons := schedules.New(a, svc)
	crons.Start()
	a.Schedules = crons
	a.Stop = func() { crons.Close(); watch.Stop(); databases.StopScheduler(); deploys.Stop(); svc.Stop() }

	humaAPI := humachi.New(k.Router, humaConfig())
	api.Register(humaAPI, a, svc)
	// A websocket upgrade is not a JSON request/response, so the web terminal is a raw route,
	// mounted next to the Huma ones under the same /api/teams prefix (member tier).
	api.MountTerminal(k.Router, a, svc)
	// The public status page (MH-575 H3): GET /api/public/status/{slug}, no session.
	api.MountPublic(k.Router, a, svc)

	k.Router.Post("/api/mcp", mcp.UserServer().Handler(a))
	k.Router.Get("/api/mcp", mcp.UserServer().Handler(a)) // 405: no server-initiated stream
	k.Router.Post("/api/admin/mcp", mcp.AdminServer().Handler(a))
	k.Router.Get("/api/admin/mcp", mcp.AdminServer().Handler(a))

	k.Router.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"ok","service":"hosbah","phase":"P5","togo":%q}`, togoVersion())
	})
	return a
}

func humaConfig() huma.Config {
	cfg := huma.DefaultConfig("Hosbah API", "0.1.0")
	cfg.Info.Description = "Hosbah control plane (P0: teams, members, invitations, MCP tokens, audit, feedback). " +
		"Team data is path-scoped under /api/teams/{team}; a team you are not in answers 404."
	return cfg
}

// securityHeaders: the API serves JSON only, so its CSP forbids everything; the docs page
// (Huma's /docs) needs its own scripts and is left to the browser defaults.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// togoVersion reports the togo framework version this app is built against.
func togoVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, d := range info.Deps {
			if d.Path == "github.com/togo-framework/togo" {
				return d.Version
			}
		}
	}
	return "dev"
}

// OpenAPI returns the OpenAPI 3.1 document (used by `togo generate`, the integration gate).
func OpenAPI() ([]byte, error) {
	router := chi.NewMux()
	humaAPI := humachi.New(router, humaConfig())
	api.Register(humaAPI, nil, nil)
	return humaAPI.OpenAPI().YAML()
}

// mailSender overrides how account emails are delivered; nil means mail.FromEnv (SMTP from
// MAIL_*, or the log with DEV_MAIL_LOG=1). The integration tests set it to capture codes.
var mailSender mail.Sender

// configureAccounts, when set, adjusts the account service after it is built (the integration
// tests point the Google and GitHub sign-in providers at fakes).
var configureAccounts func(*account.Service)
