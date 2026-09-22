// Package oauthstate keeps the one-time state of a browser round trip through a third party:
// sign-in and account linking with Google or GitHub (internal/account), and the GitHub App
// installation (internal/api). Every such trip leaves Hosbah with a redirect and comes back on a
// callback URL that anybody can forge, so the callback only proceeds with a state that
//
//   - Hosbah issued (the state parameter's SHA-256 is the key of a server-side row),
//   - has not been used (the callback deletes the row; only the request that deleted it goes on),
//   - is less than ten minutes old,
//   - comes back in the browser that started it: an HttpOnly, SameSite=Lax cookie holds a second
//     random value whose SHA-256 is on the row. A state sent to somebody else (login CSRF: "sign
//     the victim in as me") or replayed from another machine does not match.
//
// Why a server-side row rather than a sealed cookie: a sealed cookie can prove the state is ours
// and fresh, but not that it has not been used already — single use needs server state anyway —
// and the row keeps the PKCE verifier and the nonce on the server, never in the browser.
package oauthstate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/mail"
)

// TTL is how long a started round trip may take.
const TTL = 10 * time.Minute

// Purposes.
const (
	PurposeLogin         = "login"
	PurposeLink          = "link"
	PurposeGitHubInstall = "github_install"
)

// ErrInvalid: no such state, already used, expired, another browser's, or for something else.
var ErrInvalid = errors.New("this sign-in link has expired or was already used — start again")

// Record is what a round trip carries from its start to its callback.
type Record struct {
	Purpose        string
	Provider       string
	CodeVerifier   string
	Nonce          string
	Next           string
	Locale         string
	InvitationHash string
	UserID         string
	TeamID         string
	ExpiresAt      time.Time
}

// Random returns n random bytes, base64url without padding.
func Random(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Secure reports whether cookies must be Secure: COOKIE_SECURE when set (as the auth plugin reads
// it), else in production, else when the request came over TLS.
func Secure(tls bool) bool {
	if v := os.Getenv("COOKIE_SECURE"); v != "" {
		return v == "1" || v == "true"
	}
	return mail.IsProduction() || tls
}

// BrowserCookie is the HttpOnly, SameSite=Lax cookie that binds a state to this browser (Path
// /api/, so both the provider's callback and the App's setup URL receive it; ten minutes).
func BrowserCookie(name, value string, secure bool) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/api/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(TTL / time.Second)}
}

// ClearCookie expires the browser-binding cookie.
func ClearCookie(name string, secure bool) *http.Cookie {
	return &http.Cookie{Name: name, Value: "", Path: "/api/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1}
}

// Begin stores rec and returns the state parameter to send to the third party and the value of
// the browser-binding cookie to set (BrowserCookie).
func Begin(ctx context.Context, sqlDB *sql.DB, rec Record, now time.Time) (state, browser string, err error) {
	if state, err = Random(32); err != nil {
		return "", "", err
	}
	if browser, err = Random(32); err != nil {
		return "", "", err
	}
	q := db.New(sqlDB)
	_ = q.DeleteExpiredOAuthStates(ctx)
	if err := q.CreateOAuthState(ctx, db.CreateOAuthStateParams{
		ID: hash(state), BrowserHash: hash(browser), Purpose: rec.Purpose, Provider: rec.Provider,
		CodeVerifier: rec.CodeVerifier, Nonce: rec.Nonce, Next: rec.Next, Locale: rec.Locale, InvitationHash: rec.InvitationHash,
		UserID: nilIfEmpty(rec.UserID), TeamID: nilIfEmpty(rec.TeamID), ExpiresAt: now.UTC().Add(TTL),
	}); err != nil {
		return "", "", err
	}
	return state, browser, nil
}

// Finish consumes state. It is deleted whatever happens next, so it can never be tried twice.
// browser is the value of the browser-binding cookie this request carried ("" when none); the
// caller clears the cookie (ClearCookie). The record's purpose must be one of purposes.
func Finish(ctx context.Context, sqlDB *sql.DB, state, browser string, now time.Time, purposes ...string) (Record, error) {
	state = strings.TrimSpace(state)
	if state == "" || len(state) > 200 {
		return Record{}, ErrInvalid
	}
	row, err := db.New(sqlDB).ConsumeOAuthState(ctx, hash(state))
	if err != nil {
		return Record{}, ErrInvalid
	}
	if browser == "" || hash(browser) != row.BrowserHash {
		return Record{}, ErrInvalid
	}
	if !now.Before(row.ExpiresAt) {
		return Record{}, ErrInvalid
	}
	ok := false
	for _, p := range purposes {
		ok = ok || p == row.Purpose
	}
	if !ok {
		return Record{}, ErrInvalid
	}
	rec := Record{Purpose: row.Purpose, Provider: row.Provider, CodeVerifier: row.CodeVerifier, Nonce: row.Nonce, Next: row.Next,
		Locale: row.Locale, InvitationHash: row.InvitationHash, ExpiresAt: row.ExpiresAt}
	if row.UserID != nil {
		rec.UserID = *row.UserID
	}
	if row.TeamID != nil {
		rec.TeamID = *row.TeamID
	}
	return rec, nil
}

// SafeNext returns next when it is a same-origin relative path — "/" then not "/" or "\", no
// scheme, no control characters, no backslashes anywhere — and "" otherwise.
func SafeNext(next string) string {
	if next == "" || len(next) > 1024 || next[0] != '/' {
		return ""
	}
	if len(next) > 1 && (next[1] == '/' || next[1] == '\\') {
		return ""
	}
	for _, c := range next {
		if c < 0x20 || c == 0x7f || c == '\\' {
			return ""
		}
	}
	// "/%2F%2Fevil.example" decodes to "//evil.example" in some routers; refuse encoded slashes
	// and backslashes right after the first one.
	lower := strings.ToLower(next)
	if strings.HasPrefix(lower, "/%2f") || strings.HasPrefix(lower, "/%5c") {
		return ""
	}
	return next
}
