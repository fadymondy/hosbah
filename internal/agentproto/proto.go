// Package agentproto is the wire contract between the Hosbah control plane and hosbah-agent.
// Both sides import it, so a message shape or a signature layout cannot drift between them.
//
// Transport: one WebSocket per agent, dialled OUT by the agent to /agent/ws. Every message is a
// JSON Frame. The sequence on connect is
//
//	server -> {type:"challenge", nonce}
//	agent  -> {type:"auth", server_id, signature: ed25519(AuthSigningBytes(nonce, server_id)), agent_version}
//	server -> {type:"welcome", server_id}                       (or {type:"error"} and close)
//	server -> {type:"resume", resume:[{op_id, last_seq}...]}    ops the server thinks are in flight
//	agent  -> events after last_seq for each (or status "unknown" so the server re-queues it)
//
// and then, in any order: heartbeats (agent -> server, with metrics), ops (server -> agent,
// signed), events (agent -> server: log | progress | status, each with op_id and a per-op seq).
//
// Signed operations: the control plane signs OpSigningBytes with its ed25519 key; the agent
// pinned that public key at enrolment and refuses anything that does not verify, is for another
// server, has expired, or names a kind outside the typed allowlist. A relay (Cloudflare, a proxy,
// a compromised network) can drop or delay operations but cannot forge or alter one.
package agentproto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// ProtocolVersion is bumped on an incompatible change to the frames below.
const ProtocolVersion = 1

// Frame types.
const (
	TypeChallenge = "challenge"
	TypeAuth      = "auth"
	TypeWelcome   = "welcome"
	TypeResume    = "resume"
	TypeOp        = "op"
	TypeEvent     = "event"
	TypeHeartbeat = "heartbeat"
	TypeError     = "error"
	// TypeMetrics is a light, frequent reading (agent -> server, Metrics only) between heartbeats,
	// for live charts. It does not count as a heartbeat; control planes that predate it ignore it.
	TypeMetrics = "metrics"
	// TypeTraffic carries one or more apps' finished minute of Caddy access-log aggregates
	// (agent -> server, Traffic only), sent once a minute. Control planes that predate it ignore
	// it (an unknown frame type is simply not switched on).
	TypeTraffic = "traffic"
	// TypeTermData carries one chunk of a terminal.open session's bytes, either direction, base64
	// in TermData, tagged by SessionID. Never logged, never persisted.
	TypeTermData = "term_data"
	// TypeTermResize (browser -> agent, through the control plane) changes a session's PTY size.
	TypeTermResize = "term_resize"
	// TypeTermClose ends a session, either direction: the control plane asking the agent to kill
	// it, or the agent reporting the process exited or the session was refused/timed out. TermReason
	// is a short, human phrase — never anything the process printed.
	TypeTermClose = "term_close"
)

// Operation kinds. Typed only: there is no free-form shell.
const (
	KindSystemInfo    = "system.info"
	KindDockerCheck   = "docker.check"
	KindProvisionStep = "provision.step"
	KindAgentUpdate   = "agent.update"
)

// AgentKinds is what an agent will execute. Anything else is refused before it runs.
var AgentKinds = []string{KindSystemInfo, KindDockerCheck, KindProvisionStep, KindAgentUpdate,
	KindAppDetect, KindAppBuild, KindAppFastBuild, KindAppRelease, KindAppStart, KindRouteSet, KindAppDrain,
	KindAppRollback, KindAppLogs, KindAppRemove, KindOpCancel, KindAppExec, KindAppProcesses,
	KindDBProvision, KindDBCreate, KindDBBackup, KindDBRestore, KindDBRemove, KindProxySet, KindDBDrop, KindTunnelFrp, KindTunnelTailscale,
	KindWorkloadPower, KindWorkloadLogs, KindWorkloadStats, KindGuestResize, KindGuestSnapshot, KindGuestBackup,
	KindContainerRecreate, KindWorkloadDestroy, KindDBProbe,
	KindGitHubInstall, KindGitHubLogin, KindGitHubStatus, KindGitHubLogout, KindGitHubRepos, KindGitHubBranches,
	KindGitHubCommits, KindGitHubCompare, KindGitHubPulls, KindCaddyCerts,
	KindVolumeUsage, KindVolumeRemove, KindBucketCreate, KindBucketRemove, KindRealtimeRoute,
	KindBrowserCreate, KindBrowserRemove, KindBrowserStatus,
	KindRecipeInstall, KindRecipeRemove, KindRecipeStatus,
	KindFSList, KindFSRead, KindFSWrite, KindFSMkdir, KindFSDelete, KindFSRename, KindTerminalOpen, KindSFTPUser,
	KindAppLogsSearch, KindLogDrainSet, KindLogDrainStatus, KindLogDrainTest}

// Provision steps (params.step of provision.step). Each is idempotent: it checks the current state
// first and reports "already done" rather than re-applying.
const (
	StepBasePackages  = "packages.base"
	StepDockerInstall = "docker.install"
	StepSwap          = "swap.ensure"
)

