// Package agent is hosbah-agent (cloudy-agent before the rename): the small program on every managed server. It enrols once with
// a one-time token, then dials OUT to the control plane over a WebSocket, answers a signed
// challenge with its own ed25519 key, heartbeats with metrics, and executes the typed operations
// the control plane signs — nothing else. cmd/agent is the thin binary around it.
package agent

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// DefaultStatePath is where enrolment leaves the agent's identity.
// It was /etc/cloudy/agent.json before the rename (MigrateStateDirs moves it; StatePath finds it).
const DefaultStatePath = agentproto.StateDir + "/agent.json"

// State is what enrolment writes to disk (mode 0600): who this agent is and whom it trusts.
type State struct {
	ControlPlane          string    `json:"control_plane"` // http(s) origin the agent enrolled with
	WebSocketURL          string    `json:"ws_url"`
	ServerID              string    `json:"server_id"`
	ServerName            string    `json:"server_name"`
	PrivateKey            string    `json:"private_key"`              // base64 ed25519 private key; never leaves the host
	ControlPlanePublicKey string    `json:"control_plane_public_key"` // base64 ed25519, pinned at enrolment
	EnrolledAt            time.Time `json:"enrolled_at"`
}

// LoadState reads a state file written by Save.
func LoadState(path string) (*State, error) {
	b, err := os.ReadFile(path) //#nosec G304 -- operator-chosen path
	if err != nil {
		return nil, fmt.Errorf("read %s: %w (has this server been enrolled?)", path, err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if _, _, err := s.Keys(); err != nil {
		return nil, err
	}
	if s.ServerID == "" || s.WebSocketURL == "" {
		return nil, errors.New("state file is incomplete: missing server id or WebSocket URL")
	}
	return &s, nil
}

// Save writes the state atomically with owner-only permissions.
func (s *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Keys decodes the agent's private key and the pinned control-plane public key.
func (s *State) Keys() (ed25519.PrivateKey, ed25519.PublicKey, error) {
	priv, err := base64.StdEncoding.DecodeString(s.PrivateKey)
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return nil, nil, errors.New("state file: the agent's private key is missing or malformed")
	}
	pub, err := base64.StdEncoding.DecodeString(s.ControlPlanePublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, nil, errors.New("state file: the control plane's public key is missing or malformed")
	}
	return ed25519.PrivateKey(priv), ed25519.PublicKey(pub), nil
}
