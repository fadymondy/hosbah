package agentproto

import (
	"errors"
	"fmt"
	"strings"
)

// ExecParams: app.exec — Argv runs INSIDE the web container of the release DeploymentID made live
// (docker exec, no TTY, that container's own environment), never on the host. A schedule's command
// is a shell line by nature; it travels as ONE element — Argv = ["sh", "-lc", <command>] — and the
// shell that reads it is the container's. Neither side ever puts it into a host shell.
//
// Argv is a real argv: a caller that needs no shell sends the program and its arguments as they are
// (internal/queues: ["php", "artisan", "queue:retry", <id>]) and no shell reads them anywhere — the
// program is found on the image's PATH and runs in the image's working directory. ShellArgv is for
// the callers whose command IS a shell line; nothing else should use it.
type ExecParams struct {
	AppID          string   `json:"app_id"`
	DeploymentID   string   `json:"deployment_id"` // the live release's deployment: selects its web container by label
	Argv           []string `json:"argv"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

// ExecResult: app.exec. ExitCode is the command's own (ExecExitTimeout when it was killed at the
// timeout).
type ExecResult struct {
	Container string `json:"container"`
	ExitCode  int    `json:"exit_code"`
	TimedOut  bool   `json:"timed_out,omitempty"`
	Truncated bool   `json:"truncated,omitempty"` // the output passed MaxExecOutput; the rest was dropped
	Bytes     int    `json:"bytes"`               // bytes of output relayed
}

// Limits of app.exec, enforced on both sides.
const (
	MaxExecArgs           = 16
	MaxExecArg            = 4096
	MinExecTimeoutSeconds = 1
	MaxExecTimeoutSeconds = 3600
	MaxExecOutput         = 256 << 10 // bytes of output relayed per run
	// ExecExitTimeout is the exit code of a command killed at its timeout (as coreutils' timeout).
	ExecExitTimeout = 124
)

// ShellArgv wraps a shell line for ExecParams.Argv: the line is one element, read by the
// container's sh (a login shell, so the image's profile applies).
func ShellArgv(command string) []string { return []string{"sh", "-lc", command} }

// Validate checks app.exec's params, on both sides of the wire.
func (p ExecParams) Validate() error {
	switch {
	case !ValidID(p.AppID) || !ValidID(p.DeploymentID):
		return errors.New("bad app or deployment id")
	case len(p.Argv) == 0 || len(p.Argv) > MaxExecArgs:
		return fmt.Errorf("argv needs 1 to %d elements", MaxExecArgs)
	case p.TimeoutSeconds < MinExecTimeoutSeconds || p.TimeoutSeconds > MaxExecTimeoutSeconds:
		return fmt.Errorf("timeout_seconds must be between %d and %d", MinExecTimeoutSeconds, MaxExecTimeoutSeconds)
	}
	for i, a := range p.Argv {
		if len(a) > MaxExecArg || strings.ContainsRune(a, 0) {
			return fmt.Errorf("argv elements are at most %d bytes and hold no NUL", MaxExecArg)
		}
		if i == 0 && (a == "" || strings.HasPrefix(a, "-")) {
			return errors.New("argv[0] must name a program")
		}
	}
	return nil
}
