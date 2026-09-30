package circlexo

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	circlexo "github.com/circlexo/circlexo-go"
	"github.com/circlexo/circlexo-go/oidc"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/oauthstate"
)

/*
Sign in with CircleXO, next to (not instead of) Hosbah's own login.

Callback rules, in order:
 1. a hub subject already linked to a Hosbah user signs in as that user;
 2. otherwise the hub must vouch for the e-mail (email_verified): an unverified address is refused;
 3. a Hosbah account with that address is linked, but only a "usable" one (Hosbah verified the
    address itself), so nobody can squat someone's address with a password and inherit their hub
    sign-in; an unverified account is refused (account_conflict), never taken over;
 4. otherwise a passwordless Hosbah account is created and linked.

A disabled account is refused in every case. The session is Hosbah's normal one
(auth.IssueSession), so every existing login method keeps working alongside. Hosbah's 2FA is not
asked for: the hub is the identity provider and owns second factors.
*/

// Errors the login page can show, by code (web: circlexo_error=<code>).
const (
	errFailed      = "failed"
	errUnverified  = "email_unverified"
	errUnlinkable  = "account_conflict"
	errSuspended   = "suspended"
	errNoEmail     = "no_email"
	errNotMember   = "not_member"
	errUnavailable = "unavailable"
)

// loginErr is a refusal with a code for the page.
type loginErr struct {
	code string
	err  error
}

func (e *loginErr) Error() string { return e.code + ": " + e.err.Error() }

func refuse(code, msg string) *loginErr { return &loginErr{code, errors.New(msg)} }

// LoginError is a refused sign-in, exported so callers and tests can read its code.
type LoginError = loginErr

// Code is the short refusal code the login page translates.
func (e *loginErr) Code() string { return e.code }

// launchHint is the pseudo local path /sso/circlexo hands the OIDC round trip so the org named by
// the hub's launch link survives it; the callback reads the org slug off it.
const launchHint = "/__cxo"

// Login starts the sign-in (GET /auth/circlexo/login[?return_to=/local/path]).
func (s *Service) Login(w http.ResponseWriter, r *http.Request) { s.RP.Login(w, r) }

// Launch is the hub's app launch URL (GET /sso/circlexo?org=<slug>): the hub sends people here from
// its dashboard. They are signed in at the hub already, so the round trip is silent.
func (s *Service) Launch(w http.ResponseWriter, r *http.Request) {
	q := url.Values{}
	if org := strings.TrimSpace(r.URL.Query().Get("org")); org != "" {
		q.Set("return_to", launchHint+"?org="+url.QueryEscape(org))
	}
	http.Redirect(w, r, LoginPath+"?"+q.Encode(), http.StatusFound)
}

// Callback completes the sign-in (GET /auth/circlexo/callback).
func (s *Service) Callback(w http.ResponseWriter, r *http.Request) {
	login, err := s.RP.Exchange(w, r)
	if err != nil {
		s.Log.Warn("circlexo: sign-in failed", "err", err)
		s.fail(w, r, errFailed)
		return
	}
	to, lerr := s.FinishLogin(r.Context(), w, login)
	if lerr != nil {
		s.Log.Warn("circlexo: sign-in refused", "code", lerr.code, "err", lerr.err)
		s.fail(w, r, lerr.code)
		return
	}
	http.Redirect(w, r, to, http.StatusFound)
}

func (s *Service) fail(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/login?circlexo_error="+url.QueryEscape(code), http.StatusFound)
}

func rawString(c *circlexo.Claims, key string) string {
	if c == nil {
		return ""
	}
	v, _ := c.Raw[key].(string)
	return v
}

