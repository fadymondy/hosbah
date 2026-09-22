package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// Tunnels (tunnel.frp / tunnel.tailscale): a way in for a server with no public address. Both are
// idempotent — re-applying the same tunnel rewrites the same config and restarts the same unit.

const (
	frpcBin      = "/usr/local/bin/frpc"
	frpcConfig   = agentproto.StateDir + "/frpc.toml"
	frpcUnit     = "hosbah-frpc"
	frpcUnitPath = "/etc/systemd/system/hosbah-frpc.service"

	// The unit of an agent from before the rename. Applying or removing a tunnel retires it: the
	// tunnel restarts then anyway. Its config was /etc/cloudy/frpc.toml, which is frpcConfig once
	// the state directory moved.
	legacyFrpcUnit     = "cloudy-frpc"
	legacyFrpcUnitPath = "/etc/systemd/system/cloudy-frpc.service"
)

// retireLegacyFrpc stops and removes the pre-rename frpc unit, if this server has it.
func retireLegacyFrpc(oc *OpContext) {
	if _, err := os.Stat(legacyFrpcUnitPath); err != nil {
		return
	}
	oc.Info("replacing the tunnel unit %s with %s", legacyFrpcUnit, frpcUnit)
	_, _ = oc.Run("systemctl", "disable", "--now", legacyFrpcUnit)
	_ = os.Remove(legacyFrpcUnitPath)
}

const frpcUnitFile = `[Unit]
Description=Hosbah tunnel (frpc)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/frpc -c /etc/hosbah/frpc.toml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`

func tunnelFrp(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.FrpParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if !systemdRunning() {
		return nil, 2, errors.New("Hosbah runs frpc under systemd; this server has no systemd")
	}
	if p.Remove {
		retireLegacyFrpc(oc)
		_, _ = oc.Run("systemctl", "disable", "--now", frpcUnit)
		_ = os.Remove(frpcUnitPath)
		_ = os.Remove(frpcConfig)
		_, _ = oc.Run("systemctl", "daemon-reload")
		oc.Info("tunnel removed")
		return agentproto.TunnelResult{}, 0, nil
	}
	sec, err := oc.openSecret(p.TunnelID, "frp", p.Secret)
	if err != nil {
		return nil, 1, err
	}
	oc.Progress(1, 3)
	if !strings.Contains(toolVersion(oc.Ctx, frpcBin, "--version"), agentproto.FrpcVersion) {
		oc.Info("installing frpc %s", agentproto.FrpcVersion)
		if err := installFromTarball(oc, *p.Tool, "frpc", frpcBin); err != nil {
			return nil, 1, err
		}
	}
	oc.Progress(2, 3)
	// The token is a TOML basic string; strconv.Quote yields a valid one for printable input.
	conf := fmt.Sprintf(`# Written by Hosbah (tunnel %s). Changes are overwritten.
serverAddr = %s
serverPort = %d
auth.method = "token"
auth.token = %s
loginFailExit = false

[[proxies]]
name = "hosbah-%s"
type = "tcp"
localIP = "127.0.0.1"
localPort = %d
remotePort = %d
`, p.TunnelID, strconv.Quote(p.ServerAddr), p.ServerPort, strconv.Quote(sec["token"]), p.TunnelID[:8], p.LocalPort, p.RemotePort)
	if err := os.MkdirAll(agentproto.StateDir, 0o700); err != nil {
		return nil, 1, err
	}
	if err := os.WriteFile(frpcConfig, []byte(conf), 0o600); err != nil {
		return nil, 1, err
	}
	if out, err := execOutput(oc, frpcBin, "verify", "-c", frpcConfig); err != nil {
		oc.Info("%s", strings.TrimSpace(out))
		return nil, 1, errors.New("frpc rejected its configuration")
	}
	retireLegacyFrpc(oc)
	if err := os.WriteFile(frpcUnitPath, []byte(frpcUnitFile), 0o644); err != nil { //#nosec G306 -- a unit file
		return nil, 1, err
	}
	oc.Progress(3, 3)
	for _, args := range [][]string{{"daemon-reload"}, {"enable", frpcUnit}, {"restart", frpcUnit}} {
		if code, err := oc.Run("systemctl", args...); err != nil {
			return nil, code, err
		}
	}
	// frpc logs "start proxy success" once frps accepted the proxy; a refused login or a taken
	// port shows up here instead of as a silent tunnel that never carries traffic.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := execOutput(oc, "journalctl", "-u", frpcUnit, "--since", "-40s", "--no-pager", "-o", "cat")
		switch {
		case strings.Contains(out, "start proxy success"):
			addr := fmt.Sprintf("%s:%d", p.ServerAddr, p.RemotePort)
			oc.Info("tunnel up: %s → 127.0.0.1:%d", addr, p.LocalPort)
			return agentproto.TunnelResult{Address: addr}, 0, nil
		case strings.Contains(out, "port already used") || strings.Contains(out, "port not allowed"):
			return nil, 1, fmt.Errorf("frps refused remote port %d (taken or not allowed)", p.RemotePort)
		case strings.Contains(out, "token in login doesn't match") || strings.Contains(out, "authorization failed"):
			return nil, 1, errors.New("frps refused the auth token")
		}
		time.Sleep(time.Second)
	}
	return nil, 1, errors.New("frpc did not confirm the tunnel within 30s (is the frp server reachable?)")
}

func tunnelTailscale(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.TailscaleParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if p.Remove {
		if _, err := exec.LookPath("tailscale"); err == nil {
			_, _ = oc.Run("tailscale", "logout")
		}
		oc.Info("left the tailnet")
		return agentproto.TunnelResult{}, 0, nil
	}
	sec, err := oc.openSecret(p.TunnelID, "tailscale", p.Secret)
	if err != nil {
		return nil, 1, err
	}
	if _, err := exec.LookPath("tailscale"); err != nil {
		oc.Info("installing Tailscale from its package repository")
		if code, err := oc.Run("sh", "-c", "curl -fsSL https://tailscale.com/install.sh | sh"); err != nil {
			return nil, code, errors.New("could not install Tailscale")
		}
	}
	if systemdRunning() {
		_, _ = oc.Run("systemctl", "enable", "--now", "tailscaled")
	}
	// The auth key goes through a 0600 file (tailscale up --auth-key=file:…), never an argv.
	f, err := os.CreateTemp("", "hosbah-ts-*")
	if err != nil {
		return nil, 1, err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(sec["authkey"]); err != nil {
		f.Close()
		return nil, 1, err
	}
	f.Close()
	if code, err := oc.Run("tailscale", "up", "--auth-key=file:"+f.Name(), "--hostname="+p.Hostname); err != nil {
		return nil, code, errors.New("tailscale up failed (is the auth key valid and unused?)")
	}
	ip, _ := execOutput(oc, "tailscale", "ip", "-4")
	ip = strings.TrimSpace(firstLine(ip))
	oc.Info("joined the tailnet as %s (%s)", p.Hostname, ip)
	return agentproto.TunnelResult{Address: ip}, 0, nil
}
