// The integration API (MH-1153): what an outside system (Mahaam first) needs to show and drive one
// Hosbah app — its status and deployments, a deploy button, and a one-time link that signs a
// customer in to that app's page in the panel.
//
// Two bearer tokens, each looked up only in its own table and never through a browser session:
//
//	hbu_…  a team user token (mcp_tokens), the same one the MCP server takes: lists the team's apps
//	       and mints app tokens (manage permission)
//	hba_…  an app token (app_tokens), scoped to exactly one app: reads it, deploys it, issues panel
//	       links, revokes itself. It dies with its app (ON DELETE CASCADE) or when revoked.
//
// Every route is in the public tier (internal/server/authorization.go, prefix /api/integrations/)
// and authenticates its own bearer, like the product-key API. The panel link itself is a raw GET
// (MountIntegrations): it spends the link, signs the customer in with an app-scoped session (Guard
// PanelAppGuard) and redirects to /{locale}/panel/{server}/apps/{app}. internal/server's
// activeAccount keeps that session to the app's own routes (PanelAppAllows), and inTeam lets it in
// as a deployer of that one app (panelAppMembership).
package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/app"
	"github.com/fadymondy/hosbah/internal/brand"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/mcp"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

const (
	// IntegrationsPathPrefix is public in authorization.go; every route under it checks its own bearer.
	IntegrationsPathPrefix = "/api/integrations/"
	// PanelAppGuard marks a session signed in through an app's panel link (MH-1153).
	PanelAppGuard = "panel-app"

	panelLinkTTL     = 5 * time.Minute
	appReadBudget    = 5 * time.Second
	panelLinkPerMin  = 30
	appTokenPerMin   = 120
	panelAppMePath   = "/api/me/panel-app"
	panelAppKeyInfix = "#app:"
)

// IntegrationApp is an app as an integration sees it.
type IntegrationApp struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Title      string   `json:"title"`
	Status     string   `json:"status"`
	ServerID   string   `json:"server_id"`
	ServerName string   `json:"server_name"`
	Domains    []string `json:"domains"`
	LogoURL    *string  `json:"logo_url"`
	PanelURL   string   `json:"panel_url" doc:"The app's page in its server's panel (members sign in as usual; customers use a panel session link)"`
}

// IntegrationDeployment is one deployment of the app.
type IntegrationDeployment struct {
	ID         string     `json:"id"`
	Number     int64      `json:"number"`
	Kind       string     `json:"kind" enum:"deploy,rollback,restart"`
	Status     string     `json:"status" enum:"queued,running,succeeded,failed,cancelled"`
	Ref        string     `json:"ref"`
	Commit     *string    `json:"commit"`
	Message    *string    `json:"message"`
	QueuedAt   time.Time  `json:"queued_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// AppTokenOut is one app token, without its secret (shown once, on creation).
type AppTokenOut struct {
	ID             string     `json:"id"`
	Label          string     `json:"label"`
	CreatedBy      string     `json:"created_by"`
	CreatedByEmail string     `json:"created_by_email"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at"`
}

// rateWindow caps calls per key in a sliding window.
type rateWindow struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newRateWindow(max int, window time.Duration) *rateWindow {
	return &rateWindow{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *rateWindow) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	keep := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			keep = append(keep, t)
		}
	}
	if len(keep) >= l.max {
		l.hits[key] = keep
		return false
	}
	l.hits[key] = append(keep, now)
	return true
}

var (
	appTokenCalls  = newRateWindow(appTokenPerMin, time.Minute)
	panelLinkMints = newRateWindow(panelLinkPerMin, time.Minute)
	panelLinkUses  = newRateWindow(panelLinkPerMin, time.Minute)
)

// webOrigin is the console's public origin (APP_URL), or "" (then URLs stay relative).
func webOrigin() string { return strings.TrimRight(strings.TrimSpace(os.Getenv("APP_URL")), "/") }

func panelAppPath(locale, serverID, appID string) string {
	return "/" + locale + "/panel/" + url.PathEscape(serverID) + "/apps/" + url.PathEscape(appID)
}

