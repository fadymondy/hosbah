package api

/*
OAuth 2.1 for MCP connectors (claude.ai, Claude Code, ChatGPT, Cursor, …), following the MCP
authorization spec. The protected resource is the team MCP at /api/mcp; Hosbah is its own
authorization server. All of it is anonymous HTTP except the consent page, which needs a session.

	GET  /.well-known/oauth-protected-resource[/api/mcp]   RFC 9728 (the 401 on /api/mcp points here)
	GET  /.well-known/oauth-authorization-server[/api/mcp] RFC 8414
	POST /oauth/register   RFC 7591 dynamic client registration: kind 'mcp' clients, public
	                       (token_endpoint_auth_method none) unless they ask for a secret
	GET  /oauth/authorize  the web app's consent page: the person picks ONE team (default: the
	                       one with the most servers and apps) and the scopes, never creating a team
	                       unless they have none and say so (POST /api/teams, from the page)
	POST /oauth/token      authorization_code (PKCE S256, resource indicator) and refresh_token:
	                       {access_token "hbu_…" (1 hour), refresh_token "hbf_…" (90 days, rotated on
	                       every use), expires_in, scope}
	POST /oauth/revoke     RFC 7009, the access or the refresh token: the connection is revoked

A connection is one mcp_tokens row (oauth_client_id set), bound to one team like every team MCP
token, so the tenancy invariant is unchanged; refreshing rotates the row's hashes in place, so the
tokens page and the account's "AI connections" show one row per connection, revocable there too.
To use another team a person connects again and picks it (the MCP tool list_teams says so).
*/

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/oauthmeta"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

const (
	mcpAccessTTL  = time.Hour
	mcpRefreshTTL = 90 * 24 * time.Hour
	oauthKindMCP  = "mcp"
	// A connector's client_id; a refresh token. The access token is an ordinary hbu_ team token.
	prefixMCPClient  = "hbc_"
	prefixMCPRefresh = "hbf_"
	prefixMCPCode    = "hbm_"
)

// mcpDefaultScopes are pre-ticked on the consent page when a connector asks for none.
var mcpDefaultScopes = []string{scopes.Read, scopes.Deploy, scopes.Write}

// Anonymous registration and revocation, so each is rate-limited (per client address).
var (
	oauthRegisterCalls = newRateWindow(30, time.Minute)
	oauthRevokeCalls   = newRateWindow(60, time.Minute)
)

// MountMCPOAuth adds the discovery documents, registration and revocation (see the top of this file).
// The token endpoint is shared with the integration clients (oauth.go).
func MountMCPOAuth(r chi.Router, d *deps) {
	for _, p := range []string{oauthmeta.ProtectedResourcePath, oauthmeta.ProtectedResourcePath + oauthmeta.ResourcePath} {
		r.Get(p, d.handleProtectedResource)
		r.Options(p, oauthPreflight)
	}
	for _, p := range []string{oauthmeta.AuthorizationServerPath, oauthmeta.AuthorizationServerPath + oauthmeta.ResourcePath} {
		r.Get(p, d.handleAuthorizationServer)
		r.Options(p, oauthPreflight)
	}
	r.Post(oauthmeta.RegisterPath, d.handleRegister)
	r.Options(oauthmeta.RegisterPath, oauthPreflight)
	r.Post(oauthmeta.RevokePath, d.handleRevoke)
	r.Options(oauthmeta.RevokePath, oauthPreflight)
	r.Options(oauthmeta.TokenPath, oauthPreflight)
}

// oauthCORS: these endpoints take no cookies, so any origin may call them (browser-based MCP clients).
func oauthCORS(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
	h.Set("Access-Control-Max-Age", "86400")
}

func oauthPreflight(w http.ResponseWriter, _ *http.Request) {
	oauthCORS(w)
	w.WriteHeader(http.StatusNoContent)
}

