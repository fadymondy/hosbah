// Package ops is the control plane's half of the agent protocol (internal/agentproto): enrolment
// and the install script, the agent WebSocket hub, the operations queue (dispatcher, timeout
// sweeper, control-plane worker) and the wake-ups behind the team-scoped log streams.
//
// Its database work runs in the narrowest tenancy scope that can do the job:
//
//   - ServerID scope for everything an authenticated agent causes: heartbeats, events, resume. The
//     row-level-security policies then show that connection its own server's rows and nothing
//     else — not even its team's other servers.
//   - Token scope (then the token's team and server) for enrolment and the install script.
//   - System scope only for the three workers that must see every team: claiming queued
//     operations for connected agents, timing out stale ones, and running control-plane
//     operations (provider provisioning). Never derived from a request.
package ops

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/hosbah/internal/app"
	db "github.com/fadymondy/hosbah/internal/db/gen"
)

// Config is where agents reach the control plane and where agent builds live.
type Config struct {
	// PublicURL is the origin agents use (CLOUDY_AGENT_URL). The install one-liner, the binary
	// download and the WebSocket URL an agent is given at enrolment all derive from it. In local
	// development with a container as the target: http://host.docker.internal:8710.
	PublicURL string
	// DistDir holds the built agent binaries (CLOUDY_AGENT_DIST, default bin/agent):
	// cloudy-agent-linux-amd64, cloudy-agent-linux-arm64 and VERSION (`make agent`).
	DistDir string
}

// ConfigFromEnv reads Config from the environment.
func ConfigFromEnv() Config {
	u := strings.TrimRight(strings.TrimSpace(os.Getenv("CLOUDY_AGENT_URL")), "/")
	if u == "" {
		u = "http://localhost:8710"
	}
	dist := strings.TrimSpace(os.Getenv("CLOUDY_AGENT_DIST"))
	if dist == "" {
		dist = "bin/agent"
	}
	return Config{PublicURL: u, DistDir: dist}
}

// OnlineWindow: a server is online if its agent heartbeated this recently (it heartbeats every
// 15 s, so three missed beats).
const OnlineWindow = 45 * time.Second

// Online derives "online" from the last heartbeat — never a stored flag a crash could leave stale.
func Online(lastSeen *time.Time, now time.Time) bool {
	return lastSeen != nil && now.Sub(*lastSeen) < OnlineWindow
}

// ControlPlaneRunner executes a control-plane operation (e.g. provider.create). It logs through
// log; its result is stored on the operation. The Service marks it running before and ok/failed
// after.
type ControlPlaneRunner func(ctx context.Context, op db.Operation, log func(stream, line string)) (result any, err error)

// Service is the control plane's agent side.
type Service struct {
	a      *app.App
	cfg    Config
	signer ed25519.PrivateKey
	pubB64 string
	hub    *hub
	broker *Broker
	wake   chan struct{}
	log    *slog.Logger
	now    func() time.Time
	dist   distCache

	runnersMu sync.RWMutex
	runners   map[string]ControlPlaneRunner

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New builds the service. The op-signing key comes from the keyring (derived from the KEK).
func New(a *app.App, cfg Config) (*Service, error) {
	priv, err := a.Keys.OpsSigningKey()
	if err != nil {
		return nil, fmt.Errorf("ops: signing key: %w", err)
	}
	pub, err := a.Keys.OpsPublicKey()
	if err != nil {
		return nil, err
	}
	log := a.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		a: a, cfg: cfg, signer: priv, pubB64: pub, hub: newHub(), broker: NewBroker(),
		wake: make(chan struct{}, 1), log: log.With("component", "ops"), now: time.Now,
		runners: map[string]ControlPlaneRunner{},
	}, nil
}

// Start launches the background workers. Stop ends them and closes every agent connection.
func (s *Service) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(3)
	go s.dispatchLoop(ctx)
	go s.sweepLoop(ctx)
	go s.controlPlaneLoop(ctx)
}

// Stop is the counterpart of Start.
func (s *Service) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.hub.closeAll()
	s.wg.Wait()
}

// Mount adds the agent-facing routes. They are outside /api on purpose: nothing here is a
// session route. /install and /agent/enroll authenticate a one-time token; /agent/ws a signed
// challenge; /agent/download serves public binaries.
func (s *Service) Mount(r chi.Router) {
	r.Get("/install/{token}", s.handleInstall)
	r.Get("/agent/download/{file}", s.handleDownload)
	r.Post("/agent/enroll", s.handleEnroll)
	r.Get("/agent/ws", s.handleWS)
}

// RegisterRunner sets the executor for a control-plane operation kind.
func (s *Service) RegisterRunner(kind string, r ControlPlaneRunner) {
	s.runnersMu.Lock()
	defer s.runnersMu.Unlock()
	s.runners[kind] = r
}

// HasRunner reports whether a control-plane kind can run here.
func (s *Service) HasRunner(kind string) bool {
	s.runnersMu.RLock()
	defer s.runnersMu.RUnlock()
	return s.runners[kind] != nil
}

// Wake nudges the dispatcher and the control-plane worker (they also poll every second).
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Broker is the in-process wake-up bus behind the SSE streams.
func (s *Service) Broker() *Broker { return s.broker }

// PublicKey is the base64 op-signing public key agents pin.
func (s *Service) PublicKey() string { return s.pubB64 }

// PublicURL is the origin agents are told to use.
func (s *Service) PublicURL() string { return s.cfg.PublicURL }

// Connected reports whether a server's agent has a live connection to THIS process.
func (s *Service) Connected(serverID string) bool { return s.hub.get(serverID) != nil }

// ServerTopic / OpTopic name the broker topics.
func ServerTopic(serverID string) string { return "server:" + serverID }
func OpTopic(opID string) string         { return "op:" + opID }

// DeployTopic / AppTopic: a deployment's log and status; an app's page (status, deployments).
func DeployTopic(deploymentID string) string { return "deploy:" + deploymentID }
func AppTopic(appID string) string           { return "app:" + appID }

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func ptr[T any](v T) *T { return &v }
