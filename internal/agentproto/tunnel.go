package agentproto

import (
	"errors"
	"net"
	"regexp"
	"strings"
)

// Tunnels: a way in for servers without a public address (a customer's box behind NAT, a laptop,
// a Docker Desktop target). frp exposes the server's Caddy (:80) on a port of the team's frps, and
// the edge in front of that (NPM + DNS) routes domains to it. tailscale joins the team's tailnet.
const (
	KindTunnelFrp       = "tunnel.frp"
	KindTunnelTailscale = "tunnel.tailscale"
)

// FrpcVersion is the pinned frpc; it should match the frps it talks to.
const FrpcVersion = "0.65.0"

// FrpParams: tunnel.frp. Secret is an env box (context DBSecretContext(TunnelID, "frp")) carrying
// {"token": …}; the frps auth token never appears in params or on an argv.
type FrpParams struct {
	TunnelID   string       `json:"tunnel_id"`
	ServerAddr string       `json:"server_addr"`
	ServerPort int          `json:"server_port"`
	RemotePort int          `json:"remote_port"`
	LocalPort  int          `json:"local_port"`
	Tool       *ToolInstall `json:"tool,omitempty"`
	Secret     string       `json:"secret,omitempty"`
	Remove     bool         `json:"remove,omitempty"`
}

func (p FrpParams) Validate() error {
	switch {
	case !ValidID(p.TunnelID):
		return errors.New("bad tunnel id")
	case p.Remove:
		return nil
	case !validHost(p.ServerAddr):
		return errors.New("the frp server address must be an IP address or a hostname")
	case p.ServerPort < 1 || p.ServerPort > 65535 || p.RemotePort < 1 || p.RemotePort > 65535 || p.LocalPort < 1 || p.LocalPort > 65535:
		return errors.New("ports must be 1–65535")
	case p.Tool == nil || p.Tool.SHA256 == "" || p.Tool.Version != FrpcVersion:
		return errors.New("tunnel.frp needs the pinned frpc download")
	case p.Secret == "":
		return errors.New("tunnel.frp needs the sealed frps token")
	}
	return nil
}

var tsHostRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// TailscaleParams: tunnel.tailscale. Secret is an env box (context DBSecretContext(TunnelID,
// "tailscale")) carrying {"authkey": …}.
type TailscaleParams struct {
	TunnelID string `json:"tunnel_id"`
	Hostname string `json:"hostname"`
	Secret   string `json:"secret,omitempty"`
	Remove   bool   `json:"remove,omitempty"`
}

func (p TailscaleParams) Validate() error {
	switch {
	case !ValidID(p.TunnelID):
		return errors.New("bad tunnel id")
	case p.Remove:
		return nil
	case !tsHostRE.MatchString(p.Hostname):
		return errors.New("bad tailnet hostname")
	case p.Secret == "":
		return errors.New("tunnel.tailscale needs the sealed auth key")
	}
	return nil
}

// TunnelResult is what both report: the address the server is reachable at through the tunnel.
type TunnelResult struct {
	Address string `json:"address,omitempty"`
}

func validHost(h string) bool {
	h = strings.TrimSpace(h)
	return net.ParseIP(h) != nil || ValidHostname(h)
}

// TailnetHostname turns a server name into a valid tailnet hostname.
func TailnetHostname(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 63 {
		s = s[:63]
	}
	if s == "" {
		s = "hosbah-server"
	}
	return s
}