func writeOAuthJSON(w http.ResponseWriter, status int, v any) {
	oauthCORS(w)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (d *deps) handleProtectedResource(w http.ResponseWriter, r *http.Request) {
	o := oauthmeta.Origin(r)
	writeOAuthJSON(w, http.StatusOK, map[string]any{
		"resource":                 oauthmeta.Resource(r),
		"authorization_servers":    []string{o},
		"scopes_supported":         scopes.All,
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Hosbah",
	})
}

func (d *deps) handleAuthorizationServer(w http.ResponseWriter, r *http.Request) {
	o := oauthmeta.Origin(r)
	auth := []string{"none", "client_secret_post", "client_secret_basic"}
	writeOAuthJSON(w, http.StatusOK, map[string]any{
		"issuer":                                     o,
		"authorization_endpoint":                     o + oauthmeta.AuthorizePath,
		"token_endpoint":                             o + oauthmeta.TokenPath,
		"registration_endpoint":                      o + oauthmeta.RegisterPath,
		"revocation_endpoint":                        o + oauthmeta.RevokePath,
		"response_types_supported":                   []string{"code"},
		"response_modes_supported":                   []string{"query"},
		"grant_types_supported":                      []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":           []string{"S256"},
		"token_endpoint_auth_methods_supported":      auth,
		"revocation_endpoint_auth_methods_supported": auth,
		"scopes_supported":                           scopes.All,
		"ui_locales_supported":                       []string{"en", "ar"},
		"service_documentation":                      o + "/docs",
	})
}

// --- dynamic client registration (RFC 7591) ---

type dcrRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	Scope                   string   `json:"scope"`
}

func dcrError(w http.ResponseWriter, code, desc string) {
	writeOAuthJSON(w, http.StatusBadRequest, map[string]string{"error": code, "error_description": desc})
}

func (d *deps) handleRegister(w http.ResponseWriter, r *http.Request) {
	if d.a == nil || d.a.SQLDB == nil {
		writeOAuthJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "temporarily_unavailable"})
		return
	}
	if !oauthRegisterCalls.allow(panelClientIP(r)) {
		writeOAuthJSON(w, http.StatusTooManyRequests, map[string]string{"error": "slow_down", "error_description": "too many registrations, try again in a minute"})
		return
	}
	var in dcrRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in); err != nil {
		dcrError(w, "invalid_client_metadata", "send the client metadata as a JSON object")
		return
	}
	if len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > 10 {
		dcrError(w, "invalid_redirect_uri", "redirect_uris needs 1 to 10 URIs")
		return
	}
	for _, u := range in.RedirectURIs {
		if err := validRedirectURI(u); err != nil {
			dcrError(w, "invalid_redirect_uri", err.Error())
			return
		}
	}
	for _, g := range in.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			dcrError(w, "invalid_client_metadata", "grant_types may only be authorization_code and refresh_token")
			return
		}
	}
	for _, rt := range in.ResponseTypes {
		if rt != "code" {
			dcrError(w, "invalid_client_metadata", "response_types may only be code")
			return
		}
	}
	method := in.TokenEndpointAuthMethod
	switch method {
	case "":
		method = "none"
	case "none", "client_secret_post", "client_secret_basic":
	default:
		dcrError(w, "invalid_client_metadata", "token_endpoint_auth_method must be none, client_secret_post or client_secret_basic")
		return
	}
	name := cleanClientName(in.ClientName)

	clientID, _ := tokens.New(prefixMCPClient)
	var secret string
	var secretHash *string
	if method != "none" {
		var h string
		secret, h = tokens.New(tokens.PrefixOAuthSecret)
		secretHash = &h
	}
	var c db.OauthClient
	err := d.tx(r.Context(), tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		c, err = q.InsertMcpOAuthClient(r.Context(), db.InsertMcpOAuthClientParams{ClientID: clientID, Name: name,
			SecretHash: secretHash, RedirectUris: strings.Join(in.RedirectURIs, "\n")})
		return err
	})
	if err != nil {
		writeOAuthJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error", "error_description": "could not register the client"})
		return
	}
	out := map[string]any{
		"client_id":                  c.ClientID,
		"client_id_issued_at":        c.CreatedAt.Unix(),
		"client_name":                c.Name,
		"redirect_uris":              in.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": method,
	}
	if secret != "" {
		out["client_secret"] = secret
		out["client_secret_expires_at"] = 0
	}
	writeOAuthJSON(w, http.StatusCreated, out)
}