func toIntegrationDeployment(d DeploymentOut) IntegrationDeployment {
	return IntegrationDeployment{ID: d.ID, Number: d.Number, Kind: d.Kind, Status: d.Status, Ref: d.Ref, Commit: d.CommitSHA,
		Message: d.CommitMessage, QueuedAt: d.CreatedAt, FinishedAt: d.FinishedAt}
}

// integrationApp builds the app's JSON (team scope already set).
func integrationApp(ctx context.Context, q *db.Queries, a db.App, serverName string) IntegrationApp {
	title := a.Name
	if a.Title != nil && strings.TrimSpace(*a.Title) != "" {
		title = *a.Title
	}
	out := IntegrationApp{ID: a.ID, Name: a.Name, Title: title, Status: a.Status, ServerID: a.ServerID, ServerName: serverName,
		Domains: []string{}, PanelURL: webOrigin() + panelAppPath("en", a.ServerID, a.ID)}
	if serverName == "" {
		if s, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: a.TeamID}); err == nil {
			out.ServerName = s.Name
		}
	}
	if ds, err := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: a.TeamID}); err == nil {
		for _, dm := range ds {
			out.Domains = append(out.Domains, dm.Hostname)
		}
	}
	logo := []AppOut{{ID: a.ID}}
	attachAppLogos(ctx, q, a.TeamID, logo)
	if logo[0].LogoURL != nil {
		u := webOrigin() + *logo[0].LogoURL
		out.LogoURL = &u
	}
	return out
}

// userTokenAuth resolves the person behind the bearer and runs fn in their team: an hbu_ team user
// token holding scope want, or a CircleXO hub access token for Hosbah's app that an integration
// (Mahaam) obtained on the person's behalf (hubIntegrationCaller).
func (d *deps) userTokenAuth(ctx context.Context, authHeader, want string, fn func(q *db.Queries, c mcp.Caller, m *tenancy.Membership) error) error {
	tok := bearerToken(authHeader)
	if tok == "" {
		return huma.Error401Unauthorized("send a Hosbah user token (hbu_…) or a CircleXO access token as Authorization: Bearer")
	}
	var c mcp.Caller
	if tokens.HasPrefix(tok, tokens.UserMCP) {
		var err error
		c, err = mcp.AuthenticateUserToken(ctx, d.a, tok)
		if errors.Is(err, mcp.ErrTeamSuspended) {
			return huma.Error403Forbidden(err.Error())
		}
		if err != nil {
			return huma.Error401Unauthorized("the user token is invalid, expired or revoked")
		}
	} else {
		hc, err := hubIntegrationCaller(ctx, d.a, tok)
		if err != nil {
			return err
		}
		c = hc
	}
	if !scopes.Has(c.Scopes, want) {
		return huma.Error403Forbidden("the token lacks the " + want + " scope")
	}
	key := c.TokenHash
	if key == "" {
		key = c.TokenID
	}
	if !appTokenCalls.allow(key) {
		return huma.Error429TooManyRequests("too many requests, try again in a minute")
	}
	return d.tx(ctx, tenancy.Scope{UserID: c.UserID, TeamID: c.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		return fn(q, c, &tenancy.Membership{TeamID: c.TeamID, UserID: c.UserID, Role: c.Role})
	})
}

// hubIntegrationCaller verifies a CircleXO hub access token the way /api/mcp does (audience =
// Hosbah's hub app, subject through circlexo_user_links, org through circlexo_org_links, the
// CURRENT team role) and also requires an act chain: the token was exchanged for an integration,
// not a person's own. With HOSBAH_INTEGRATION_ACTORS set (comma-separated hub subjects, e.g.
// Mahaam's app id), one of them must be in that chain.
func hubIntegrationCaller(ctx context.Context, a *app.App, tok string) (mcp.Caller, error) {
	c, err, ok := mcp.AuthenticateHubToken(ctx, a, tok)
	if !ok {
		return mcp.Caller{}, huma.Error401Unauthorized("send a Hosbah user token (hbu_…) or a CircleXO access token for Hosbah")
	}
	var denied *mcp.ErrDenied
	if errors.As(err, &denied) || errors.Is(err, mcp.ErrTeamSuspended) {
		return mcp.Caller{}, huma.Error403Forbidden(err.Error())
	}
	if err != nil {
		return mcp.Caller{}, huma.Error401Unauthorized("the CircleXO token is invalid or expired")
	}
	if len(c.Actors) == 0 {
		return mcp.Caller{}, huma.Error403Forbidden("this CircleXO token was not issued to an integration (it has no act chain)")
	}
	if allowed := integrationActors(); len(allowed) > 0 && !slices.ContainsFunc(c.Actors, func(s string) bool { return slices.Contains(allowed, s) }) {
		return mcp.Caller{}, huma.Error403Forbidden("this integration may not use the Hosbah integrations API")
	}
	return c, nil
}

