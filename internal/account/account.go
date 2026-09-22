// Package account is Hosbah's account cycle around the togo auth plugin: email verification,
// password reset, sign-in by emailed code, and authenticator-app two-factor with recovery codes.
//
// Ported from fadymondy.com-v2's internal/account (signup.go, twofactor.go; same owner) and fitted
// to Hosbah: sqlc queries, the TOTP secret sealed under HOSBAH_KEK (crypto.Keyring.SealPlatform),
// the existing users.sessions_revoked_at for "sign every session out", and the platform's
// disabled-account refusal on every path that creates a session.
//
// The auth plugin still owns the users table, password hashing (bcrypt, DefaultCost) and session
// issuing. This package wraps its POST /api/auth/register and /api/auth/login (Middleware) and
// adds the endpoints in Mount. Every error answer is JSON {error, code, …}.
package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/brand"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/mail"
)

// Error codes the web branches on.
const (
	CodeEmailUnverified  = "email_unverified"
	Code2FARequired      = "2fa_required"
	Code2FAInvalid       = "2fa_invalid"
	CodeChallengeExpired = "challenge_expired"
	CodeAccountDisabled  = "account_disabled"
	CodeInvalidCode      = "invalid_code"
	CodeRateLimited      = "rate_limited"
	CodeInvalidRequest   = "invalid_request"
	CodeWeakPassword     = "invalid_password"
	CodeUnauthorized     = "unauthorized"
	Code2FAEnabled       = "2fa_already_enabled"
	Code2FANotStarted    = "2fa_not_started"
	Code2FANotEnabled    = "2fa_not_enabled"
	CodeInternal         = "internal_error"
)

// Service holds the account flows. Build it with New; tests replace Send and Now.
type Service struct {
	DB   *sql.DB
	Keys *crypto.Keyring
	Auth *auth.Service
	Log  *slog.Logger
	// Send delivers an email (mail.FromEnv in the server; a capture in tests).
	Send mail.Sender
	Now  func() time.Time

	// Rate limits, per Service (so each test server starts clean).
	signupIP  *limiter // 20 / 10 min per client: sign-up and every "send me a code"
	emailSend *limiter // 6 / hour per address: codes mailed
	tries     *limiter // 15 / 10 min per client and per address: code guesses

	trusted []*net.IPNet
}

// New builds the flows. keys seals authenticator secrets; without them two-factor enrolment is
// refused (GET /api/me/2fa says available:false) rather than storing a secret in the clear.
func New(sqlDB *sql.DB, keys *crypto.Keyring, svc *auth.Service, log *slog.Logger, send mail.Sender) *Service {
	if log == nil {
		log = slog.Default()
	}
	if send == nil {
		send = mail.FromEnv(log)
	}
	return &Service{
		DB: sqlDB, Keys: keys, Auth: svc, Log: log, Send: send, Now: time.Now,
		signupIP:  newLimiter(20, 10*time.Minute),
		emailSend: newLimiter(6, time.Hour),
		tries:     newLimiter(15, 10*time.Minute),
		trusted:   parseCIDRs(brand.Getenv("TRUSTED_PROXIES")),
	}
}

func (s *Service) q() *db.Queries { return db.New(s.DB) }

func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func validEmail(e string) bool {
	at := strings.IndexByte(e, '@')
	return at > 0 && at < len(e)-1 && len(e) <= 254 && !strings.ContainsAny(e, " \r\n\t")
}

func localeOf(r *http.Request, asked string) string {
	if asked == "ar" || asked == "en" {
		return asked
	}
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "ar") {
		return "ar"
	}
	return "en"
}

/*
clientIP keys the per-client limits. RemoteAddr, unless it is one of HOSBAH_TRUSTED_PROXIES
(comma-separated CIDRs or addresses): then X-Forwarded-For is walked from the right, and the
first address that is not a trusted proxy is the client. Nothing else trusts the header — a
client that sets its own X-Forwarded-For cannot pick a fresh bucket. Behind a proxy with the
setting unset, every client shares the proxy's bucket, so set it in production.
*/
func (s *Service) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !s.isTrusted(host) {
		return host
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSpace(parts[i])
		if p == "" {
			continue
		}
		if !s.isTrusted(p) {
			return p
		}
	}
	return host
}

func (s *Service) isTrusted(addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, n := range s.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func parseCIDRs(v string) []*net.IPNet {
	var out []*net.IPNet
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.Contains(p, "/") {
			if ip := net.ParseIP(p); ip != nil && ip.To4() != nil {
				p += "/32"
			} else {
				p += "/128"
			}
		}
		if _, n, err := net.ParseCIDR(p); err == nil {
			out = append(out, n)
		}
	}
	return out
}

/* ------------------------------------------------------------ JSON */

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// fail writes {error, code} plus any extra fields.
func fail(w http.ResponseWriter, status int, code, msg string, extra ...any) {
	body := map[string]any{"error": msg, "code": code}
	for i := 0; i+1 < len(extra); i += 2 {
		if k, ok := extra[i].(string); ok {
			body[k] = extra[i+1]
		}
	}
	writeJSON(w, status, body)
}

func tooMany(w http.ResponseWriter) {
	fail(w, http.StatusTooManyRequests, CodeRateLimited, "too many attempts — try again in a few minutes")
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func splitCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

/* ------------------------------------------------------------ rate limits */

type limiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	max    int
	window time.Duration
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{hits: map[string][]time.Time{}, max: max, window: window}
}

// allow records one hit for key and reports whether it is within the limit.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cut := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	// Bound memory: drop idle keys now and then.
	if len(l.hits) > 50_000 {
		for k, ts := range l.hits {
			if len(ts) == 0 || ts[len(ts)-1].Before(cut) {
				delete(l.hits, k)
			}
		}
	}
	return true
}

/* ------------------------------------------------------------ users */

type user struct {
	ID, Email, Roles, Permissions string
	Disabled, Unverified          bool
}

func (s *Service) userByEmail(ctx context.Context, email string) (*user, error) {
	u, err := s.q().AccountUserByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	return &user{ID: u.ID, Email: u.Email, Roles: u.Roles, Permissions: u.Permissions, Disabled: u.DisabledAt != nil, Unverified: u.EmailUnverifiedAt != nil}, nil
}

func (s *Service) userByID(ctx context.Context, id string) (*user, error) {
	u, err := s.q().AccountUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &user{ID: u.ID, Email: u.Email, Roles: u.Roles, Permissions: u.Permissions, Disabled: u.DisabledAt != nil, Unverified: u.EmailUnverifiedAt != nil}, nil
}

func disabled(w http.ResponseWriter) {
	fail(w, http.StatusForbidden, CodeAccountDisabled, "this account has been disabled — contact the platform administrator")
}

// clearSession expires the session cookie (the plugin's name and attributes).
func clearSession(w http.ResponseWriter, r *http.Request) {
	secure := r.TLS != nil
	if v := os.Getenv("COOKIE_SECURE"); v != "" {
		secure = v == "1" || v == "true"
	} else if mail.IsProduction() {
		secure = true
	}
	http.SetCookie(w, &http.Cookie{ //#nosec G124 -- HttpOnly+SameSite enforced; Secure as the plugin sets it
		Name: auth.SessionCookie, Value: "", Path: "/",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}
