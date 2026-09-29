package agentproto

import "errors"

// guest.enrol (P1 nested guest agents): installs hosbah-agent INSIDE one Proxmox guest (LXC or
// QEMU) of the node this operation runs on, by handing the guest's own one-time install one-liner
// (already carrying a fresh enrolment token, see ops.NewEnrollment) to `pct exec <vmid> -- sh -c
// <command>`. Only a node (pct present), with the guest present and running, ever accepts this —
// see the agent's checkDBTarget, reused here.
//
// The install command carries a one-time token in the clear (it is, after all, meant to be typed
// or piped into a shell). It never sits in operations.params, operations.result or a log line
// unredacted: the control plane seals it to this very node's own agent key (SealBox — the same
// mechanism a deploy's env values or passwd.set's password use), and the agent only ever opens it
// inside the handler, redacting the token from anything it logs afterwards.
const KindGuestEnrol = "guest.enrol"

// GuestEnrolContext binds a sealed install command to the guest it installs into, so a box copied
// into another operation (or a replayed frame for a different vmid) does not open.
func GuestEnrolContext(vmid string) string { return "guest:" + vmid + ":enrol" }

// GuestEnrolTimeoutSeconds bounds how long `pct exec` is given to install and enrol the agent
// inside the guest — a few minutes: a package install plus one HTTP round trip to /agent/enroll.
const GuestEnrolTimeoutSeconds = 180

// MaxGuestEnrolCommand bounds the sealed command's plaintext size: the install one-liner is short
// (curl -fsSL <url> | sh), never anything close to this.
const MaxGuestEnrolCommand = 4096

// GuestEnrolParams: guest.enrol. CommandBox is
// SealBox(this node's own agent key, GuestEnrolContext(vmid), commandRoot).
type GuestEnrolParams struct {
	VMID       string `json:"vmid"`
	CommandBox string `json:"command_box"`
}

// Validate checks guest.enrol's params, on both sides of the wire. It cannot check the command's
// own length or content — that is sealed — the agent bounds it after opening the box.
func (p GuestEnrolParams) Validate() error {
	if !ValidVMID(p.VMID) {
		return errors.New("bad vmid")
	}
	if p.CommandBox == "" {
		return errors.New("missing command_box")
	}
	return nil
}

// GuestEnrolResult: guest.enrol. Deliberately holds nothing about the install command or its
// output beyond the exit code — the agent's own log lines (redacted) carry the rest.
type GuestEnrolResult struct {
	ExitCode int `json:"exit_code"`
}