// integrationActors is HOSBAH_INTEGRATION_ACTORS: the hub subjects allowed in an act chain.
func integrationActors() []string {
	var out []string
	for _, s := range strings.Split(brand.Getenv("INTEGRATION_ACTORS"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// appTokenAuth resolves an hba_ app token and runs fn in its team, on its one app. Unknown, revoked
// or orphaned → 401. The token is looked up by hash alone (app_tokens_token_select), then the
// transaction switches to its team, as the product-key API does.
func (d *deps) appTokenAuth(ctx context.Context, authHeader string, fn func(q *db.Queries, t db.AppToken, a db.App) error) error {
	tok := bearerToken(authHeader)
	if tok == "" {
		return huma.Error401Unauthorized("send the app token (hba_…) as Authorization: Bearer")
	}
	if !tokens.HasPrefix(tok, tokens.App) {
		return huma.Error401Unauthorized("this endpoint takes an app token (hba_…)")
	}
	hash := tokens.Hash(tok)
	if !appTokenCalls.allow(hash) {
		return huma.Error429TooManyRequests("too many requests, try again in a minute")
	}
	return d.tx(ctx, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
		t, err := q.AppTokenByHash(ctx, hash)
		if err != nil || t.RevokedAt != nil {
			return huma.Error401Unauthorized("the app token is invalid or revoked")
		}
		if err := tenancy.SetTeam(ctx, tx, t.TeamID); err != nil {
			return err
		}
		if at, err := q.TeamSuspendedAt(ctx, t.TeamID); err != nil {
			return huma.Error401Unauthorized("the app token is invalid or revoked")
		} else if at != nil {
			return huma.Error403Forbidden("this team is suspended")
		}
		a, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: t.AppID, TeamID: t.TeamID})
		if err != nil {
			return huma.Error401Unauthorized("the app token is invalid or revoked")
		}
		_ = q.TouchAppToken(ctx, db.TouchAppTokenParams{ID: t.ID, TeamID: t.TeamID})
		return fn(q, t, a)
	})
}

func appTokenActor(t db.AppToken) deploy.Actor {
	return deploy.Actor{UserID: t.CreatedBy, Via: "integration", AppTokenID: t.ID}
}

