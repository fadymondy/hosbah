package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"github.com/fadymondy/cloudy/internal/agentproto"
)

// Discovery: what an existing server already runs, so connecting it imports its workloads instead
// of presenting an empty box. Read-only; nothing here changes the host.

// knownUnits are the systemd services worth showing (web servers, databases, caches, panels).
var knownUnits = []string{
	"nginx", "apache2", "httpd", "caddy", "traefik", "haproxy",
	"mysql", "mariadb", "postgresql", "redis-server", "redis", "mongod", "memcached",
	"php8.1-fpm", "php8.2-fpm", "php8.3-fpm", "php8.4-fpm", "php-fpm",
	"docker", "containerd", "pveproxy", "pvedaemon",
}

// ProxmoxInfo reports whether this host is a Proxmox VE node.
func ProxmoxInfo(ctx context.Context) agentproto.ProxmoxFacts {
	var p agentproto.ProxmoxFacts
	if _, err := exec.LookPath("pveversion"); err != nil {
		return p
	}
	p.Installed = true
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(cctx, "pveversion").Output(); err == nil {
		// "pve-manager/8.2.4/faa83925c9641325 (running kernel: …)"
		v := strings.TrimSpace(string(out))
		if _, rest, ok := strings.Cut(v, "/"); ok {
			v, _, _ = strings.Cut(rest, "/")
		}
		p.Version = v
	}
	return p
}

// DiscoverServices lists containers, well-known systemd units and (on Proxmox) guests.
func DiscoverServices(ctx context.Context, docker bool, proxmox bool) []agentproto.Service {
	var out []agentproto.Service
	if docker {
		out = append(out, containers(ctx)...)
	}
	out = append(out, units(ctx)...)
	if proxmox {
		out = append(out, guests(ctx)...)
	}
	return out
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return exec.CommandContext(cctx, name, args...).Output()
}

func containers(ctx context.Context) []agentproto.Service {
	b, err := run(ctx, "docker", "ps", "-a", "--format", "{{json .}}")
	if err != nil {
		return nil
	}
	var out []agentproto.Service
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		var c struct{ Names, Image, State, Status, Ports string }
		if json.Unmarshal([]byte(sc.Text()), &c) != nil {
			continue
		}
		s := agentproto.Service{Kind: "container", Name: c.Names, Image: c.Image, Status: c.State}
		if s.Status == "" {
			s.Status = c.Status
		}
		for _, p := range strings.Split(c.Ports, ",") {
			if p = strings.TrimSpace(p); p != "" {
				s.Ports = append(s.Ports, p)
			}
		}
		out = append(out, s)
	}
	return out
}

func units(ctx context.Context) []agentproto.Service {
	if !systemdRunning() {
		return nil
	}
	var out []agentproto.Service
	for _, u := range knownUnits {
		b, _ := run(ctx, "systemctl", "is-active", u+".service")
		state := strings.TrimSpace(string(b))
		if state == "" || state == "inactive" && !unitExists(ctx, u) {
			continue
		}
		out = append(out, agentproto.Service{Kind: "systemd", Name: u, Status: state})
	}
	return out
}

func unitExists(ctx context.Context, u string) bool {
	b, _ := run(ctx, "systemctl", "list-unit-files", u+".service", "--no-legend")
	return strings.TrimSpace(string(b)) != ""
}

// guests lists a Proxmox node's VMs and LXCs.
func guests(ctx context.Context) []agentproto.Service {
	var out []agentproto.Service
	for _, g := range []struct{ cmd, kind string }{{"qm", "vm"}, {"pct", "lxc"}} {
		b, err := run(ctx, g.cmd, "list")
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		for _, l := range lines[min(1, len(lines)):] { // skip the header
			f := strings.Fields(l)
			if len(f) < 2 {
				continue
			}
			// qm list: VMID NAME STATUS …   pct list: VMID Status [Lock] Name
			s := agentproto.Service{Kind: "guest", Image: g.kind + " " + f[0]}
			if g.kind == "vm" && len(f) >= 3 {
				s.Name, s.Status = f[1], f[2]
			} else {
				s.Name, s.Status = f[len(f)-1], f[1]
			}
			out = append(out, s)
		}
	}
	return out
}
