package ops

import (
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

type installVars struct {
	ControlPlane, Token, ServerName, ExpiresAt, Version, SHA256AMD64, SHA256ARM64 string
}

// shq single-quotes a value for POSIX sh.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// installScript renders the BYO installer. It is plain POSIX sh (dash on Debian/Ubuntu), runs as
// root, downloads the agent from the control plane, verifies its SHA-256 against the value embedded
// here, installs it, enrols with the one-time token (the agent runs the enrolment checks), and
// starts it under systemd — or detached, with a warning, where there is no systemd.
//
// What the checksum does and does not prove: the script and the binary come from the same control
// plane, so the check catches a truncated or corrupted download, not a malicious control plane.
// The production release channel (goreleaser → GitHub Releases) should verify a signed checksum
// file instead.
func installScript(v installVars) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Hosbah agent installer for server " + oneLine(v.ServerName) + ".\n")
	b.WriteString("# Generated for ONE enrolment: the token below works once and expires at " + v.ExpiresAt + ".\n")
	b.WriteString("set -eu\n")
	b.WriteString("HOSBAH_URL=" + shq(v.ControlPlane) + "\n")
	b.WriteString("TOKEN=" + shq(v.Token) + "\n")
	b.WriteString("VERSION=" + shq(v.Version) + "\n")
	b.WriteString("SHA256_AMD64=" + shq(v.SHA256AMD64) + "\n")
	b.WriteString("SHA256_ARM64=" + shq(v.SHA256ARM64) + "\n")
	b.WriteString(installBody)
	return b.String()
}

func oneLine(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) }

const installBody = `
fail() { echo "hosbah: $*" >&2; exit 1; }
say() { echo "==> $*"; }

[ "$(id -u)" = 0 ] || fail "run this as root (pipe it to 'sudo sh')"
[ "$(uname -s)" = Linux ] || fail "the Hosbah agent runs on Linux only"
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64; SUM=$SHA256_AMD64 ;;
  aarch64|arm64) ARCH=arm64; SUM=$SHA256_ARM64 ;;
  *) fail "unsupported architecture $(uname -m) (amd64 or arm64)" ;;
esac
[ -n "$SUM" ] || fail "this control plane has no linux/$ARCH build of the agent"
command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is required (coreutils)"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM
say "downloading hosbah-agent $VERSION (linux/$ARCH)"
curl -fsSL "$HOSBAH_URL/agent/download/hosbah-agent-linux-$ARCH" -o "$TMP/hosbah-agent"
echo "$SUM  $TMP/hosbah-agent" | sha256sum -c - >/dev/null 2>&1 || fail "checksum mismatch — refusing to install"
say "checksum verified ($SUM)"
install -m 0755 "$TMP/hosbah-agent" /usr/local/bin/hosbah-agent
# A server that ran the agent before the rename (cloudy-agent): the old path leads to the new binary.
if [ -e /usr/local/bin/cloudy-agent ] || [ -L /usr/local/bin/cloudy-agent ]; then
  ln -sfn hosbah-agent /usr/local/bin/cloudy-agent
fi

say "enrolling (running the enrolment checks)"
/usr/local/bin/hosbah-agent enroll --url "$HOSBAH_URL" --token "$TOKEN"

if [ -d /run/systemd/system ]; then
  cat > /etc/systemd/system/hosbah-agent.service <<'UNIT'
` + agentproto.AgentUnitFile + `UNIT
  systemctl daemon-reload
  # The pre-rename unit, if this server had it, never starts again (the new unit conflicts with it).
  systemctl disable cloudy-agent >/dev/null 2>&1 || true
  systemctl enable hosbah-agent >/dev/null 2>&1
  systemctl restart hosbah-agent
  say "hosbah-agent is running under systemd (journalctl -u hosbah-agent)"
else
  say "no systemd here: starting the agent detached — it will NOT come back after a reboot"
  pkill -f '/usr/local/bin/cloudy-agent run' 2>/dev/null || true
  pkill -f '/usr/local/bin/hosbah-agent run' 2>/dev/null || true
  nohup /usr/local/bin/hosbah-agent run >>/var/log/hosbah-agent.log 2>&1 &
  say "log: /var/log/hosbah-agent.log"
fi
say "done — the server shows as online in Hosbah within a few seconds"
`