func (d *deps) registerIntegrations(api huma.API) {
	tags := []string{"Integrations"}

	type integrationAppsOut struct {
		Body struct {
			Apps []IntegrationApp `json:"apps"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "integration-list-apps", Method: http.MethodGet, Path: IntegrationsPathPrefix + "apps",
		Summary: "The apps the person can manage in their team (Bearer hbu_… user token with read scope, or a CircleXO access token)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Authorization string `header:"Authorization"`
	}) (*integrationAppsOut, error) {
		out := &integrationAppsOut{}
		out.Body.Apps = []IntegrationApp{}
		err := d.userTokenAuth(ctx, in.Authorization, scopes.Read, func(q *db.Queries, c mcp.Caller, m *tenancy.Membership) error {
			// Only the apps the person can connect: minting an app token takes manage_apps.
			if !tenancy.Can(ctx, m, tenancy.AbilityManageApps) {
				return nil
			}
			rows, err := q.ListAppsOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list apps")
			}
			for _, r := range rows {
				a := db.App{ID: r.ID, TeamID: r.TeamID, ServerID: r.ServerID, Name: r.Name, Title: r.Title, Status: r.Status}
				out.Body.Apps = append(out.Body.Apps, integrationApp(ctx, q, a, r.ServerName))
			}
			return nil
		})
		return out, err
	})

	type integrationMintOut struct {
		Status int
		Body   struct {
			Token string         `json:"token" doc:"The app token (hba_…). Shown once; only its hash is kept."`
			App   IntegrationApp `json:"app"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "integration-create-app-token", Method: http.MethodPost, Path: IntegrationsPathPrefix + "apps/{app}/tokens",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Mint an app token for one app (Bearer hbu_… user token with write scope, or a CircleXO access token; owner or admin)",
	}, func(ctx context.Context, in *struct {
		Authorization string `header:"Authorization"`
		App           string `path:"app" maxLength:"64" doc:"App id or name"`
		Body          struct {
			Label string `json:"label" minLength:"1" maxLength:"120" doc:"Who holds it, e.g. \"Mahaam · Project\""`
		}
	}) (*integrationMintOut, error) {
		out := &integrationMintOut{Status: http.StatusCreated}
		err := d.userTokenAuth(ctx, in.Authorization, scopes.Write, func(q *db.Queries, c mcp.Caller, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return huma.Error404NotFound("no app " + in.App + " in this team")
			}
			if !tenancy.Can(ctx, m, tenancy.AbilityManageApps) {
				return huma.Error403Forbidden("your role in this team (" + m.Role + ") cannot mint app tokens; owners and admins can")
			}
			label := strings.TrimSpace(in.Body.Label)
			if label == "" {
				return huma.Error422UnprocessableEntity("label is required")
			}
			plain, hash := tokens.New(tokens.PrefixApp)
			t, err := q.InsertAppToken(ctx, db.InsertAppTokenParams{TeamID: m.TeamID, AppID: a.ID, Label: label, TokenHash: hash, CreatedBy: c.UserID})
			if err != nil {
				return huma.Error500InternalServerError("could not mint the token")
			}
			if err := tenancy.Record(ctx, q, m.TeamID, c.UserID, "app_token.created", "app", a.ID,
				map[string]any{"token_id": t.ID, "label": label, "via": "integration", "mcp_token_id": c.TokenID}); err != nil {
				return huma.Error500InternalServerError("audit failed")
			}
			out.Body.Token = plain
			out.Body.App = integrationApp(ctx, q, a, "")
			return nil
		})
		return out, err
	})

	type integrationAppOut struct {
		Body struct {
			App         IntegrationApp          `json:"app"`
			Deployments []IntegrationDeployment `json:"deployments"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "integration-get-app", Method: http.MethodGet, Path: IntegrationsPathPrefix + "app",
		Summary: "The token's app and its latest deployments (Bearer hba_… app token)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Authorization string `header:"Authorization"`
	}) (*integrationAppOut, error) {
		ctx, cancel := context.WithTimeout(ctx, appReadBudget)
		defer cancel()
		out := &integrationAppOut{}
		err := d.appTokenAuth(ctx, in.Authorization, func(q *db.Queries, t db.AppToken, a db.App) error {
			out.Body.App = integrationApp(ctx, q, a, "")
			deps, err := d.deploymentsOf(ctx, q, &tenancy.Membership{TeamID: t.TeamID}, a, 10)
			if err != nil {
				return huma.Error500InternalServerError("could not read the deployments")
			}
			out.Body.Deployments = []IntegrationDeployment{}
			for _, dep := range deps {
				out.Body.Deployments = append(out.Body.Deployments, toIntegrationDeployment(dep))
			}
			return nil
		})
		return out, err
	})

	type integrationDeployOut struct {
		Status int
		Body   struct {
			Deployment IntegrationDeployment `json:"deployment"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "integration-deploy-app", Method: http.MethodPost, Path: IntegrationsPathPrefix + "app/deployments",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Deploy the token's app from its branch (Bearer hba_… app token). A deploy still waiting is superseded.",
	}, func(ctx context.Context, in *struct {
		Authorization string `header:"Authorization"`
		Body          struct{}
	}) (*integrationDeployOut, error) {
		out := &integrationDeployOut{Status: http.StatusCreated}
		err := d.appTokenAuth(ctx, in.Authorization, func(q *db.Queries, t db.AppToken, a db.App) error {
			dep, err := d.deploys().QueueDeployment(ctx, q, t.TeamID, appTokenActor(t), a, "deploy", "", nil, "")
			if err != nil {
				return actionErr(err, "could not queue the deploy")
			}
			out.Body.Deployment = toIntegrationDeployment(toDeploymentOut(dep))
			return nil
		})
		return out, err
	})

	type integrationLinkOut struct {
		Status int
		Body   struct {
			URL       string    `json:"url" doc:"One-time sign-in link to the app's page in the panel"`
			ExpiresAt time.Time `json:"expires_at"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "integration-create-panel-session", Method: http.MethodPost, Path: IntegrationsPathPrefix + "app/panel-sessions",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "A one-time link (about 5 minutes) that signs a customer in to the app's page in the panel (Bearer hba_… app token, 30 a minute)",
	}, func(ctx context.Context, in *struct {
		Authorization string `header:"Authorization"`
		Body          struct {
			Email  string `json:"email" minLength:"3" maxLength:"200"`
			Name   string `json:"name,omitempty" maxLength:"120"`
			Locale string `json:"locale,omitempty" enum:"en,ar" default:"en"`
		}
	}) (*integrationLinkOut, error) {
		out := &integrationLinkOut{Status: http.StatusCreated}
		err := d.appTokenAuth(ctx, in.Authorization, func(q *db.Queries, t db.AppToken, a db.App) error {
			if !panelLinkMints.allow(t.ID) {
				return huma.Error429TooManyRequests("too many panel links for this token, try again in a minute")
			}
			email := strings.ToLower(strings.TrimSpace(in.Body.Email))
			if !validPanelEmail(email) {
				return huma.Error422UnprocessableEntity("enter a valid email address")
			}
			locale := in.Body.Locale
			if locale != "ar" {
				locale = "en"
			}
			raw := make([]byte, 32)
			if _, err := rand.Read(raw); err != nil {
				return huma.Error500InternalServerError("could not create the link")
			}
			secret := base64.RawURLEncoding.EncodeToString(raw)
			exp := time.Now().Add(panelLinkTTL).UTC().Truncate(time.Second)
			name := strings.TrimSpace(in.Body.Name)
			if _, err := q.InsertAppPanelLink(ctx, db.InsertAppPanelLinkParams{TeamID: t.TeamID, AppID: a.ID, AppTokenID: t.ID,
				LinkHash: hashPanelToken(secret), Email: email, Name: name, Locale: locale, ExpiresAt: exp}); err != nil {
				return huma.Error500InternalServerError("could not create the link")
			}
			if err := tenancy.Record(ctx, q, t.TeamID, t.CreatedBy, "app_panel_link.issued", "app", a.ID, map[string]any{
				"via": "integration", "app_token_id": t.ID, "label": t.Label, "email": email, "name": name, "ip": tenancy.MetaFrom(ctx).IP,
			}); err != nil {
				return huma.Error500InternalServerError("audit failed")
			}
			out.Body.URL = webOrigin() + IntegrationsPathPrefix + "panel/" + secret
			out.Body.ExpiresAt = exp
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "integration-revoke-app-token", Method: http.MethodDelete, Path: IntegrationsPathPrefix + "app/token",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Revoke the app token making this call (Bearer hba_… app token); it stops working at once",
	}, func(ctx context.Context, in *struct {
		Authorization string `header:"Authorization"`
	}) (*struct{}, error) {
		err := d.appTokenAuth(ctx, in.Authorization, func(q *db.Queries, t db.AppToken, a db.App) error {
			if _, err := q.RevokeAppToken(ctx, db.RevokeAppTokenParams{ID: t.ID, AppID: a.ID, TeamID: t.TeamID}); err != nil {
				return huma.Error500InternalServerError("could not revoke the token")
			}
			return tenancy.Record(ctx, q, t.TeamID, t.CreatedBy, "app_token.revoked", "app", a.ID,
				map[string]any{"token_id": t.ID, "label": t.Label, "via": "integration"})
		})
		return nil, err
	})

	d.registerAppTokens(api)
	d.registerPanelAppMe(api)
}

// registerAppTokens: the app settings page's token list (member tier; owner/admin revoke).
func (d *deps) registerAppTokens(api huma.API) {
	tags := []string{"Integrations"}
	type appTokensOut struct {
		Body struct {
			Tokens []AppTokenOut `json:"tokens"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-app-tokens", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}/tokens",
		Summary: "The app's integration tokens (hba_…), without their secrets. Owner or admin.", Tags: tags,
	}, func(ctx context.Context, in *AppPath) (*appTokensOut, error) {
		out := &appTokensOut{}
		out.Body.Tokens = []AppTokenOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			rows, err := q.ListAppTokens(ctx, db.ListAppTokensParams{AppID: a.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not list the tokens")
			}
			for _, r := range rows {
				out.Body.Tokens = append(out.Body.Tokens, AppTokenOut{ID: r.ID, Label: r.Label, CreatedBy: r.CreatedBy,
					CreatedByEmail: r.CreatedByEmail, CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt})
			}
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "revoke-app-token", Method: http.MethodDelete, Path: "/api/teams/{team}/apps/{app}/tokens/{token}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Revoke one of the app's integration tokens. Owner or admin.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Token string `path:"token" maxLength:"64"`
	}) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			n, err := q.RevokeAppToken(ctx, db.RevokeAppTokenParams{ID: in.Token, AppID: a.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not revoke the token")
			}
			if n == 0 {
				return errNotFound
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "app_token.revoked", "app", a.ID, map[string]any{"token_id": in.Token})
		})
		return nil, err
	})
}

