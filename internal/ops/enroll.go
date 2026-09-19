package ops

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

// EnrolTTL is how long an enrolment token (and the install command carrying it) works.
const EnrolTTL = 15 * time.Minute

// enrolPrefix makes a leaked enrolment token recognisable (cle_ = cloudy enrolment).
const enrolPrefix = "cle_"

// Install is the one-liner a person runs on a server.
type Install struct {
	URL         string    `json:"url"`
	Command     string    `json:"command"`      // for a sudo-capable user
	CommandRoot string    `json:"command_root"` // already root (containers, fresh LXCs)
	ExpiresAt   time.Time `json:"expires_at"`
}

// NewEnrollment mints a one-time enrolment token for a server, in the caller's transaction, and
// replaces any unused one. Only the token's SHA-256 is stored.
func (s *Service) NewEnrollment(ctx context.Context, q *db.Queries, teamID, serverID, createdBy string) (Install, error) {
	if err := q.DeleteOpenEnrollments(ctx, db.DeleteOpenEnrollmentsParams{ServerID: serverID, TeamID: teamID}); err != nil {
		return Install{}, err
	}
	plain, hash := tokens.New(enrolPrefix)
	exp := s.now().Add(EnrolTTL).UTC()
	if _, err := q.InsertEnrollment(ctx, db.InsertEnrollmentParams{TeamID: teamID, ServerID: serverID, TokenHash: hash,
		ExpiresAt: exp, CreatedBy: createdBy}); err != nil {
		return Install{}, err
	}
	u := s.cfg.PublicURL + "/install/" + plain
	return Install{URL: u, Command: "curl -fsSL " + u + " | sudo sh", CommandRoot: "curl -fsSL " + u + " | sh", ExpiresAt: exp}, nil
}

var errBadToken = errors.New("the enrolment token is invalid, already used, or expired")

// lookupEnrollment resolves an unused, unexpired enrolment by its token, in token scope.
func lookupEnrollment(ctx context.Context, q *db.Queries, hash string, now time.Time) (db.AgentEnrollment, error) {
	e, err := q.EnrollmentByHash(ctx, hash)
	if err != nil || e.UsedAt != nil || now.After(e.ExpiresAt) {
		return db.AgentEnrollment{}, errBadToken
	}
	return e, nil
}

// handleInstall is GET /install/{token}: a POSIX sh script, for `curl -fsSL … | sudo sh`. It does
// not consume the token (enrolment does), so a failed install can be re-run within the 15 minutes.
func (s *Service) handleInstall(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	notValid := func() {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "This Hosbah install link is not valid: it expired, was already used, or never existed.\n"+
			"Create a new one on the server's page in Hosbah.\n")
	}
	if !strings.HasPrefix(token, enrolPrefix) || len(token) > 128 {
		notValid()
		return
	}
	hash := tokens.Hash(token)
	var serverName string
	var expires time.Time
	err := tenancy.Tx(r.Context(), s.a.SQLDB, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
		e, err := lookupEnrollment(r.Context(), q, hash, s.now())
		if err != nil {
			return err
		}
		if err := tenancy.SetServer(r.Context(), tx, e.ServerID); err != nil {
			return err
		}
		srv, err := q.ServerByID(r.Context(), e.ServerID)
		if err != nil {
			return err
		}
		serverName, expires = srv.Name, e.ExpiresAt
		return nil
	})
	if err != nil {
		notValid()
		return
	}
	d := s.dist.load(s.cfg.DistDir)
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, installScript(installVars{
		ControlPlane: s.cfg.PublicURL, Token: token, ServerName: serverName, ExpiresAt: expires.UTC().Format(time.RFC3339),
		Version: d.version, SHA256AMD64: d.sha["amd64"], SHA256ARM64: d.sha["arm64"],
	}))
}

// handleDownload serves the agent binaries built into DistDir, and their SHA256SUMS.
func (s *Service) handleDownload(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "file")
	d := s.dist.load(s.cfg.DistDir)
	if name == "SHA256SUMS" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for _, arch := range []string{"amd64", "arm64"} {
			if d.sha[arch] != "" {
				fmt.Fprintf(w, "%s  cloudy-agent-linux-%s\n", d.sha[arch], arch)
			}
		}
		return
	}
	arch, ok := strings.CutPrefix(name, "cloudy-agent-linux-")
	if !ok || (arch != "amd64" && arch != "arm64") || d.sha[arch] == "" {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(s.cfg.DistDir, name)) //#nosec G304 -- name is one of two fixed values
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Checksum-Sha256", d.sha[arch])
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, time.Time{}, f)
}

// UpdateParams builds agent.update's params from the current build in DistDir, so the signed
// operation names the exact bytes to install.
func (s *Service) UpdateParams(arch string) (json.RawMessage, string, error) {
	if arch == "" {
		arch = "amd64"
	}
	d := s.dist.load(s.cfg.DistDir)
	if d.sha[arch] == "" {
		return nil, "", fmt.Errorf("no linux/%s agent build is available on this control plane (run `make agent`)", arch)
	}
	b, _ := json.Marshal(map[string]string{
		"version": d.version,
		"url":     s.cfg.PublicURL + "/agent/download/cloudy-agent-linux-" + arch,
		"sha256":  d.sha[arch],
	})
	return b, d.version, nil
}

// AvailableAgentVersion is the version of the agent build this control plane serves.
func (s *Service) AvailableAgentVersion() string { return s.dist.load(s.cfg.DistDir).version }