// ProvisionSteps is the allowlist, in the order a fresh server runs them.
var ProvisionSteps = []string{StepBasePackages, StepDockerInstall, StepSwap, StepBuildTools, StepCaddy, StepRailpack}

// Event kinds and statuses.
const (
	EventLog      = "log"
	EventProgress = "progress"
	EventStatus   = "status"

	StatusRunning  = "running"
	StatusOK       = "ok"
	StatusFailed   = "failed"
	StatusRejected = "rejected" // the agent refused the op (bad signature, expired, unknown kind…)
	StatusUnknown  = "unknown"  // resume: the agent never received this op
)

// Frame is every WebSocket message.
type Frame struct {
	Type         string          `json:"type"`
	Nonce        string          `json:"nonce,omitempty"`
	ServerID     string          `json:"server_id,omitempty"`
	Signature    string          `json:"signature,omitempty"`
	AgentVersion string          `json:"agent_version,omitempty"`
	Protocol     int             `json:"protocol,omitempty"`
	Op           *SignedOp       `json:"op,omitempty"`
	Event        *Event          `json:"event,omitempty"`
	Metrics      *Metrics        `json:"metrics,omitempty"`
	Traffic      []TrafficMinute `json:"traffic,omitempty"`
	Resume       []ResumeEntry   `json:"resume,omitempty"`
	Error        string          `json:"error,omitempty"`
	// SessionID, TermCols/TermRows, TermData (base64) and TermReason carry TypeTermData /
	// TypeTermResize / TypeTermClose. Kept on Frame, not a sub-struct, so relaying one costs no
	// extra allocation on the hot path an interactive session leans on.
	SessionID  string `json:"session_id,omitempty"`
	TermCols   int    `json:"term_cols,omitempty"`
	TermRows   int    `json:"term_rows,omitempty"`
	TermData   string `json:"term_data,omitempty"`
	TermReason string `json:"term_reason,omitempty"`
}

// SignedOp is an operation as the agent receives it.
type SignedOp struct {
	OpID           string          `json:"op_id"`
	ServerID       string          `json:"server_id"`
	Kind           string          `json:"kind"`
	Params         json.RawMessage `json:"params"`
	ParamsHash     string          `json:"params_hash"` // hex SHA-256 of the compacted Params
	Exp            int64           `json:"exp"`         // unix seconds; the agent refuses a NEW op after this
	TimeoutSeconds int64           `json:"timeout_seconds"`
	Sig            string          `json:"sig"` // base64 ed25519 over OpSigningBytes
}

