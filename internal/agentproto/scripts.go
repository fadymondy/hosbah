package agentproto

import (
	"errors"
	"fmt"
)

// script.run (MH-576 D4): a team's own script (or a built-in one, internal/scripts/builtin) run
// with bash on one server, in a fresh temp directory, as root. Body is the script text the control
// plane signed — never assembled from a template on the agent, so nothing here reads untrusted
// input into a shell. Args and Env are passed to the child process as environment variables, never
// interpolated into Body or an argv: a caller-supplied value can hold anything and is still just
// data to the shell that runs the script.
//
// Secret values (marked so by the caller) never appear in Env: they travel sealed in EnvBox, to
// this server's own key (SealBox / OpenEnv), exactly like a deploy's env — so operations.params
// never holds plaintext.
type ScriptRunParams struct {
	ScriptRunID string            `json:"script_run_id"` // script_run_servers.id: also the temp dir's identity and the env box's context
	Name        string            `json:"name"`           // for logging only
	Body        string            `json:"body"`
	Args        map[string]string `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"` // non-secret env
	EnvBox      string            `json:"env_box,omitempty" doc:"SealBox(ScriptEnvContext(script_run_id)) of the secret env values, or empty."`
	TimeoutSeconds int            `json:"timeout_seconds"`
}

// ScriptRunResult: script.run.
type ScriptRunResult struct {
	ExitCode  int  `json:"exit_code"`
	TimedOut  bool `json:"timed_out,omitempty"`
	Truncated bool `json:"truncated,omitempty"` // the output passed MaxScriptOutput; the rest was dropped
	Bytes     int  `json:"bytes"`
}

// Limits of script.run, enforced on both sides.
const (
	MaxScriptBody          = 1 << 20 // 1 MiB of script text
	MinScriptTimeoutSeconds = 1
	MaxScriptTimeoutSeconds = 3600
	MaxScriptOutput         = 1 << 20 // bytes of output kept per run (last N)
	MaxScriptEnvVars        = 64
	MaxScriptEnvKey         = 128
	MaxScriptEnvValue       = 8192
	// ScriptExitTimeout is the exit code of a script killed at its timeout.
	ScriptExitTimeout = 124
)

// ScriptEnvContext binds a script run's sealed env box to that run alone.
func ScriptEnvContext(scriptRunServerID string) string { return "script:" + scriptRunServerID + ":env" }

// Validate checks script.run's params, on both sides of the wire.
func (p ScriptRunParams) Validate() error {
	switch {
	case !ValidID(p.ScriptRunID):
		return errors.New("bad script_run_id")
	case p.Body == "" || len(p.Body) > MaxScriptBody:
		return fmt.Errorf("the script body must be 1 to %d bytes", MaxScriptBody)
	case p.TimeoutSeconds < MinScriptTimeoutSeconds || p.TimeoutSeconds > MaxScriptTimeoutSeconds:
		return fmt.Errorf("timeout_seconds must be between %d and %d", MinScriptTimeoutSeconds, MaxScriptTimeoutSeconds)
	case len(p.Args)+len(p.Env) > MaxScriptEnvVars:
		return fmt.Errorf("at most %d args/env vars", MaxScriptEnvVars)
	}
	for k, v := range p.Args {
		if err := validEnvVar(k, v); err != nil {
			return err
		}
	}
	for k, v := range p.Env {
		if err := validEnvVar(k, v); err != nil {
			return err
		}
	}
	return nil
}

func validEnvVar(k, v string) error {
	if k == "" || len(k) > MaxScriptEnvKey {
		return fmt.Errorf("bad variable name %q", k)
	}
	if len(v) > MaxScriptEnvValue {
		return fmt.Errorf("value of %q is too long", k)
	}
	for _, r := range k {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return fmt.Errorf("bad variable name %q", k)
		}
	}
	return nil
}