// handleEnroll is POST /agent/enroll.
func (s *Service) handleEnroll(w http.ResponseWriter, r *http.Request) {
	writeJSON := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	var req agentproto.EnrollRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req); err != nil {
		writeJSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	pub, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		writeJSON(http.StatusBadRequest, map[string]string{"error": "public_key must be a base64 ed25519 key"})
		return
	}
	token := strings.TrimSpace(req.Token)
	if !strings.HasPrefix(token, enrolPrefix) {
		writeJSON(http.StatusUnauthorized, map[string]string{"error": errBadToken.Error()})
		return
	}
	hash := tokens.Hash(token)

	// The verdict is the control plane's own, from the facts — not whatever checks the agent sent.
	checks := agentproto.EvaluateChecks(req.Facts)
	blocking := agentproto.Blocking(checks)
	fp := sha256.Sum256(pub)
	var resp agentproto.EnrollResponse
	errBlocked := errors.New("blocked")

	err = tenancy.Tx(r.Context(), s.a.SQLDB, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
		ctx := r.Context()
		e, err := lookupEnrollment(ctx, q, hash, s.now())
		if err != nil {
			return err
		}
		if len(blocking) > 0 {
			return errBlocked // the token stays valid: fix the host and re-run within the 15 minutes
		}
		if n, err := q.ConsumeEnrollment(ctx, e.ID); err != nil || n == 0 {
			return errBadToken
		}
		if err := tenancy.SetTeam(ctx, tx, e.TeamID); err != nil {
			return err
		}
		if err := tenancy.SetServer(ctx, tx, e.ServerID); err != nil {
			return err
		}
		factsJSON, _ := json.Marshal(req.Facts)
		checksJSON, _ := json.Marshal(checks)
		osLabel := req.Facts.OSID
		srv, err := q.EnrolServer(ctx, db.EnrolServerParams{
			AgentPublicKey: req.PublicKey,
			Hostname:       optional(clip(req.Facts.Hostname, 253)),
			Os:             optional(clip(osLabel, 64)),
			OsVersion:      optional(clip(req.Facts.OSVersion, 64)),
			Arch:           optional(clip(req.Facts.Arch, 32)),
			Kernel:         optional(clip(req.Facts.Kernel, 128)),
			AgentVersion:   optional(clip(req.AgentVersion, 64)),
			Facts:          ptr(string(factsJSON)),
			Checks:         ptr(string(checksJSON)),
			IpAddress:      optional(serverAddress(req.Facts.PrimaryIP, remoteIP(r))),
			ID:             e.ServerID,
			TeamID:         e.TeamID,
		})
		if err != nil {
			return err
		}
		if err := tenancy.Record(ctx, q, e.TeamID, "agent:"+e.ServerID, "server.enrolled", "server", e.ServerID, map[string]any{
			"hostname": req.Facts.Hostname, "os": strings.TrimSpace(req.Facts.OSID + " " + req.Facts.OSVersion),
			"agent_version": req.AgentVersion, "agent_key_sha256": hex.EncodeToString(fp[:]),
		}); err != nil {
			return err
		}
		resp = agentproto.EnrollResponse{ServerID: srv.ID, ServerName: srv.Name, ControlPlanePublicKey: s.pubB64,
			WebSocketURL: s.wsURL(), Checks: checks}
		return nil
	})
	switch {
	case errors.Is(err, errBlocked):
		writeJSON(http.StatusUnprocessableEntity, map[string]any{"error": "this server does not meet Hosbah's requirements", "checks": checks})
	case errors.Is(err, errBadToken):
		writeJSON(http.StatusUnauthorized, map[string]string{"error": errBadToken.Error()})
	case err != nil:
		s.log.Error("enrolment failed", "err", err)
		writeJSON(http.StatusInternalServerError, map[string]string{"error": "enrolment failed"})
	default:
		s.log.Info("agent enrolled", "server", resp.ServerID, "hostname", req.Facts.Hostname)
		s.broker.Publish(ServerTopic(resp.ServerID))
		writeJSON(http.StatusCreated, resp)
	}
}

func (s *Service) wsURL() string {
	u := s.cfg.PublicURL
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u + "/agent/ws"
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ---- agent builds ------------------------------------------------------------------------------

// distCache hashes the agent binaries once per (size, mtime), so a rebuild is picked up without a
// restart and the install script always names the checksum of the file actually served.
type distCache struct {
	mu      sync.Mutex
	entries map[string]distEntry
}

type distEntry struct {
	size int64
	mod  time.Time
	sha  string
}

type distInfo struct {
	version string
	sha     map[string]string
}

func (c *distCache) load(dir string) distInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]distEntry{}
	}
	out := distInfo{version: "dev", sha: map[string]string{}}
	if b, err := os.ReadFile(filepath.Join(dir, "VERSION")); err == nil { //#nosec G304 -- operator-configured dir
		if v := strings.TrimSpace(string(b)); v != "" {
			out.version = v
		}
	}
	for _, arch := range []string{"amd64", "arm64"} {
		path := filepath.Join(dir, "cloudy-agent-linux-"+arch)
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		if e, ok := c.entries[arch]; ok && e.size == fi.Size() && e.mod.Equal(fi.ModTime()) {
			out.sha[arch] = e.sha
			continue
		}
		f, err := os.Open(path) //#nosec G304 -- fixed names under the dist dir
		if err != nil {
			continue
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		_ = f.Close()
		if err != nil {
			continue
		}
		sum := hex.EncodeToString(h.Sum(nil))
		c.entries[arch] = distEntry{size: fi.Size(), mod: fi.ModTime(), sha: sum}
		out.sha[arch] = sum
	}
	return out
}

// serverAddress prefers the address the agent reports for itself: behind NPM or Cloudflare the TCP
// peer is the proxy, not the server.
func serverAddress(reported, peer string) string {
	if ip := net.ParseIP(strings.TrimSpace(reported)); ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
		return ip.String()
	}
	return peer
}
