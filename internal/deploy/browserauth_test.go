package deploy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/app"
	"github.com/fadymondy/hosbah/internal/config"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/migrate"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// The forward_auth handoff (internal/deploy/browserauth.go): a one-time signed token exchanged for
// a stateless, sealed session cookie, re-checked against live team membership on every request. Set
// TEST_DATABASE_URL (a migrated DB) to run it; skipped otherwise, the same convention as
// internal/traffic/traffic_test.go.
func TestBrowserAuthHandoff(t *testing.T) {
	config.LoadDotEnv()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the browser auth DB tests")
	}
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	ctx := context.Background()
	if err := migrate.Run(ctx, sqlDB, migrate.RepoDir(migrate.Dir), false, io.Discard); err != nil {
		t.Fatal(err)
	}

	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewKeyring(kek)
	if err != nil {
		t.Fatal(err)
	}
	a := &app.App{SQLDB: sqlDB, Keys: keys}
	s := &Service{a: a}

	n := time.Now().UnixNano()
	teamID := fmt.Sprintf("bauth-team-%d", n)
	otherTeamID := fmt.Sprintf("bauth-team-other-%d", n)
	userID := fmt.Sprintf("bauth-user-%d", n)
	outsiderID := fmt.Sprintf("bauth-user-outsider-%d", n)
	serverID := fmt.Sprintf("bauth-server-%d", n)
	appID := fmt.Sprintf("bauth-app-%d", n)
	browserID := fmt.Sprintf("bauth-browser-%d", n)
	hostname := fmt.Sprintf("bauth-%d.example.test", n)

	var memberRow db.TeamMember
	seed := tenancy.Scope{UserID: userID, TeamID: teamID}
	if err := tenancy.Tx(ctx, sqlDB, seed, func(q *db.Queries, _ *sql.Tx) error {
		if _, err := q.InsertTeam(ctx, db.InsertTeamParams{ID: teamID, Name: teamID, Slug: teamID, OwnerID: userID, DekWrapped: "x"}); err != nil {
			return fmt.Errorf("team: %w", err)
		}
		row, err := q.InsertTeamMember(ctx, db.InsertTeamMemberParams{TeamID: teamID, UserID: userID, Role: "owner"})
		if err != nil {
			return fmt.Errorf("member: %w", err)
		}
		memberRow = row
		if _, err := q.InsertServer(ctx, db.InsertServerParams{
			ID: serverID, TeamID: teamID, Name: "srv", Provider: "byo", Kind: "vm", Status: "enrolled", CreatedBy: userID,
		}); err != nil {
			return fmt.Errorf("server: %w", err)
		}
		if _, err := q.InsertApp(ctx, db.InsertAppParams{
			ID: appID, TeamID: teamID, ServerID: serverID, Name: "app", RepoURL: "https://example.com/repo.git",
			Branch: "main", Port: 3000, HealthcheckPath: "/", HealthcheckTimeout: 5, CreatedBy: userID,
		}); err != nil {
			return fmt.Errorf("app: %w", err)
		}
		if _, err := q.InsertAppBrowser(ctx, db.InsertAppBrowserParams{
			ID: browserID, TeamID: teamID, AppID: appID, Name: "desk", Image: agentproto.DesktopImage,
			MemoryMb: agentproto.DesktopDefaultMemoryMB, Cpus: 1, MaxSessions: 1, IdleTimeoutSeconds: 300,
			EnvPrefix: "DDESKTOPTEST", TokenSealed: "csv1:unused-for-a-desktop-browser", Kind: agentproto.BrowserKindDesktop,
		}); err != nil {
			return fmt.Errorf("browser: %w", err)
		}
		if _, err := q.SetAppBrowserHostname(ctx, db.SetAppBrowserHostnameParams{Hostname: &hostname, ID: browserID, TeamID: teamID}); err != nil {
			return fmt.Errorf("hostname: %w", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("seed fixtures: %v", err)
	}
	if err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{UserID: outsiderID, TeamID: otherTeamID}, func(q *db.Queries, _ *sql.Tx) error {
		_, err := q.InsertTeam(ctx, db.InsertTeamParams{ID: otherTeamID, Name: otherTeamID, Slug: otherTeamID, OwnerID: outsiderID, DekWrapped: "x"})
		return err
	}); err != nil {
		t.Fatalf("seed other team: %v", err)
	}

	mintTokenAt := func(t *testing.T, tok string, expiresAt time.Time) string {
		t.Helper()
		tok = fmt.Sprintf("%s-%d", tok, time.Now().UnixNano()) // unique per run: token_hash is unique
		hash := tokenHashForTest(tok)
		if err := s.sys(ctx, func(q *db.Queries) error {
			_, err := q.InsertBrowserAuthToken(ctx, db.InsertBrowserAuthTokenParams{
				ID: fmt.Sprintf("bauth-tok-%s", hash[:16]), TeamID: teamID, BrowserID: browserID,
				UserID: userID, TokenHash: hash, ExpiresAt: expiresAt,
			})
			return err
		}); err != nil {
			t.Fatalf("mint token: %v", err)
		}
		return tok
	}
	mintToken := func(t *testing.T, tok string, ttl time.Duration) string {
		t.Helper()
		return mintTokenAt(t, tok, time.Now().Add(ttl))
	}

	exchange := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, browserAuthExchangePath+"?token="+token, nil)
		req.Host = hostname
		req.Header.Set("X-Forwarded-Host", hostname)
		req.Header.Set("X-Forwarded-Uri", browserAuthExchangePath+"?token="+token)
		w := httptest.NewRecorder()
		s.handleBrowsersAuth(w, req)
		return w
	}
	check := func(cookie *http.Cookie, host string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		req.Header.Set("X-Forwarded-Host", host)
		req.Header.Set("X-Forwarded-Uri", "/")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		s.handleBrowsersAuth(w, req)
		return w
	}
	sessionCookie := func(w *httptest.ResponseRecorder) *http.Cookie {
		for _, c := range w.Result().Cookies() {
			if c.Name == browserSessionCookie {
				return c
			}
		}
		return nil
	}

	// --- allow: a fresh valid token exchanges for a session that then passes the steady-state check.
	t.Run("allow", func(t *testing.T) {
		tok := mintToken(t, "tok-allow", BrowserAuthTokenTTL)
		w := exchange(tok)
		if w.Code != http.StatusFound {
			t.Fatalf("exchange: status = %d, want %d; body=%s", w.Code, http.StatusFound, w.Body.String())
		}
		cookie := sessionCookie(w)
		if cookie == nil {
			t.Fatal("exchange: no session cookie set")
		}
		if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
			t.Errorf("session cookie must be HttpOnly, Secure, SameSite=Lax: %+v", cookie)
		}
		w2 := check(cookie, hostname)
		if w2.Code != http.StatusOK {
			t.Fatalf("check: status = %d, want 200; body=%s", w2.Code, w2.Body.String())
		}
	})

	// --- deny: no cookie at all.
	t.Run("deny_no_cookie", func(t *testing.T) {
		w := check(nil, hostname)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})

	// --- deny: a valid session, but the team membership backing it is gone by the time of the check.
	t.Run("deny_other_team_membership_revoked", func(t *testing.T) {
		tok := mintToken(t, "tok-revoke", BrowserAuthTokenTTL)
		w := exchange(tok)
		cookie := sessionCookie(w)
		if cookie == nil {
			t.Fatal("no session cookie set")
		}
		// team_members' DELETE policy needs app.team_id set (or platform admin); a bare system scope
		// matches no rows and DeleteTeamMember (sqlc :exec) would silently no-op.
		teamScope := tenancy.Scope{System: true, TeamID: teamID}
		if err := tenancy.Tx(ctx, sqlDB, teamScope, func(q *db.Queries, _ *sql.Tx) error { return q.DeleteTeamMember(ctx, memberRow.ID) }); err != nil {
			t.Fatalf("revoke membership: %v", err)
		}
		defer func() {
			_ = tenancy.Tx(ctx, sqlDB, teamScope, func(q *db.Queries, _ *sql.Tx) error {
				row, err := q.InsertTeamMember(ctx, db.InsertTeamMemberParams{TeamID: teamID, UserID: userID, Role: "owner"})
				memberRow = row
				return err
			})
		}()
		w2 := check(cookie, hostname)
		if w2.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 once membership is gone", w2.Code)
		}
	})

	// --- deny: an expired token. browser_auth_tokens_expiry_check requires expires_at > created_at,
	// so a token cannot be minted already-expired; mint one that expires almost immediately and let
	// it age past that before exchanging it.
	t.Run("deny_expired_token", func(t *testing.T) {
		// The DB container's clock can drift by up to a second or so from the test host's (Docker
		// Desktop, host sleep/resume) — mint against the DB's OWN now() and sleep well past it so the
		// comparison inside ConsumeBrowserAuthToken (expires_at > now(), both evaluated in Postgres)
		// is never a race against wall-clock skew between the two.
		var dbNow time.Time
		if err := sqlDB.QueryRowContext(ctx, "select now()").Scan(&dbNow); err != nil {
			t.Fatalf("db now: %v", err)
		}
		tok := mintTokenAt(t, "tok-expired", dbNow.Add(time.Second))
		time.Sleep(4 * time.Second)
		w := exchange(tok)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 for an expired token", w.Code)
		}
	})

	// --- deny: replaying an already-used token.
	t.Run("deny_replayed_token", func(t *testing.T) {
		tok := mintToken(t, "tok-replay", BrowserAuthTokenTTL)
		w1 := exchange(tok)
		if w1.Code != http.StatusFound {
			t.Fatalf("first exchange: status = %d, want 302; body=%s", w1.Code, w1.Body.String())
		}
		w2 := exchange(tok)
		if w2.Code != http.StatusUnauthorized {
			t.Fatalf("replay: status = %d, want 401", w2.Code)
		}
	})

	// --- deny: a garbage/foreign cookie value.
	t.Run("deny_garbage_cookie", func(t *testing.T) {
		w := check(&http.Cookie{Name: browserSessionCookie, Value: "not-a-real-session"}, hostname)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})
}

// tokenHashForTest mirrors exchangeBrowserAuth's own hashing of the raw token, so tests can mint a
// token row directly without going through OpenDesktopBrowser.
func tokenHashForTest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// The proxies in front of the control plane rewrite X-Forwarded-Host (to app.<domain>), so the
// agent's own X-Hosbah-Browser-Host wins; Host is the last resort.
func TestBrowserAuthHost(t *testing.T) {
	r := httptest.NewRequest("GET", "https://app.hosbah.com/api/browsers/auth", nil)
	if got := browserAuthHost(r); got != "app.hosbah.com" {
		t.Fatalf("Host only: %q", got)
	}
	r.Header.Set("X-Forwarded-Host", "app.hosbah.com")
	r.Header.Set("X-Hosbah-Browser-Host", "Browser.Moharrik.com:443")
	if got := browserAuthHost(r); got != "browser.moharrik.com" {
		t.Fatalf("X-Hosbah-Browser-Host should win: %q", got)
	}
}
