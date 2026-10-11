package api

/*
Hosbah's OAuth authorization server for integrations (MH-1153), so Mahaam (or any registered
client) can connect Hosbah apps without the person copying a token by hand. One consent can
cover several apps of one team (MH-1194: a project's web, api and mcp apps):

	GET  /oauth/authorize?response_type=code&client_id&redirect_uri&state
	     &code_challenge&code_challenge_method=S256&scope=app&ui_locales=en|ar
	     The web app's consent page (/{locale}/oauth/authorize). It needs a signed-in person
	     (CircleXO or local), lists the apps they can manage and sends them back with ?code&state,
	     or ?error&error_description&state. It talks to the two session routes below.
	GET  /api/oauth/authorize   the client's name and the person's apps, for that page
	POST /api/oauth/authorize   their answer: a redirect URL carrying the code (or the error)
	POST /oauth/token           grant_type=authorization_code, code, redirect_uri, code_verifier,
	                            client_id, client_secret (form, or HTTP Basic for the client) →
	                            {access_token: "hba_…", token_type: "Bearer", app,
	                             apps: [{access_token, app}]}; RFC 6749 errors. access_token/app
	                            are the first app's, for clients that link one.

A code is one-time, lives five minutes, is bound to its client, redirect URI and PKCE challenge
(S256 only), and names the apps picked, all of one team; each gets its own app token. Clients are registered by a platform admin
(go run ./cmd/admin oauth-client add <name> <redirect_uri>...); only the secret's hash is kept.
An unknown client or a redirect URI it did not register is never redirected to: the page shows
the error instead (RFC 6749 §4.1.2.1).
*/

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/oauthmeta"
	"github.com/fadymondy/hosbah/internal/pgtext"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

const (
	// OAuthTokenPath is the token endpoint, public (the client authenticates with its secret).
	OAuthTokenPath = "/oauth/token"
	oauthCodeTTL   = 5 * time.Minute
	oauthScopeApp  = "app"
	oauthPerMin    = 60
)

var oauthTokenCalls = newRateWindow(oauthPerMin, time.Minute)

// OAuthApp is one app the person may connect, on the consent page.
type OAuthApp struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Title      string  `json:"title"`
	Status     string  `json:"status"`
	ServerName string  `json:"server_name"`
	TeamSlug   string  `json:"team_slug"`
	TeamName   string  `json:"team_name"`
	LogoURL    *string `json:"logo_url"`
}

// oauthClient finds an active client and checks the redirect URI is one it registered, exactly.
func (d *deps) oauthClient(ctx context.Context, clientID, redirectURI string) (db.OauthClient, error) {
	var c db.OauthClient
	err := d.tx(ctx, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		c, err = q.OAuthClientByClientID(ctx, clientID)
		return err
	})
	if err != nil {
		return c, errors.New("unknown client")
	}
	allowed := oauthRedirectAllowed
	if c.Kind == oauthKindMCP {
		allowed = mcpRedirectAllowed
	}
	if redirectURI == "" || !allowed(c, redirectURI) {
		return c, errors.New("this redirect URI is not registered for the client")
	}
	return c, nil
}

func oauthRedirectAllowed(c db.OauthClient, redirectURI string) bool {
	for _, u := range strings.Split(c.RedirectUris, "\n") {
		if strings.TrimSpace(u) == redirectURI {
			return true
		}
	}
	return false
}

// oauthRedirect is redirectURI with params added to its query.
func oauthRedirect(redirectURI string, params map[string]string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// pkceS256 is the S256 code challenge of a verifier (RFC 7636 §4.2).
func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func validVerifier(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, r := range v {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r)) {
			return false
		}
	}
	return true
}