func rawBool(c *circlexo.Claims, key string) bool {
	if c == nil {
		return false
	}
	switch v := c.Raw[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// userByEmail finds a Hosbah account by address. usable says the address alone may be trusted to
// identify its owner: Hosbah verified it (email_unverified_at is NULL). sql.ErrNoRows: no account.
func (s *Service) userByEmail(ctx context.Context, email string) (id string, usable bool, err error) {
	u, err := db.New(s.DB).AccountUserByEmail(ctx, email)
	if err != nil {
		return "", false, err
	}
	return u.ID, u.EmailUnverifiedAt == nil, nil
}

// createUser makes a passwordless, already verified account for an address the hub vouched for. A
// concurrent creation of the same address is reused.
func (s *Service) createUser(ctx context.Context, email string) (string, error) {
	id := randomHex(16)
	err := db.New(s.DB).CreateOAuthUser(ctx, db.CreateOAuthUserParams{ID: id, Email: email, CreatedAt: s.now().UTC().Format(time.RFC3339)})
	if err == nil {
		return id, nil
	}
	if isUnique(err) {
		if uid, _, err := s.userByEmail(ctx, email); err == nil {
			return uid, nil
		}
	}
	return "", err
}

// FinishLogin turns a verified hub sign-in into a Hosbah session and says where to land.
func (s *Service) FinishLogin(ctx context.Context, w http.ResponseWriter, l *oidc.Login) (string, *loginErr) {
	id := l.IDClaims
	if id == nil || id.Subject == "" {
		return "", refuse(errFailed, "no subject")
	}
	userID, lerr := s.linkedUser(ctx, l)
	if lerr != nil {
		return "", lerr
	}

	u, err := db.New(s.DB).AccountUserByID(ctx, userID)
	if err != nil || u.DisabledAt != nil {
		return "", refuse(errSuspended, "the account is disabled or gone")
	}

	to, lerr := s.landing(ctx, l, userID)
	if lerr != nil {
		return "", lerr
	}

	token, err := s.Auth.IssueSession(w, auth.Identity{
		ID: u.ID, Email: u.Email, Roles: splitCSV(u.Roles), Permissions: splitCSV(u.Permissions), Guard: "api",
	})
	if err != nil {
		return "", refuse(errFailed, "could not start the session")
	}
	sid := ""
	if l.AccessClaims != nil {
		sid = l.AccessClaims.SessionID
	}
	if sid == "" {
		sid = id.SessionID
	}
	s.recordSession(ctx, sid, userID, token)
	return to, nil
}

// linkedUser is rules 1-4 of the header comment.
func (s *Service) linkedUser(ctx context.Context, l *oidc.Login) (string, *loginErr) {
	id := l.IDClaims
	if uid, err := s.UserBySubject(ctx, id.Subject); err == nil {
		return uid, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", &loginErr{errUnavailable, err}
	}

	email := norm(firstNonEmpty(rawString(id, "email"), rawString(l.AccessClaims, "email")))
	if email == "" {
		return "", refuse(errNoEmail, "the hub sent no e-mail address")
	}
	if !rawBool(id, "email_verified") && !rawBool(l.AccessClaims, "email_verified") {
		return "", refuse(errUnverified, "the hub has not verified this address")
	}

	uid, usable, err := s.userByEmail(ctx, email)
	switch {
	case err == nil:
		if !usable {
			return "", refuse(errUnlinkable, "an account with this address exists but has not confirmed it")
		}
		got, err := s.linkUser(ctx, id.Subject, uid, email, "email")
		if err != nil {
			return "", &loginErr{errUnavailable, err}
		}
		if got != uid {
			return "", refuse(errUnlinkable, "the account is already linked to another CircleXO user")
		}
		return uid, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", &loginErr{errUnavailable, err}
	}

	newID, err := s.createUser(ctx, email)
	if err != nil {
		return "", &loginErr{errUnavailable, err}
	}
	got, err := s.linkUser(ctx, id.Subject, newID, email, "created")
	if err != nil {
		return "", &loginErr{errUnavailable, err}
	}
	if got != newID {
		return "", refuse(errUnlinkable, "the account is already linked to another CircleXO user")
	}
	return got, nil
}

// landing puts the user into the team mapped to the token's org (adding them with the hub role) and
// returns the page to open. With no org in play, the web app's own home decides.
func (s *Service) landing(ctx context.Context, l *oidc.Login, userID string) (string, *loginErr) {
	ac := l.AccessClaims
	orgID, orgRole := "", ""
	if ac != nil {
		orgID, orgRole = ac.OrgID, ac.OrgRole
	}
	slugHint := ""
	if strings.HasPrefix(l.ReturnTo, launchHint) {
		v, _ := url.ParseQuery(strings.TrimPrefix(l.ReturnTo, launchHint+"?"))
		slugHint = v.Get("org")
	} else if l.ReturnTo != "" && orgID == "" {
		return l.ReturnTo, nil
	}
	if orgID == "" && slugHint != "" {
		_ = s.DB.QueryRowContext(ctx, `SELECT org_id FROM circlexo_org_links WHERE org_slug = $1 AND status = 'active'`, slugHint).Scan(&orgID)
	}
	if orgID == "" {
		return "/", nil
	}
	link, err := s.OrgByID(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		// The hub installed the app but the webhook has not (or did not) arrive: provision now if
		// the hub confirms the org has the app.
		if _, terr := s.Client.TenantByOrg(ctx, orgID); terr != nil {
			return "/", nil
		}
		if link, err = s.Provision(ctx, ProvisionInput{OrgID: orgID, Slug: slugHint}); err != nil {
			s.Log.Error("circlexo: provisioning at sign-in", "org", orgID, "err", err)
			return "/", nil
		}
	} else if err != nil {
		return "", &loginErr{errUnavailable, err}
	}
	if !link.Active {
		return "/", nil
	}
	if err := s.setMember(ctx, link, userID, orgRole); err != nil {
		return "", &loginErr{errUnavailable, err}
	}
	locale := link.Locale
	if locale != "ar" {
		locale = "en"
	}
	return "/" + locale + "/t/" + link.TeamID, nil
}

// Logout clears Hosbah's session and returns to the login page. Hosbah's session is a stateless
// token, so this ends it in this browser only (the same as Hosbah's own sign-out).
func (s *Service) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{ //#nosec G124 -- HttpOnly+SameSite enforced; Secure as the plugin sets it
		Name: auth.SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: oauthstate.Secure(r.TLS != nil), SameSite: http.SameSiteLaxMode,
	})
	s.RP.Logout(baseURL() + "/login")(w, r)
}