// PanelAppSession is what the panel needs to open a customer's app-scoped session.
type PanelAppSession struct {
	Email    string `json:"email"`
	TeamID   string `json:"team_id"`
	TeamSlug string `json:"team_slug"`
	AppID    string `json:"app_id"`
	AppName  string `json:"app_name"`
	ServerID string `json:"server_id"`
}

// registerPanelAppMe: GET /api/me/panel-app, the panel's first call for a customer signed in
// through an app's panel link. Any other session → 404.
func (d *deps) registerPanelAppMe(api huma.API) {
	type panelAppSessionOut struct{ Body PanelAppSession }
	huma.Register(api, huma.Operation{OperationID: "get-panel-app-session", Method: http.MethodGet, Path: panelAppMePath,
		Summary: "The app a panel-link session is limited to (404 for any other session)", Tags: []string{"Integrations"},
	}, func(ctx context.Context, _ *struct{}) (*panelAppSessionOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		teamID, appID, ok := PanelAppScope(id)
		if !ok {
			return nil, errNotFound
		}
		o := &panelAppSessionOut{}
		err = d.tx(ctx, tenancy.Scope{UserID: id.ID, TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
			t, err := q.GetTeam(ctx, teamID)
			if err != nil {
				return errNotFound
			}
			a, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: appID, TeamID: teamID})
			if err != nil {
				return errNotFound
			}
			o.Body = PanelAppSession{Email: id.Email, TeamID: t.ID, TeamSlug: t.Slug, AppID: a.ID, AppName: a.Name, ServerID: a.ServerID}
			return nil
		})
		return o, err
	})
}

