package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fadymondy/cloudy/internal/agentproto"
)

// EnrollError is a refused enrolment, with the control plane's verdict on each check.
type EnrollError struct {
	Status  int
	Message string
	Checks  []agentproto.Check
}

func (e *EnrollError) Error() string {
	return fmt.Sprintf("enrolment refused (%d): %s", e.Status, e.Message)
}

// Enroll registers this host with the control plane using a one-time token. The agent generates
// its ed25519 key here; only the public half is sent. On success the returned State is ready to
// Save. The checks are the control plane's verdict (it re-evaluates the facts itself).
func Enroll(ctx context.Context, controlPlane, token, version string, client *http.Client) (*State, []agentproto.Check, error) {
	return EnrollWithFacts(ctx, controlPlane, token, version, client, CollectFacts(ctx))
}

// EnrollWithFacts is Enroll with the host facts supplied (tests on a non-Linux machine).
func EnrollWithFacts(ctx context.Context, controlPlane, token, version string, client *http.Client, facts agentproto.Facts) (*State, []agentproto.Check, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	req := agentproto.EnrollRequest{
		Token:        strings.TrimSpace(token),
		PublicKey:    base64.StdEncoding.EncodeToString(pub),
		AgentVersion: version,
		Facts:        facts,
		Checks:       agentproto.EvaluateChecks(facts),
	}
	body, _ := json.Marshal(req)
	base := strings.TrimRight(controlPlane, "/")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/agent/enroll", bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "cloudy-agent/"+version)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, req.Checks, fmt.Errorf("could not reach the control plane at %s: %w", base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		var e struct {
			Error  string             `json:"error"`
			Checks []agentproto.Check `json:"checks"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(raw))
		}
		checks := e.Checks
		if checks == nil {
			checks = req.Checks
		}
		return nil, checks, &EnrollError{Status: resp.StatusCode, Message: e.Error, Checks: checks}
	}
	var out agentproto.EnrollResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, req.Checks, fmt.Errorf("unexpected enrolment response: %w", err)
	}
	if out.ServerID == "" || out.WebSocketURL == "" || out.ControlPlanePublicKey == "" {
		return nil, req.Checks, fmt.Errorf("incomplete enrolment response")
	}
	return &State{
		ControlPlane:          base,
		WebSocketURL:          out.WebSocketURL,
		ServerID:              out.ServerID,
		ServerName:            out.ServerName,
		PrivateKey:            base64.StdEncoding.EncodeToString(priv),
		ControlPlanePublicKey: out.ControlPlanePublicKey,
		EnrolledAt:            time.Now().UTC(),
	}, out.Checks, nil
}