// cleanClientName is what the consent page shows: printable, at most 80 characters, never empty.
func cleanClientName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if rs := []rune(s); len(rs) > 80 {
		s = strings.TrimSpace(string(rs[:80]))
	}
	if s == "" {
		return "MCP client"
	}
	return s
}

// validRedirectURI: an absolute URI without a fragment; https, http only to a loopback address
// (RFC 8252 §7.3, native apps such as Claude Code), or a native app's private-use scheme.
func validRedirectURI(raw string) error {
	if len(raw) > 2000 {
		return errors.New("a redirect URI is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Fragment != "" {
		return errors.New("each redirect URI must be absolute, without a fragment")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		if u.Host == "" {
			return errors.New("an https redirect URI needs a host")
		}
	case "http":
		if !isLoopback(u.Hostname()) {
			return errors.New("http redirect URIs are only allowed to localhost / a loopback address")
		}
	case "javascript", "data", "file", "vbscript", "about", "blob":
		return errors.New("that redirect URI scheme is not allowed")
	}
	return nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// mcpRedirectAllowed is exact matching, except that a loopback redirect may use any port
// (RFC 8252 §7.3: a native app picks a free port each time).
func mcpRedirectAllowed(c db.OauthClient, redirectURI string) bool {
	if oauthRedirectAllowed(c, redirectURI) {
		return true
	}
	got, err := url.Parse(redirectURI)
	if err != nil || got.Scheme != "http" || !isLoopback(got.Hostname()) {
		return false
	}
	for _, reg := range strings.Split(c.RedirectUris, "\n") {
		want, err := url.Parse(strings.TrimSpace(reg))
		if err == nil && want.Scheme == "http" && want.Hostname() == got.Hostname() &&
			want.Path == got.Path && want.RawQuery == got.RawQuery {
			return true
		}
	}
	return false
}

// --- token endpoint: the MCP grants ---

type mcpTokenOut struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// mcpGrant answers /oauth/token for an authenticated 'mcp' client.
func (d *deps) mcpGrant(w http.ResponseWriter, r *http.Request, client db.OauthClient, f url.Values) {
	if !oauthmeta.ResourceAllowed(f.Get("resource")) {
		oauthError(w, http.StatusBadRequest, "invalid_target", "the only resource is "+oauthmeta.Resource(r))
		return
	}
	var out mcpTokenOut
	var err error
	switch f.Get("grant_type") {
	case "authorization_code":
		out, err = d.mcpCodeGrant(r.Context(), client, f)
	case "refresh_token":
		out, err = d.mcpRefreshGrant(r.Context(), client, f)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
		return
	}
	var oe *oauthErr
	switch {
	case errors.As(err, &oe):
		oauthError(w, http.StatusBadRequest, oe.code, oe.desc)
		return
	case err != nil:
		oauthError(w, http.StatusInternalServerError, "server_error", "could not issue the token")
		return
	}
	oauthCORS(w)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	_ = json.NewEncoder(w).Encode(out)
}

type oauthErr struct{ code, desc string }

func (e *oauthErr) Error() string { return e.code + ": " + e.desc }

var errMCPGrant = &oauthErr{"invalid_grant", "the code or refresh token is invalid, expired or already used"}

// errMCPGrantSpent: the code was found but fails a check; the exchange burns it outside the
// (rolled back) transaction, as the integration grant does.
var errMCPGrantSpent = errors.New("invalid grant (spend the code)")

func (d *deps) mcpCodeGrant(ctx context.Context, client db.OauthClient, f url.Values) (mcpTokenOut, error) {
	code, redirectURI, verifier := f.Get("code"), f.Get("redirect_uri"), f.Get("code_verifier")
	if code == "" || redirectURI == "" || verifier == "" {
		return mcpTokenOut{}, &oauthErr{"invalid_request", "code, redirect_uri and code_verifier are required"}
	}
	if !validVerifier(verifier) {
		return mcpTokenOut{}, &oauthErr{"invalid_request", "code_verifier must be 43 to 128 unreserved characters"}
	}
	hash := tokens.Hash(code)
	var out mcpTokenOut
	err := d.tx(ctx, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
		c, err := q.McpOAuthCodeByHash(ctx, hash)
		if err != nil || c.ClientID != client.ID {
			return errMCPGrant
		}
		if err := tenancy.SetTeam(ctx, tx, c.TeamID); err != nil {
			return err
		}
		if n, err := q.UseMcpOAuthCode(ctx, db.UseMcpOAuthCodeParams{ID: c.ID, TeamID: c.TeamID}); err != nil || n != 1 {
			return errMCPGrant
		}
		if c.RedirectUri != redirectURI || subtle.ConstantTimeCompare([]byte(pkceS256(verifier)), []byte(c.CodeChallenge)) != 1 {
			return errMCPGrantSpent
		}
		if !d.mayConnect(ctx, q, c.TeamID, c.UserID) {
			return errMCPGrantSpent
		}
		granted := scopes.Parse(c.Scopes)
		access, accessHash := tokens.New(tokens.PrefixUserMCP)
		refresh, refreshHash := tokens.New(prefixMCPRefresh)
		now := time.Now().UTC()
		exp, rexp := now.Add(mcpAccessTTL), now.Add(mcpRefreshTTL)
		t, err := q.InsertOAuthMcpToken(ctx, db.InsertOAuthMcpTokenParams{TeamID: c.TeamID, UserID: c.UserID, Name: client.Name,
			TokenHash: accessHash, Scopes: scopes.Join(granted), ExpiresAt: &exp, OauthClientID: &client.ID,
			RefreshHash: &refreshHash, RefreshExpiresAt: &rexp})
		if err != nil {
			return err
		}
		if err := tenancy.Record(ctx, q, c.TeamID, c.UserID, "mcp_token.created", "mcp_token", t.ID,
			map[string]any{"name": t.Name, "scopes": granted, "via": "oauth", "client_id": client.ClientID}); err != nil {
			return err
		}
		out = mcpTokenOut{AccessToken: access, TokenType: "Bearer", ExpiresIn: int(mcpAccessTTL.Seconds()),
			RefreshToken: refresh, Scope: strings.Join(granted, " ")}
		return nil
	})
	if errors.Is(err, errMCPGrantSpent) {
		d.burnMCPCode(ctx, hash)
		return mcpTokenOut{}, errMCPGrant
	}
	return out, err
}

func (d *deps) burnMCPCode(ctx context.Context, hash string) {
	_ = d.tx(ctx, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
		c, err := q.McpOAuthCodeByHash(ctx, hash)
		if err != nil {
			return err
		}
		if err := tenancy.SetTeam(ctx, tx, c.TeamID); err != nil {
			return err
		}
		_, err = q.UseMcpOAuthCode(ctx, db.UseMcpOAuthCodeParams{ID: c.ID, TeamID: c.TeamID})
		return err
	})
}