func (d *deps) registerOAuth(api huma.API) {
	tags := []string{"OAuth"}

	type oauthConsentOut struct {
		Body struct {
			Client struct {
				Name string `json:"name"`
			} `json:"client"`
			Apps []OAuthApp `json:"apps"`
			// An MCP connector (kind "mcp") is granted a team, not apps: Teams and Scopes are for it.
			Kind          string      `json:"kind" enum:"integration,mcp"`
			Teams         []OAuthTeam `json:"teams"`
			Scopes        []string    `json:"scopes" doc:"Every scope a connector can be granted"`
			DefaultScopes []string    `json:"default_scopes" doc:"The scopes pre-ticked: the ones the connector asked for, or read, deploy and write"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "oauth-consent", Method: http.MethodGet, Path: "/api/oauth/authorize",
		Summary: "The consent page's data: the client's name and the apps you can connect (owner or admin of their team)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		ClientID    string `query:"client_id" maxLength:"200"`
		RedirectURI string `query:"redirect_uri" maxLength:"2000"`
		Scope       string `query:"scope" maxLength:"500"`
		Resource    string `query:"resource" maxLength:"2000"`
	}) (*oauthConsentOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		c, err := d.oauthClient(ctx, in.ClientID, in.RedirectURI)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		out := &oauthConsentOut{}
		out.Body.Client.Name = c.Name
		out.Body.Apps = []OAuthApp{}
		out.Body.Kind = c.Kind
		out.Body.Teams = []OAuthTeam{}
		out.Body.Scopes = []string{}
		out.Body.DefaultScopes = []string{}
		if c.Kind == oauthKindMCP {
			if !oauthmeta.ResourceAllowed(in.Resource) {
				return nil, huma.Error400BadRequest("this connector asks for a resource other than Hosbah's MCP server")
			}
			out.Body.Scopes = scopes.All
			out.Body.DefaultScopes = requestedScopes(in.Scope)
			out.Body.Teams, err = d.oauthTeams(ctx, id.ID)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not list your teams")
			}
			return out, nil
		}
		var teams []db.ListTeamsForUserRow
		if err := d.tx(ctx, tenancy.Scope{UserID: id.ID}, func(q *db.Queries, _ *sql.Tx) error {
			teams, err = q.ListTeamsForUser(ctx, id.ID)
			return err
		}); err != nil {
			return nil, huma.Error500InternalServerError("could not list your teams")
		}
		for _, t := range teams {
			m := &tenancy.Membership{TeamID: t.ID, UserID: id.ID, Role: t.Role}
			if t.SuspendedAt != nil || !tenancy.Can(ctx, m, tenancy.AbilityManageApps) {
				continue
			}
			err := d.tx(ctx, tenancy.Scope{UserID: id.ID, TeamID: t.ID}, func(q *db.Queries, _ *sql.Tx) error {
				rows, err := q.ListAppsOfTeam(ctx, t.ID)
				if err != nil {
					return err
				}
				logos := make([]AppOut, len(rows))
				for i, r := range rows {
					logos[i] = AppOut{ID: r.ID}
				}
				attachAppLogos(ctx, q, t.ID, logos)
				for i, r := range rows {
					title := r.Name
					if r.Title != nil && strings.TrimSpace(*r.Title) != "" {
						title = *r.Title
					}
					out.Body.Apps = append(out.Body.Apps, OAuthApp{ID: r.ID, Name: r.Name, Title: title, Status: r.Status,
						ServerName: r.ServerName, TeamSlug: t.Slug, TeamName: t.Name, LogoURL: logos[i].LogoURL})
				}
				return nil
			})
			if err != nil {
				return nil, huma.Error500InternalServerError("could not list apps")
			}
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{OperationID: "oauth-authorize", Method: http.MethodPost, Path: "/api/oauth/authorize",
		Summary: "Answer a client's authorization request: allow it apps of one team (owner or admin), or deny", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Body struct {
			ResponseType        string `json:"response_type" maxLength:"40"`
			ClientID            string `json:"client_id" maxLength:"200"`
			RedirectURI         string `json:"redirect_uri" maxLength:"2000"`
			State               string `json:"state,omitempty" maxLength:"1000"`
			CodeChallenge       string `json:"code_challenge,omitempty" maxLength:"200"`
			CodeChallengeMethod string `json:"code_challenge_method,omitempty" maxLength:"20"`
			Scope               string `json:"scope,omitempty" maxLength:"200"`
			Team                string `json:"team,omitempty" maxLength:"64" doc:"The app's team (id or slug)"`
			App                 string   `json:"app,omitempty" maxLength:"64" doc:"The app to connect (id); or apps"`
			Apps                []string `json:"apps,omitempty" maxItems:"20" doc:"The apps to connect (ids), all of the team"`
			Scopes              []string `json:"scopes,omitempty" maxItems:"5" doc:"MCP connector: the scopes granted"`
			Resource            string   `json:"resource,omitempty" maxLength:"2000" doc:"MCP connector: the RFC 8707 resource indicator"`
			Allow               bool   `json:"allow"`
		}
	}) (*oauthDecisionOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		b := in.Body
		client, err := d.oauthClient(ctx, b.ClientID, b.RedirectURI)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		out := &oauthDecisionOut{}
		fail := func(code, desc string) (*oauthDecisionOut, error) {
			out.Body.Redirect = oauthRedirect(b.RedirectURI, map[string]string{"error": code, "error_description": desc, "state": b.State})
			return out, nil
		}
		switch {
		case b.ResponseType != "code":
			return fail("unsupported_response_type", "only response_type=code is supported")
		case b.CodeChallenge == "" || b.CodeChallengeMethod != "S256":
			return fail("invalid_request", "PKCE is required: code_challenge with code_challenge_method=S256")
		case client.Kind != oauthKindMCP && b.Scope != "" && b.Scope != oauthScopeApp:
			return fail("invalid_scope", "the only scope is \"app\"")
		case client.Kind == oauthKindMCP && !oauthmeta.ResourceAllowed(b.Resource):
			return fail("invalid_target", "the only resource is Hosbah's MCP server, "+oauthmeta.ResourcePath)
		case !b.Allow:
			return fail("access_denied", "the person declined")
		}
		if client.Kind == oauthKindMCP {
			return d.authorizeMCP(ctx, id.ID, client, b.Team, b.Scopes, b.RedirectURI, b.CodeChallenge, b.State)
		}
		picked := oauthPicked(b.App, b.Apps)
		if b.Team == "" || len(picked) == 0 {
			return nil, huma.Error422UnprocessableEntity("pick an app")
		}
		plain, hash := tokens.New(tokens.PrefixOAuthCode)
		err = d.inTeam(ctx, b.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			for _, app := range picked {
				if _, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: app, TeamID: m.TeamID}); err != nil {
					return huma.Error404NotFound("no such app in this team")
				}
			}
			if _, err := q.InsertOAuthCode(ctx, db.InsertOAuthCodeParams{CodeHash: hash, ClientID: client.ID, TeamID: m.TeamID, AppID: picked[0],
				AppIds: pgtext.Array(picked), UserID: id.ID, RedirectUri: b.RedirectURI, CodeChallenge: b.CodeChallenge,
				ExpiresAt: time.Now().Add(oauthCodeTTL)}); err != nil {
				return huma.Error500InternalServerError("could not issue the code")
			}
			for _, app := range picked {
				if err := tenancy.Record(ctx, q, m.TeamID, id.ID, "oauth.authorized", "app", app, map[string]any{"client": client.Name, "client_id": client.ClientID}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		out.Body.Redirect = oauthRedirect(b.RedirectURI, map[string]string{"code": plain, "state": b.State})
		return out, nil
	})
}

type oauthDecisionOut struct {
	Body struct {
		Redirect string `json:"redirect" doc:"Where to send the browser: the client's redirect URI with code and state, or error"`
	}
}

// oauthPicked is the apps of an answer, in order and once each: apps, or the single app.
func oauthPicked(app string, apps []string) []string {
	if len(apps) == 0 && app != "" {
		apps = []string{app}
	}
	out := make([]string, 0, len(apps))
	seen := map[string]bool{}
	for _, a := range apps {
		if a = strings.TrimSpace(a); a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// oauthTokenApp is one app of a token response, with its own app token.
type oauthTokenApp struct {
	AccessToken string         `json:"access_token"`
	App         IntegrationApp `json:"app"`
}

// oauthError writes an RFC 6749 §5.2 error.
func oauthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="hosbah"`)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": desc})
}

// mountOAuthToken registers POST /oauth/token (see the top of this file).
func mountOAuthToken(r chi.Router, d *deps) {
	r.Post(OAuthTokenPath, d.handleOAuthToken)
}

func (d *deps) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if d.a == nil || d.a.SQLDB == nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "the server is not ready")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "send the parameters as application/x-www-form-urlencoded")
		return
	}
	f := r.PostForm
	oauthCORS(w)
	clientKey := f.Get("client_id")
	if u, _, ok := r.BasicAuth(); ok {
		clientKey, _ = url.QueryUnescape(u)
	}
	if !oauthTokenCalls.allow(panelClientIP(r) + "|" + clientKey) {
		oauthError(w, http.StatusTooManyRequests, "slow_down", "too many requests, try again in a minute")
		return
	}
	if g := f.Get("grant_type"); g != "authorization_code" && g != "refresh_token" {
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
		return
	}
	client, ok := d.authenticateOAuthClient(ctx, r, f)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client or wrong client secret")
		return
	}
	// An MCP connector gets a team MCP token (mcp_oauth.go); an integration client an app token.
	if client.Kind == oauthKindMCP {
		d.mcpGrant(w, r, client, f)
		return
	}
	if f.Get("grant_type") != "authorization_code" {
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "only grant_type=authorization_code is supported")
		return
	}
	code, redirectURI, verifier := f.Get("code"), f.Get("redirect_uri"), f.Get("code_verifier")
	if code == "" || redirectURI == "" || verifier == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "code, redirect_uri and code_verifier are required")
		return
	}
	if !validVerifier(verifier) {
		oauthError(w, http.StatusBadRequest, "invalid_request", "code_verifier must be 43 to 128 unreserved characters")
		return
	}
	invalid := func() { oauthError(w, http.StatusBadRequest, "invalid_grant", "the code is invalid, expired or already used") }

	hash := tokens.Hash(code)
	var out []oauthTokenApp
	err := d.tx(ctx, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
		c, err := q.OAuthCodeByHash(ctx, hash)
		if err != nil || c.ClientID != client.ID {
			return errOAuthGrant
		}
		if err := tenancy.SetTeam(ctx, tx, c.TeamID); err != nil {
			return err
		}
		// Spent first, so a code that fails any check below is still gone.
		if n, err := q.UseOAuthCode(ctx, db.UseOAuthCodeParams{ID: c.ID, TeamID: c.TeamID}); err != nil || n != 1 {
			return errOAuthGrant
		}
		if c.RedirectUri != redirectURI ||
			subtle.ConstantTimeCompare([]byte(pkceS256(verifier)), []byte(c.CodeChallenge)) != 1 {
			return errOAuthGrantSpent
		}
		// The person must still be able to connect the app now, not just when they consented.
		m, err := q.MembershipFor(ctx, db.MembershipForParams{TeamID: c.TeamID, UserID: c.UserID})
		if err != nil || !tenancy.Can(ctx, &tenancy.Membership{TeamID: c.TeamID, UserID: c.UserID, Role: m.Role}, tenancy.AbilityManageApps) {
			return errOAuthGrantSpent
		}
		if u, err := q.UserByID(ctx, c.UserID); err != nil || u.DisabledAt != nil {
			return errOAuthGrantSpent
		}
		if at, err := q.TeamSuspendedAt(ctx, c.TeamID); err != nil || at != nil {
			return errOAuthGrantSpent
		}
		ids := []string(c.AppIds)
		if len(ids) == 0 {
			ids = []string{c.AppID}
		}
		for _, id := range ids {
			// Every app must still exist in the team; one gone fails the whole exchange.
			a, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: id, TeamID: c.TeamID})
			if err != nil {
				return errOAuthGrantSpent
			}
			plain, h := tokens.New(tokens.PrefixApp)
			t, err := q.InsertAppToken(ctx, db.InsertAppTokenParams{TeamID: c.TeamID, AppID: a.ID, Label: client.Name, TokenHash: h, CreatedBy: c.UserID})
			if err != nil {
				return err
			}
			if err := tenancy.Record(ctx, q, c.TeamID, c.UserID, "app_token.created", "app", a.ID,
				map[string]any{"token_id": t.ID, "label": client.Name, "via": "oauth", "client_id": client.ClientID}); err != nil {
				return err
			}
			out = append(out, oauthTokenApp{AccessToken: plain, App: integrationApp(ctx, q, a, "")})
		}
		return nil
	})
	switch {
	case errors.Is(err, errOAuthGrant):
		invalid()
		return
	case errors.Is(err, errOAuthGrantSpent):
		// The code was spent in its own transaction step above, but the whole transaction rolls
		// back on error: spend it again, outside, so a failed exchange still burns the code.
		d.burnOAuthCode(ctx, hash)
		invalid()
		return
	case err != nil:
		oauthError(w, http.StatusInternalServerError, "server_error", "could not issue the token")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": out[0].AccessToken, "token_type": "Bearer", "app": out[0].App, "apps": out})
}

var (
	errOAuthGrant      = errors.New("invalid grant")
	errOAuthGrantSpent = errors.New("invalid grant (spend the code)")
)

// burnOAuthCode spends a code whose exchange failed after it was found, so it cannot be retried.
func (d *deps) burnOAuthCode(ctx context.Context, hash string) {
	_ = d.tx(ctx, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
		c, err := q.OAuthCodeByHash(ctx, hash)
		if err != nil {
			return err
		}
		if err := tenancy.SetTeam(ctx, tx, c.TeamID); err != nil {
			return err
		}
		_, err = q.UseOAuthCode(ctx, db.UseOAuthCodeParams{ID: c.ID, TeamID: c.TeamID})
		return err
	})
}
