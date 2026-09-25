package agentproto

import (
	"encoding/base64"
	"errors"
	"strings"
)

/*
A web terminal for a server (and, cheaply, for an app's web container). Unlike every other
operation kind, terminal.open does not run to completion and report a result: it starts an
interactive PTY that streams bytes both ways over the same agent WebSocket, tagged by SessionID
in TypeTermData / TypeTermResize / TypeTermClose frames — never through the operation's log (a
keystroke is never persisted). The control plane still signs the open the same as any other
operation, so the agent only ever opens a PTY the control plane asked for.
*/

// KindTerminalOpen: the signed op that starts a session. The agent spawns the PTY, streams a
// short-lived "running" status through the normal event/log channel (so the browser learns the
// open succeeded or failed) and then only ever exchanges term.* frames for SessionID.
const KindTerminalOpen = "terminal.open"

// TerminalTargetHost: a login shell on the server itself (/bin/bash -l, falling back to /bin/sh).
const TerminalTargetHost = "host"

// TerminalAppPrefix: TerminalTarget "app:<app_id>/<process>" is a shell inside that process's
// live container (docker exec -it, sh -c 'bash || sh').
const TerminalAppPrefix = "app:"

// Limits, enforced on both sides.
const (
	MinTermCols, MaxTermCols = 10, 500
	MinTermRows, MaxTermRows = 4, 200
	// MaxTermFrame bounds one term.data payload's decoded size — generous for a paste, far below
	// what a runaway process could use to flood the control plane.
	MaxTermFrame = 64 << 10
	// MaxTermSessionSeconds bounds a whole session (4 hours); MaxTermIdleSeconds bounds the gap
	// since the last keystroke or resize before the agent closes it on its own.
	MaxTermSessionSeconds = 4 * 3600
	MaxTermIdleSeconds    = 15 * 60
)

// TerminalOpenParams: terminal.open.
type TerminalOpenParams struct {
	SessionID string `json:"session_id"`
	// Target is "host" or "app:<app_id>/<process>" (process is "web" or "worker:<name>", the same
	// vocabulary as app.exec/app.processes).
	Target string `json:"target"`
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
	// DeploymentID selects the app target's live web/worker container, the same as app.exec.
	DeploymentID string `json:"deployment_id,omitempty"`
}

// Validate checks terminal.open's params, on both sides of the wire.
func (p TerminalOpenParams) Validate() error {
	if !ValidID(p.SessionID) {
		return errors.New("bad session id")
	}
	if p.Cols < MinTermCols || p.Cols > MaxTermCols || p.Rows < MinTermRows || p.Rows > MaxTermRows {
		return errors.New("cols/rows are out of range")
	}
	if p.Target == TerminalTargetHost {
		return nil
	}
	appID, process, ok := ParseTerminalAppTarget(p.Target)
	if !ok || !ValidID(appID) || !ValidProcess(process) {
		return errors.New(`target must be "host" or "app:<app_id>/<process>"`)
	}
	if !ValidID(p.DeploymentID) {
		return errors.New("an app target needs its live deployment id")
	}
	return nil
}

// ParseTerminalAppTarget splits "app:<app_id>/<process>".
func ParseTerminalAppTarget(target string) (appID, process string, ok bool) {
	rest, found := strings.CutPrefix(target, TerminalAppPrefix)
	if !found {
		return "", "", false
	}
	appID, process, found = strings.Cut(rest, "/")
	return appID, process, found && appID != "" && process != ""
}

// ValidTermData reports whether a term.data frame's base64 payload decodes within MaxTermFrame.
func ValidTermData(b64 string) bool {
	if b64 == "" {
		return true
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	return err == nil && len(raw) <= MaxTermFrame
}

// ValidTermSize reports whether a resize is within bounds.
func ValidTermSize(cols, rows int) bool {
	return cols >= MinTermCols && cols <= MaxTermCols && rows >= MinTermRows && rows <= MaxTermRows
}
