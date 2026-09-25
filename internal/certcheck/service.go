// Package certcheck is MH-580 J6: the team TLS certificate watch. Every TickEvery it TLS-dials
// every hostname across every team whose tls is 'auto' (an app domain or a proxy host — see
// internal/db/schema/ssl_certificate.sql), records issuer, validity window and whether the
// hostname is covered by the certificate's own SANs, and alerts a team's notification channels
// (internal/notifications, event cert.expiring) at 14, 7 and 1 day(s) left and on an outright
// failure, each deduplicated by ssl_certificates' alerted_* flags so the same expiry or the same
// failure is never announced twice.
//
// Like the monitor evaluator, every connection goes through the SSRF-safe dialer
// (internal/monitors.SafeDialContext) — these are hostnames a team registered, not arbitrary
// input, but the same rule applies uniformly to anything the control plane dials on a tenant's
// behalf.
package certcheck

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/app"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/monitors"
	"github.com/fadymondy/hosbah/internal/notifications"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

const (
	// TickEvery is how often the sweep runs.
	TickEvery = 6 * time.Hour
	// DialTimeout bounds one TLS handshake.
	DialTimeout = 10 * time.Second

	workers = 8

	flag14      = "alerted_14"
	flag7       = "alerted_7"
	flag1       = "alerted_1"
	flagFailure = "alerted_failure"
)

// Thresholds, days left, checked in order (soonest first so only the tightest fired flag is set
// per tick — a cert 2 days from expiry sets alerted_1 but not alerted_14/7, since those already
// fired on earlier ticks and stay set).
var thresholds = []struct {
	Days int
	Flag string
}{{1, flag1}, {7, flag7}, {14, flag14}}

// Service is the sweep.
type Service struct {
	db     *sql.DB
	log    *slog.Logger
	notify *notifications.Service

	mu      sync.RWMutex
	now     func() time.Time
	dial    func(ctx context.Context, network, addr string) (net.Conn, error)
	rootCAs *x509.CertPool // nil = the system roots; tests trust their own throwaway CA instead

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New builds the service. notify may be nil in tests that only check the recorded rows.
func New(a *app.App, notify *notifications.Service) *Service {
	log := a.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: a.SQLDB, log: log.With("component", "certcheck"), notify: notify,
		now: time.Now, dial: monitors.SafeDialContext(nil)}
}

// SetClock replaces the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.mu.Lock(); s.now = now; s.mu.Unlock() }

// SetDialer replaces the TCP dialer (tests: httptest TLS servers on loopback, which the SSRF rule
// would otherwise refuse).
func (s *Service) SetDialer(dial func(ctx context.Context, network, addr string) (net.Conn, error)) {
	s.mu.Lock()
	s.dial = dial
	s.mu.Unlock()
}

// SetRootCAs replaces the certificate pool the TLS handshake verifies against (tests: a throwaway
// CA signing the test listener's leaf). Production never calls this, so it dials against the
// system roots, exactly like a browser would.
func (s *Service) SetRootCAs(pool *x509.CertPool) { s.mu.Lock(); s.rootCAs = pool; s.mu.Unlock() }

func (s *Service) deps() (func() time.Time, func(context.Context, string, string) (net.Conn, error), *x509.CertPool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.now, s.dial, s.rootCAs
}

// Start launches the sweep loop; Stop ends it. The first tick comes TickEvery after Start.
func (s *Service) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(TickEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
					s.log.Warn("certificate sweep failed", "err", err)
				}
			}
		}
	}()
}

// Stop is the counterpart of Start. Safe to call twice.
func (s *Service) Stop() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.wg.Wait()
}

// hostTarget is one hostname to check, across any team.
type hostTarget struct {
	TeamID   string
	Hostname string
	Source   string
}

// Tick checks every registered hostname once and returns how many it checked.
func (s *Service) Tick(ctx context.Context) (int, error) {
	var targets []hostTarget
	err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		rows, err := q.SystemHostnamesForSSL(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			targets = append(targets, hostTarget{TeamID: r.TeamID, Hostname: r.Hostname, Source: r.Source})
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("list hostnames: %w", err)
	}

	jobs := make(chan hostTarget)
	var wg sync.WaitGroup
	for i := 0; i < min(workers, len(targets)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				if err := s.checkOne(ctx, t); err != nil && ctx.Err() == nil {
					s.log.Warn("certificate check failed", "hostname", t.Hostname, "err", err)
				}
			}
		}()
	}
	for _, t := range targets {
		jobs <- t
	}
	close(jobs)
	wg.Wait()
	return len(targets), nil
}