// PanelAppScope reads the team and app a panel-link session is limited to.
func PanelAppScope(id *auth.Identity) (teamID, appID string, ok bool) {
	if id == nil || id.Guard != PanelAppGuard {
		return "", "", false
	}
	for _, p := range id.Permissions {
		if v, found := strings.CutPrefix(p, "team:"); found {
			teamID = v
		} else if v, found := strings.CutPrefix(p, "app:"); found {
			appID = v
		}
	}
	return teamID, appID, teamID != "" && appID != ""
}

// PanelAppAllows reports whether a panel-link session (its account on panelServerID) may call
// method+path: its bootstrap (GET /api/me/panel-app), its team (GET /api/teams/{team}) and its own
// app's routes (/api/teams/{team}/apps/{app}[/…]), where {team} is the team's id or slug and {app}
// the app's id or name, and the app still runs on the account's server (panelAppSubAllowed narrows
// what it may do there). Nothing else.
func PanelAppAllows(ctx context.Context, a *app.App, id *auth.Identity, panelServerID, method, path string) bool {
	teamID, appID, ok := PanelAppScope(id)
	if !ok || a == nil || a.SQLDB == nil {
		return false
	}
	if path == panelAppMePath {
		return method == http.MethodGet || method == http.MethodHead
	}
	rest, found := strings.CutPrefix(path, "/api/teams/")
	if !found {
		return false
	}
	parts := strings.SplitN(rest, "/", 4)
	teamOnly := len(parts) == 1
	if teamOnly && method != http.MethodGet && method != http.MethodHead {
		return false
	}
	if !teamOnly && (len(parts) < 3 || parts[1] != "apps" || parts[2] == "") {
		return false
	}
	allowed := false
	_ = tenancy.Tx(ctx, a.SQLDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		t, err := q.GetTeam(ctx, teamID)
		if err != nil || (parts[0] != t.ID && parts[0] != t.Slug) {
			return nil
		}
		ap, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: appID, TeamID: teamID})
		if err != nil || ap.ServerID != panelServerID {
			return nil
		}
		if teamOnly {
			allowed = true
			return nil
		}
		if parts[2] != ap.ID && parts[2] != ap.Name {
			return nil
		}
		sub := ""
		if len(parts) == 4 {
			sub = strings.Trim(parts[3], "/")
		}
		allowed = panelAppSubAllowed(method, sub)
		return nil
	})
	return allowed
}

