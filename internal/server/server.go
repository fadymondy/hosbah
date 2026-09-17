// Package server wires the togo kernel and returns a ready App. It lives in its own package
// so cmd/api (serve) and the integration tests boot exactly the same stack.
package server

import (
	"context"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/cloudy/internal/api"
	"github.com/fadymondy/cloudy/internal/app"
	"github.com/fadymondy/cloudy/internal/config"
	"github.com/fadymondy/cloudy/internal/crypto"
	"github.com/fadymondy/cloudy/internal/deploy"
	"github.com/fadymondy/cloudy/internal/mcp"
	"github.com/fadymondy/cloudy/internal/ops"
	_ "github.com/fadymondy/cloudy/internal/plugins" // blank-imports installed plugins (togo install)
	"github.com/fadymondy/cloudy/internal/provisioning"
	"github.com/fadymondy/cloudy/internal/tenancy"
)

// Boot builds the kernel + app and mounts Cloudy's routes. It does not listen.
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
		panic("cloudy: database unavailable — check DATABASE_URL")
	}

	keys, err := crypto.LoadKeyring()
	if err != nil {
		// Refuse to run rather than store team secrets without envelope encryption.
		panic("cloudy: " + err.Error())
	}
	a.Keys = keys

	// Outermost first.
	k.UseMiddleware(securityHeaders)
	k.UseMiddleware(tenancy.MetaMiddleware)
	k.UseMiddleware(requireSecondFactor(a))
	requireAuthorization(k)

	svc, err := ops.New(a, ops.ConfigFromEnv())
	if err != nil {
		panic("cloudy: " + err.Error())
	}
	svc.RegisterRunner(ops.KindProviderCreate, provisioning.Runner(a, svc))
	svc.Mount(k.Router) // /install, /agent/* — outside /api, authenticated by token or signature
	svc.Start()
	a.Ops = svc

	deploys := deploy.New(a, svc)
	deploys.MountPublic(k.Router) // /hooks/*, /internal/tls/allow — outside /api, token / HMAC / yes-no
	deploys.Start()
	a.Deploy = deploys
	a.Stop = func() { deploys.Stop(); svc.Stop() }

	humaAPI := humachi.New(k.Router, humaConfig())
	api.Register(humaAPI, a, svc)

	k.Router.Post("/api/mcp", mcp.UserServer().Handler(a))
	k.Router.Get("/api/mcp", mcp.UserServer().Handler(a)) // 405: no server-initiated stream
	k.Router.Post("/api/admin/mcp", mcp.AdminServer().Handler(a))
	k.Router.Get("/api/admin/mcp", mcp.AdminServer().Handler(a))

	k.Router.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"ok","service":"cloudy","phase":"P2","togo":%q}`, togoVersion())
	})
	return a
}

func humaConfig() huma.Config {
	cfg := huma.DefaultConfig("Cloudy API", "0.1.0")
	cfg.Info.Description = "Cloudy control plane (P0: teams, members, invitations, MCP tokens, audit, feedback). " +
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