// Event is one thing an operation emitted. Seq is per operation, starting at 1, contiguous.
type Event struct {
	OpID     string          `json:"op_id"`
	Seq      int64           `json:"seq"`
	Kind     string          `json:"event"`
	Stream   string          `json:"stream,omitempty"` // log: stdout | stderr | info
	Line     string          `json:"line,omitempty"`
	Step     int             `json:"step,omitempty"` // progress: step of total
	Total    int             `json:"total,omitempty"`
	Status   string          `json:"status,omitempty"`
	ExitCode *int            `json:"exit_code,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
	// Data is a typed payload a client can act on while the operation runs (a JSON object with a
	// "type", e.g. DataGitHubDeviceCode). Old agents never send it; old control planes drop it.
	Data json.RawMessage `json:"data,omitempty"`
	At   time.Time       `json:"at"`
}

// ResumeEntry: the server holds events of OpID up to LastSeq.
type ResumeEntry struct {
	OpID    string `json:"op_id"`
	LastSeq int64  `json:"last_seq"`
}

// Metrics is the heartbeat payload.
type Metrics struct {
	CPUPercent     float64   `json:"cpu_percent"`
	MemUsedBytes   uint64    `json:"mem_used_bytes"`
	MemTotalBytes  uint64    `json:"mem_total_bytes"`
	DiskUsedBytes  uint64    `json:"disk_used_bytes"`
	DiskTotalBytes uint64    `json:"disk_total_bytes"`
	UptimeSeconds  uint64    `json:"uptime_seconds"`
	Load1          float64   `json:"load1"`
	At             time.Time `json:"at"`
	// Docker, when present, is a fresh reading of the engine that the control plane merges into
	// the server's facts.docker so it does not go stale after enrolment. Sampled at a slower
	// cadence than the rest of the heartbeat, so it is nil on most beats.
	Docker *DockerFacts `json:"docker,omitempty"`
	// GPUs is the last NVIDIA reading (nvidia-smi, re-read on a slower cadence and repeated on
	// the beats between). Empty without an NVIDIA GPU or from older agents.
	GPUs []GPUMetric `json:"gpus,omitempty"`
	// NetRxBytesPerSec / NetTxBytesPerSec: traffic since the previous beat over the host's
	// non-virtual interfaces (/proc/net/dev deltas). Zero on the first beat.
	NetRxBytesPerSec float64 `json:"net_rx_bytes_per_sec,omitempty"`
	NetTxBytesPerSec float64 `json:"net_tx_bytes_per_sec,omitempty"`
}

// KeyCount is one entry of a ranked list (a path or a referrer host, and how many requests it
// had), already sorted and capped by the agent.
type KeyCount struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// TrafficMinute is one app's Caddy access-log aggregate for one whole, finished minute. The agent
// builds it from its own routes table (which app a hostname belongs to) and Caddy's JSON access
// log; the control plane never sees a raw IP address or user agent, only these counts.
type TrafficMinute struct {
	AppID          string     `json:"app_id"`
	At             time.Time  `json:"at" doc:"Start of the minute (UTC, truncated to the minute)."`
	Requests       int64      `json:"requests"`
	Status2xx      int64      `json:"status_2xx"`
	Status3xx      int64      `json:"status_3xx"`
	Status4xx      int64      `json:"status_4xx"`
	Status5xx      int64      `json:"status_5xx"`
	BytesOut       int64      `json:"bytes_out"`
	DurationP50Ms  float64    `json:"duration_p50_ms"`
	DurationP95Ms  float64    `json:"duration_p95_ms"`
	UniqueVisitors int64      `json:"unique_visitors" doc:"Distinct sha256(ip+user_agent) seen in the minute."`
	TopPaths       []KeyCount `json:"top_paths"`
	TopReferrers   []KeyCount `json:"top_referrers"`
}

// ---- signing ---------------------------------------------------------------------------------

// ErrRefused wraps every reason an agent refuses an operation.
var ErrRefused = errors.New("operation refused")

// CanonicalParams compacts params JSON, so re-encoding by a relay (whitespace) does not change the
// hash. Empty params are "{}".
func CanonicalParams(params []byte) ([]byte, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return []byte("{}"), nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, params); err != nil {
		return nil, fmt.Errorf("params are not valid JSON: %w", err)
	}
	return buf.Bytes(), nil
}

// ParamsHash is the hex SHA-256 of the canonical params.
func ParamsHash(params []byte) (string, error) {
	c, err := CanonicalParams(params)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c)
	return hex.EncodeToString(sum[:]), nil
}

// OpSigningBytes is exactly what the control plane signs: the domain tag and every field that
// decides what runs and where. Newline-separated; none of the fields can contain a newline
// (ids are uuids, kinds are from the allowlist, the hash is hex, the rest are integers).
func OpSigningBytes(op *SignedOp) []byte {
	// "cloudy-op-v1" is frozen (pre-rename): agents that have not updated verify with it.
	return []byte("cloudy-op-v1\n" + op.OpID + "\n" + op.ServerID + "\n" + op.Kind + "\n" +
		op.ParamsHash + "\n" + strconv.FormatInt(op.Exp, 10) + "\n" + strconv.FormatInt(op.TimeoutSeconds, 10))
}

// SignOp canonicalises params, fills ParamsHash and signs.
func SignOp(priv ed25519.PrivateKey, op *SignedOp) error {
	c, err := CanonicalParams(op.Params)
	if err != nil {
		return err
	}
	op.Params = c
	if op.ParamsHash, err = ParamsHash(c); err != nil {
		return err
	}
	op.Sig = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, OpSigningBytes(op)))
	return nil
}

// VerifyOp is the agent's gate. It checks, in order: the signature is present and valid for the
// pinned control-plane key, the params match their signed hash, the op is for THIS server, it has
// not expired, and its kind is on the allowlist. Any failure is ErrRefused with the reason.
func VerifyOp(pub ed25519.PublicKey, op *SignedOp, serverID string, now time.Time) error {
	refuse := func(reason string) error { return fmt.Errorf("%w: %s", ErrRefused, reason) }
	if op == nil {
		return refuse("no operation")
	}
	if len(pub) != ed25519.PublicKeySize {
		return refuse("no pinned control-plane key")
	}
	sig, err := base64.StdEncoding.DecodeString(op.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return refuse("unsigned or malformed signature")
	}
	if !ed25519.Verify(pub, OpSigningBytes(op), sig) {
		return refuse("signature does not verify")
	}
	hash, err := ParamsHash(op.Params)
	if err != nil || hash != op.ParamsHash {
		return refuse("params do not match the signed hash")
	}
	if op.ServerID != serverID {
		return refuse("operation is for another server")
	}
	if now.Unix() > op.Exp {
		return refuse("operation expired")
	}
	if !contains(AgentKinds, op.Kind) {
		return refuse("unknown operation kind " + strconv.Quote(op.Kind))
	}
	return nil
}

// AuthSigningBytes is what an agent signs to answer a challenge.
func AuthSigningBytes(nonce, serverID string) []byte {
	// "cloudy-agent-auth-v1" is frozen (pre-rename): control planes on both sides of an upgrade check it.
	return []byte("cloudy-agent-auth-v1\n" + nonce + "\n" + serverID)
}

// VerifyAuth checks an agent's challenge answer against its enrolled public key.
func VerifyAuth(pubB64, nonce, serverID, sigB64 string) bool {
	pub, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, AuthSigningBytes(nonce, serverID), sig)
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