// panelAppSubAllowed is what a customer may do under its app (sub is the path after
// /apps/{app}): read it, and deploy, cancel, restart or roll back. Never its integration tokens, its
// deploy hook, an export of its config or a revealed secret; no settings, env or domain changes.
func panelAppSubAllowed(method, sub string) bool {
	first, _, _ := strings.Cut(sub, "/")
	switch method {
	case http.MethodGet, http.MethodHead:
		switch first {
		case "tokens", "export-template", "adopt":
			return false
		}
		return true
	case http.MethodPost:
		switch sub {
		case "deployments", "restart", "rollback":
			return true
		}
		segs := strings.Split(sub, "/")
		return len(segs) == 3 && segs[0] == "deployments" && segs[2] == "cancel"
	}
	return false
}

// panelAppMembership is inTeam's answer for a panel-link session, which has no team_members row:
// a deployer of its one team, when ref names that team and its app still runs on the account's
// server. activeAccount has already kept the session to that app's routes.
func (d *deps) panelAppMembership(ctx context.Context, q *db.Queries, tx *sql.Tx, id *auth.Identity, ref string) (db.MembershipForRefRow, bool) {
	teamID, appID, ok := PanelAppScope(id)
	if !ok {
		return db.MembershipForRefRow{}, false
	}
	st, err := q.UserAccountState(ctx, id.ID)
	if err != nil || st.PanelServerID == nil || st.DisabledAt != nil {
		return db.MembershipForRefRow{}, false
	}
	if err := tenancy.SetTeam(ctx, tx, teamID); err != nil {
		return db.MembershipForRefRow{}, false
	}
	t, err := q.GetTeam(ctx, teamID)
	if err != nil || (ref != t.ID && ref != t.Slug) {
		return db.MembershipForRefRow{}, false
	}
	ap, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: appID, TeamID: teamID})
	if err != nil || ap.ServerID != *st.PanelServerID {
		return db.MembershipForRefRow{}, false
	}
	return db.MembershipForRefRow{TeamID: t.ID, Role: tenancy.RoleDeployer, SuspendedAt: t.SuspendedAt}, true
}

// SessionIssuer starts a browser session (the auth plugin's *auth.Service).
type SessionIssuer interface {
	IssueSession(w http.ResponseWriter, id auth.Identity) (string, error)
}