// CheckNow checks one team's hostnames at once (POST …/certificates/check).
func (s *Service) CheckNow(ctx context.Context, teamID string) (int, error) {
	var targets []hostTarget
	err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true, TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		rows, err := q.SystemHostnamesForSSL(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.TeamID == teamID {
				targets = append(targets, hostTarget{TeamID: r.TeamID, Hostname: r.Hostname, Source: r.Source})
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, t := range targets {
		if err := s.checkOne(ctx, t); err != nil {
			s.log.Warn("certificate check failed", "hostname", t.Hostname, "err", err)
		}
	}
	return len(targets), nil
}

// reading is what one TLS dial found.
type reading struct {
	OK        bool
	Issuer    string
	NotBefore time.Time
	NotAfter  time.Time
	SANMatch  bool
	Err       string
}

func (s *Service) dialOne(ctx context.Context, hostname string) reading {
	now, dial, rootCAs := s.deps()
	dctx, cancel := context.WithTimeout(ctx, DialTimeout)
	defer cancel()
	conn, err := dial(dctx, "tcp", net.JoinHostPort(hostname, "443"))
	if err != nil {
		return reading{Err: err.Error()}
	}
	defer conn.Close()
	tc := tls.Client(conn, &tls.Config{ServerName: hostname, MinVersion: tls.VersionTLS12, RootCAs: rootCAs}) //nolint:gosec -- ServerName only, verification stays on
	_ = tc.SetDeadline(now().Add(DialTimeout))
	if err := tc.HandshakeContext(dctx); err != nil {
		return reading{Err: err.Error()}
	}
	state := tc.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return reading{Err: "server presented no certificate"}
	}
	leaf := state.PeerCertificates[0]
	sanMatch := leaf.VerifyHostname(hostname) == nil
	return reading{OK: true, Issuer: issuerName(leaf), NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter, SANMatch: sanMatch}
}

func issuerName(c *x509.Certificate) string {
	if cn := c.Issuer.CommonName; cn != "" {
		return cn
	}
	if len(c.Issuer.Organization) > 0 {
		return c.Issuer.Organization[0]
	}
	return c.Issuer.String()
}

func (s *Service) checkOne(ctx context.Context, t hostTarget) error {
	r := s.dialOne(ctx, t.Hostname)
	now, _, _ := s.deps()
	ok := r.OK && r.SANMatch
	errMsg := r.Err
	if r.OK && !r.SANMatch {
		errMsg = "the certificate does not cover this hostname"
	}
	var row db.SslCertificate
	err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true, TeamID: t.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		var nb, na *time.Time
		if !r.NotBefore.IsZero() {
			v := r.NotBefore
			nb = &v
		}
		if !r.NotAfter.IsZero() {
			v := r.NotAfter
			na = &v
		}
		checked := now()
		var err error
		row, err = q.UpsertSSLCertificate(ctx, db.UpsertSSLCertificateParams{
			TeamID: t.TeamID, Hostname: t.Hostname, Source: t.Source, Ok: ok, Issuer: r.Issuer,
			NotBefore: nb, NotAfter: na, SanMatch: r.SANMatch, Error: errMsg, LastCheckedAt: &checked,
		})
		return err
	})
	if err != nil {
		return err
	}
	s.maybeAlert(ctx, row)
	return nil
}

func (s *Service) maybeAlert(ctx context.Context, row db.SslCertificate) {
	if s.notify == nil {
		return
	}
	if !row.Ok {
		if !row.AlertedFailure {
			s.setFlag(ctx, row, flagFailure)
			s.notify.DeliverEvent(ctx, row.TeamID, notifications.Alert{
				Event: notifications.EventCertExpiring, Subject: "certificate", Title: "Certificate check failed: " + row.Hostname,
				Reason: strings.TrimSpace(row.Error), Link: certLink(row.TeamID),
			})
		}
		return
	}
	if row.NotAfter == nil {
		return
	}
	daysLeft := int(time.Until(*row.NotAfter).Hours() / 24)
	for _, th := range thresholds {
		if daysLeft > th.Days {
			continue
		}
		if flagSet(row, th.Flag) {
			return // a tighter or equal threshold already alerted this cycle
		}
		s.setFlag(ctx, row, th.Flag)
		s.notify.DeliverEvent(ctx, row.TeamID, notifications.Alert{
			Event: notifications.EventCertExpiring, Subject: "certificate",
			Title:  fmt.Sprintf("Certificate for %s expires in %d day(s)", row.Hostname, daysLeft),
			Reason: fmt.Sprintf("issued by %s, valid until %s", row.Issuer, row.NotAfter.Format(time.RFC3339)),
			Link:   certLink(row.TeamID),
		})
		return
	}
}

func flagSet(row db.SslCertificate, flag string) bool {
	switch flag {
	case flag14:
		return row.Alerted14
	case flag7:
		return row.Alerted7
	case flag1:
		return row.Alerted1
	case flagFailure:
		return row.AlertedFailure
	}
	return false
}

func (s *Service) setFlag(ctx context.Context, row db.SslCertificate, flag string) {
	err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true, TeamID: row.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		return q.SetSSLAlerted(ctx, db.SetSSLAlertedParams{ID: row.ID, Flag: flag})
	})
	if err != nil {
		s.log.Warn("could not record the certificate alert", "id", row.ID, "err", err)
	}
}

// certLink is the web app's certificates page for a team. Built the same way as
// notifications.AppLink (APP_URL, "en" locale — see its comment), since that helper only builds
// an app link and a certificate has no app id.
func certLink(teamID string) string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("APP_URL")), "/")
	if base == "" || teamID == "" {
		return ""
	}
	return fmt.Sprintf("%s/en/t/%s/certificates", base, teamID)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