// mayConnect: the person can still hold a connector token for the team, now (not only when they
// consented): an owner or admin (who may mint tokens), account enabled, team not suspended.
func (d *deps) mayConnect(ctx context.Context, q *db.Queries, teamID, userID string) bool {
	m, err := q.MembershipFor(ctx, db.MembershipForParams{TeamID: teamID, UserID: userID})
	if err != nil || !tenancy.Can(ctx, &tenancy.Membership{TeamID: teamID, UserID: userID, Role: m.Role}, tenancy.AbilityManageTokens) {
		return false
	}
	if u, err := q.UserByID(ctx, userID); err != nil || u.DisabledAt != nil {
		return false
	}
	at, err := q.TeamSuspendedAt(ctx, teamID)
	return err == nil && at == nil
}

func (d *deps) mcpRefreshGrant(ctx context.Context, client db.OauthClient, f url.Values) (mcpTokenOut, error) {
	refresh := f.Get("refresh_token")
	if refresh == "" {
		return mcpTokenOut{}, &oauthErr{"invalid_request", "refresh_token is required"}
	}
	hash := tokens.Hash(refresh)
	var out mcpTokenOut
	err := d.tx(ctx, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
		t, err := q.McpTokenByRefreshHash(ctx, &hash)
		if err != nil || t.OauthClientID == nil || *t.OauthClientID != client.ID || t.RevokedAt != nil ||
			t.RefreshExpiresAt == nil || time.Now().After(*t.RefreshExpiresAt) {
			return errMCPGrant
		}
		if err := tenancy.SetTeam(ctx, tx, t.TeamID); err != nil {
			return err
		}
		if !d.mayConnect(ctx, q, t.TeamID, t.UserID) {
			return errMCPGrant
		}
		granted := scopes.Parse(t.Scopes)
		if s := strings.Fields(f.Get("scope")); len(s) > 0 {
			for _, want := range s {
				if !scopes.Has(granted, want) {
					return &oauthErr{"invalid_scope", "a refresh cannot add scopes; connect again to grant " + want}
				}
			}
		}
		access, accessHash := tokens.New(tokens.PrefixUserMCP)
		next, nextHash := tokens.New(prefixMCPRefresh)
		now := time.Now().UTC()
		exp, rexp := now.Add(mcpAccessTTL), now.Add(mcpRefreshTTL)
		n, err := q.RotateMcpToken(ctx, db.RotateMcpTokenParams{ID: t.ID, RefreshHash: &hash, TokenHash: accessHash,
			ExpiresAt: &exp, RefreshHash_2: &nextHash, RefreshExpiresAt: &rexp})
		if err != nil {
			return err
		}
		if n != 1 {
			return errMCPGrant // a racing refresh already rotated it
		}
		out = mcpTokenOut{AccessToken: access, TokenType: "Bearer", ExpiresIn: int(mcpAccessTTL.Seconds()),
			RefreshToken: next, Scope: strings.Join(granted, " ")}
		return nil
	})
	return out, err
}

