package ops

import (
	"encoding/json"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// GitHubState is a server's GitHub CLI connection as the control plane last heard it (servers.github).
type GitHubState struct {
	Installed bool          `json:"installed"`
	Version   string        `json:"version,omitempty"`
	Connected bool          `json:"connected"`
	User      string        `json:"user,omitempty"`
	Scopes    []string      `json:"scopes,omitempty"`
	CheckedAt *time.Time    `json:"checked_at,omitempty"`
	Error     string        `json:"error,omitempty"`
	Pending   *GitHubDevice `json:"pending,omitempty"`
}

// GitHubDevice is a device login waiting for a human: the code to enter and where.
type GitHubDevice struct {
	Operation       string    `json:"operation"`
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// maxEventData bounds what an agent may attach to one event.
const maxEventData = 4 << 10

// cleanEventData keeps an event's Data only when it is a small JSON object.
func cleanEventData(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || len(raw) > maxEventData {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return raw
}

func isGitHubKind(k string) bool {
	for _, g := range agentproto.GitHubKinds {
		if g == k {
			return true
		}
	}
	return false
}

// githubPatch is what one event of a github.* operation changes in servers.github (nil: nothing).
// Keys set to null are removed by MergeServerGitHub.
func githubPatch(kind, opID string, ev agentproto.Event, now time.Time) []byte {
	if !isGitHubKind(kind) {
		return nil
	}
	switch {
	case ev.Kind == agentproto.EventLog && kind == agentproto.KindGitHubLogin && len(ev.Data) > 0:
		var dc agentproto.GitHubDeviceCode
		if json.Unmarshal(ev.Data, &dc) != nil || dc.Type != agentproto.DataGitHubDeviceCode || len(dc.UserCode) > 16 || len(dc.VerificationURI) > 200 {
			return nil
		}
		b, _ := json.Marshal(map[string]any{"pending": GitHubDevice{Operation: opID, UserCode: dc.UserCode,
			VerificationURI: dc.VerificationURI, ExpiresAt: dc.ExpiresAt}})
		return b
	case ev.Kind != agentproto.EventStatus:
		return nil
	case ev.Status == agentproto.StatusFailed:
		if kind != agentproto.KindGitHubLogin && kind != agentproto.KindGitHubLogout {
			return nil // a failed repo listing says nothing about the connection itself
		}
		msg := ev.Error
		if len(msg) > 300 {
			msg = msg[:300]
		}
		b, _ := json.Marshal(map[string]any{"pending": nil, "error": msg})
		return b
	case ev.Status != agentproto.StatusOK:
		return nil
	}
	switch kind {
	case agentproto.KindGitHubStatus, agentproto.KindGitHubLogin, agentproto.KindGitHubLogout, agentproto.KindGitHubInstall:
	default:
		return nil
	}
	var st agentproto.GitHubStatus
	if json.Unmarshal(ev.Result, &st) != nil {
		return nil
	}
	if st.User != "" && !agentproto.ValidGitHubOwner(st.User) {
		st.User = ""
	}
	var scopes []string
	for _, s := range st.Scopes {
		if len(s) <= 40 && len(scopes) < 30 {
			scopes = append(scopes, s)
		}
	}
	patch := map[string]any{"installed": st.Installed, "version": nullIfEmpty(clipStr(st.Version, 40)), "connected": st.LoggedIn && st.User != "",
		"user": nullIfEmpty(st.User), "scopes": scopes, "checked_at": now.UTC(), "error": nil}
	if kind == agentproto.KindGitHubLogin || kind == agentproto.KindGitHubLogout {
		patch["pending"] = nil // the login finished (or was undone): no code is waiting any more
	}
	b, _ := json.Marshal(patch)
	return b
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func clipStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