// MountIntegrations adds the panel link: GET /api/integrations/panel/{link}. Public (the link is the
// credential), one use, about 5 minutes. On success the browser holds an app-scoped session and
// lands on /{locale}/panel/{server}/apps/{app}; otherwise on /{locale}/link-expired.
func MountIntegrations(r chi.Router, a *app.App, sessions SessionIssuer) {
	d := &deps{a: a}
	r.Get(IntegrationsPathPrefix+"panel/{link}", func(w http.ResponseWriter, r *http.Request) {
		d.handlePanelLink(w, r, sessions)
	})
	mountOAuthToken(r, d)
}

func (d *deps) handlePanelLink(w http.ResponseWriter, r *http.Request, sessions SessionIssuer) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	locale := "en"
	if r.URL.Query().Get("l") == "ar" {
		locale = "ar"
	}
	expired := func() { http.Redirect(w, r, "/"+locale+"/link-expired", http.StatusSeeOther) }
	secret := chi.URLParam(r, "link")
	if secret == "" || len(secret) > 100 || sessions == nil || d.a == nil || d.a.SQLDB == nil {
		expired()
		return
	}
	if !panelLinkUses.allow(panelClientIP(r)) {
		writeJSONError(w, http.StatusTooManyRequests, "too many attempts, try again in a minute")
		return
	}
	hash := hashPanelToken(secret)
	var ident auth.Identity
	var target string
	err := tenancy.Tx(ctx, d.a.SQLDB, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
		link, err := q.AppPanelLinkByHash(ctx, hash)
		if err != nil {
			return errNotFound
		}
		locale = link.Locale
		if err := tenancy.SetTeam(ctx, tx, link.TeamID); err != nil {
			return err
		}
		if n, err := q.UseAppPanelLink(ctx, db.UseAppPanelLinkParams{ID: link.ID, TeamID: link.TeamID}); err != nil || n == 0 {
			return errNotFound // used, or past its few minutes
		}
		t, err := q.AppTokenInTeam(ctx, db.AppTokenInTeamParams{ID: link.AppTokenID, TeamID: link.TeamID})
		if err != nil || t.RevokedAt != nil {
			return errNotFound
		}
		if at, err := q.TeamSuspendedAt(ctx, link.TeamID); err != nil || at != nil {
			return errNotFound
		}
		ap, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: link.AppID, TeamID: link.TeamID})
		if err != nil {
			return errNotFound
		}
		// The customer is their own account, keyed to this app ("<email>#app:<app id>"), never a
		// Hosbah user or a panel account with the same address: it holds nothing but this session.
		key := strings.ToLower(link.Email) + panelAppKeyInfix + ap.ID
		userID := ""
		acc, err := q.PanelAccountByEmail(ctx, db.PanelAccountByEmailParams{Email: key, PanelServerID: &ap.ServerID})
		switch {
		case err == nil && acc.DisabledAt != nil:
			return errNotFound
		case err == nil:
			userID = acc.ID
		case isNoRows(err):
			c, cerr := q.CreatePanelAccount(ctx, db.CreatePanelAccountParams{Email: key, PasswordHash: "",
				CreatedAt: time.Now().UTC().Format(time.RFC3339), PanelServerID: &ap.ServerID})
			if cerr != nil {
				return cerr
			}
			userID = c.ID
		default:
			return err
		}
		if err := tenancy.Record(ctx, q, link.TeamID, t.CreatedBy, "app_panel_link.used", "app", ap.ID, map[string]any{
			"via": "integration", "app_token_id": t.ID, "label": t.Label, "email": link.Email, "name": link.Name,
			"ip": panelClientIP(r), "customer_id": userID,
		}); err != nil {
			return err
		}
		ident = auth.Identity{ID: userID, Email: link.Email, Guard: PanelAppGuard, Permissions: []string{"team:" + link.TeamID, "app:" + ap.ID}}
		target = panelAppPath(locale, ap.ServerID, ap.ID)
		return nil
	})
	if err != nil || target == "" {
		expired()
		return
	}
	if _, err := sessions.IssueSession(w, ident); err != nil {
		expired()
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
