package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// guest.enrol: installs hosbah-agent inside one Proxmox guest of this node (checkDBTarget's own
// safety checks: this must be a node, and the guest must exist and be running — dbtarget.go), by
// handing the sealed install one-liner to `pct exec <vmid> -- sh -c <command>`. The command is
// opened with this agent's own key and is never logged, argv-visible in the agent's own process
// list beyond this one exec, or included in the operation's result; the guest's own output is
// relayed with its one-time token redacted, in case an install script or curl ever echoes the URL
// it was given.
func guestEnrol(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.GuestEnrolParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	pt, err := agentproto.OpenBox(oc.priv, agentproto.GuestEnrolContext(p.VMID), p.CommandBox)
	if err != nil {
		return nil, 1, errors.New("the install command could not be opened with this server's key")
	}
	defer zero(pt)
	if len(pt) == 0 || len(pt) > agentproto.MaxGuestEnrolCommand {
		return nil, 2, errors.New("the install command is empty or too long")
	}
	command := string(pt)

	if err := checkDBTarget(oc, dbTargetOf(p.VMID, "")); err != nil {
		return nil, 1, err
	}

	oc.Info("installing hosbah-agent inside guest %s", p.VMID)

	ctx, cancel := context.WithTimeout(oc.Ctx, agentproto.GuestEnrolTimeoutSeconds*time.Second)
	defer cancel()
	//#nosec G204 -- vmid is validated (ValidVMID); command is opened here from a control-plane-signed,
	// per-guest sealed box — never assembled from untrusted input.
	cmd := exec.CommandContext(ctx, "pct", "exec", p.VMID, "--", "sh", "-c", command)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()

	for _, line := range strings.Split(strings.TrimRight(redactInstallToken(out.String()), "\n"), "\n") {
		if line != "" {
			oc.log("stdout", clip(line, maxLine))
		}
	}

	if ctx.Err() == context.DeadlineExceeded {
		return agentproto.GuestEnrolResult{ExitCode: 124}, 124, errors.New("installing the agent inside the guest timed out")
	}
	if oc.Ctx.Err() != nil {
		return agentproto.GuestEnrolResult{}, 1, oc.Ctx.Err()
	}
	code := 0
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &exitErr):
		code = exitErr.ExitCode()
	default:
		return agentproto.GuestEnrolResult{}, 1, fmt.Errorf("could not run pct exec: %w", runErr)
	}
	if code != 0 {
		return agentproto.GuestEnrolResult{ExitCode: code}, code, fmt.Errorf("the install command exited with status %d", code)
	}
	return agentproto.GuestEnrolResult{ExitCode: 0}, 0, nil
}

// installTokenRE matches the one-time token in a Hosbah install URL (/install/<token>), so the
// guest's own output (an install script can echo the URL it curled) never repeats it in the clear.
var installTokenRE = regexp.MustCompile(`/install/[A-Za-z0-9_-]+`)

func redactInstallToken(s string) string {
	return installTokenRE.ReplaceAllString(s, "/install/[redacted]")
}