// --- revocation (RFC 7009) ---

func (d *deps) handleRevoke(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	oauthCORS(w)
	if d.a == nil || d.a.SQLDB == nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "the server is not ready")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "send the parameters as application/x-www-form-urlencoded")
		return
	}
	if !oauthRevokeCalls.allow(panelClientIP(r)) {
		oauthError(w, http.StatusTooManyRequests, "slow_down", "too many requests, try again in a minute")
		return
	}
	client, ok := d.authenticateOAuthClient(ctx, r, r.PostForm)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client or wrong client secret")
		return
	}
	if tok := r.PostForm.Get("token"); tok != "" && client.Kind == oauthKindMCP {
		d.revokeMCPToken(ctx, client, tok)
	}
	// RFC 7009 §2.2: 200 whether or not the token was valid.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

// revokeMCPToken revokes the connection a presented access or refresh token belongs to, if it was
// issued to this client.
func (d *deps) revokeMCPToken(ctx context.Context, client db.OauthClient, presented string) {
	hash := tokens.Hash(presented)
	_ = d.tx(ctx, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
		t, err := q.McpTokenByRefreshHash(ctx, &hash)
		if err != nil {
			if t, err = q.McpTokenByHash(ctx, hash); err != nil {
				return err
			}
		}
		if t.OauthClientID == nil || *t.OauthClientID != client.ID {
			return nil
		}
		if err := tenancy.SetTeam(ctx, tx, t.TeamID); err != nil {
			return err
		}
		if n, err := q.RevokeMcpToken(ctx, db.RevokeMcpTokenParams{ID: t.ID, TeamID: t.TeamID}); err != nil || n == 0 {
			return err
		}
		return tenancy.Record(ctx, q, t.TeamID, t.UserID, "mcp_token.revoked", "mcp_token", t.ID,
			map[string]any{"via": "oauth", "client_id": client.ClientID})
	})
}

// authenticateOAuthClient finds the client of a token or revocation request: client_id and
// client_secret in the form or HTTP Basic. A public client (no secret registered) needs only its id.
func (d *deps) authenticateOAuthClient(ctx context.Context, r *http.Request, f url.Values) (db.OauthClient, bool) {
	clientID, secret := f.Get("client_id"), f.Get("client_secret")
	if u, p, ok := r.BasicAuth(); ok {
		clientID, _ = url.QueryUnescape(u)
		secret, _ = url.QueryUnescape(p)
	}
	if clientID == "" {
		return db.OauthClient{}, false
	}
	var client db.OauthClient
	err := d.tx(ctx, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		client, err = q.OAuthClientByClientID(ctx, clientID)
		return err
	})
	if err != nil {
		return client, false
	}
	if client.SecretHash == nil {
		return client, client.Kind == oauthKindMCP
	}
	return client, secret != "" && subtle.ConstantTimeCompare([]byte(tokens.Hash(secret)), []byte(*client.SecretHash)) == 1
}

// --- the consent page (GET/POST /api/oauth/authorize, oauth.go) ---

// OAuthTeam is one team on an MCP connector's consent page.
type OAuthTeam struct {
	ID      string `json:"id"`
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	Servers int64  `json:"servers"`
	Apps    int64  `json:"apps"`
	// CanConnect: the person may hold a connector token for it (owner or admin, team not suspended).
	CanConnect bool `json:"can_connect"`
	// Default is the pre-selected team: the connectable one with the most servers and apps (the
	// oldest on a tie), so an empty team made at sign-up never wins over the one with the work in it.
	Default bool `json:"default"`
}

// oauthTeams lists every team of the person, with what lives in it and the default marked.
func (d *deps) oauthTeams(ctx context.Context, userID string) ([]OAuthTeam, error) {
	var rows []db.ListTeamsForUserRow
	if err := d.tx(ctx, tenancy.Scope{UserID: userID}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		rows, err = q.ListTeamsForUser(ctx, userID)
		return err
	}); err != nil {
		return nil, err
	}
	out := make([]OAuthTeam, 0, len(rows))
	best := -1
	for _, t := range rows {
		ot := OAuthTeam{ID: t.ID, Slug: t.Slug, Name: t.Name, Role: t.Role}
		ot.CanConnect = t.SuspendedAt == nil &&
			tenancy.Can(ctx, &tenancy.Membership{TeamID: t.ID, UserID: userID, Role: t.Role}, tenancy.AbilityManageTokens)
		if err := d.tx(ctx, tenancy.Scope{UserID: userID, TeamID: t.ID}, func(q *db.Queries, _ *sql.Tx) error {
			f, err := q.CountTeamFootprint(ctx, t.ID)
			ot.Servers, ot.Apps = f.Servers, f.Apps
			return err
		}); err != nil {
			return nil, err
		}
		out = append(out, ot)
		if ot.CanConnect && (best < 0 || ot.Servers+ot.Apps > out[best].Servers+out[best].Apps) {
			best = len(out) - 1
		}
	}
	if best >= 0 {
		out[best].Default = true
	}
	return out, nil
}

// requestedScopes are the known scopes in a connector's space-separated scope parameter, or the
// defaults when it named none of them.
func requestedScopes(scope string) []string {
	got, _ := scopes.Normalize(strings.Fields(scope))
	if len(got) == 0 {
		return mcpDefaultScopes
	}
	return got
}

// authorizeMCP issues the code for an MCP connector: bound to the ONE team the person picked and
// the scopes they ticked. It never creates a team (a person with none creates one on the page first).
func (d *deps) authorizeMCP(ctx context.Context, userID string, client db.OauthClient, team string, picked []string,
	redirectURI, challenge, state string) (*oauthDecisionOut, error) {
	granted, ok := scopes.Normalize(picked)
	if !ok || len(granted) == 0 {
		return nil, huma.Error422UnprocessableEntity("pick at least one permission")
	}
	if strings.TrimSpace(team) == "" {
		return nil, huma.Error422UnprocessableEntity("pick a team")
	}
	plain, hash := tokens.New(prefixMCPCode)
	err := d.inTeam(ctx, team, tenancy.AbilityManageTokens, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
		if _, err := q.InsertMcpOAuthCode(ctx, db.InsertMcpOAuthCodeParams{CodeHash: hash, ClientID: client.ID, TeamID: m.TeamID,
			UserID: userID, RedirectUri: redirectURI, CodeChallenge: challenge, Scopes: scopes.Join(granted),
			ExpiresAt: time.Now().Add(oauthCodeTTL)}); err != nil {
			return huma.Error500InternalServerError("could not issue the code")
		}
		return tenancy.Record(ctx, q, m.TeamID, userID, "oauth.authorized", "team", m.TeamID,
			map[string]any{"client": client.Name, "client_id": client.ClientID, "kind": oauthKindMCP, "scopes": granted})
	})
	if err != nil {
		return nil, err
	}
	out := &oauthDecisionOut{}
	out.Body.Redirect = oauthRedirect(redirectURI, map[string]string{"code": plain, "state": state})
	return out, nil
}
