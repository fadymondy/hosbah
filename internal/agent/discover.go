package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
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

// guests lists a Proxmox node's VMs and LXCs with the details that make each one a site: vmid,
// type, cores, memory, disk, status, and its configured address.
func guests(ctx context.Context) []agentproto.Service {
	b, err := run(ctx, "pvesh", "get", "/cluster/resources", "--type", "vm", "--output-format", "json")
	if err != nil {
		return nil
	}
	var rs []struct {
		VMID    int     `json:"vmid"`
		Name    string  `json:"name"`
		Type    string  `json:"type"`
		Status  string  `json:"status"`
		Node    string  `json:"node"`
		MaxCPU  float64 `json:"maxcpu"`
		MaxMem  uint64  `json:"maxmem"`
		MaxDisk uint64  `json:"maxdisk"`
	}
	if json.Unmarshal(b, &rs) != nil {
		return nil
	}
	host, _ := os.Hostname()
	var out []agentproto.Service
	for _, r := range rs {
		if r.Node != "" && host != "" && r.Node != host {
			continue // a cluster: only this node's guests belong to this server
		}
		id := strconv.Itoa(r.VMID)
		s := agentproto.Service{Kind: "guest", Name: r.Name, Status: r.Status, ID: id, Type: r.Type,
			CPUs: int(r.MaxCPU), MemBytes: r.MaxMem, DiskBytes: r.MaxDisk, Image: r.Type + " " + id}
		s.Address = guestAddress(ctx, r.Type, id)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { a, _ := strconv.Atoi(out[i].ID); b, _ := strconv.Atoi(out[j].ID); return a < b })
	return out
}

// guestAddress reads a guest's configured IPv4 (ip= in an LXC's net0, a VM's cloud-init ipconfig0).
func guestAddress(ctx context.Context, typ, id string) string {
	tool, key := "pct", "net0"
	if typ == "qemu" {
		tool, key = "qm", "ipconfig0"
	}
	b, err := run(ctx, tool, "config", id)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(l, key+":") {
			continue
		}
		for _, kv := range strings.Split(strings.TrimSpace(strings.TrimPrefix(l, key+":")), ",") {
			if v, ok := strings.CutPrefix(kv, "ip="); ok && v != "dhcp" {
				ip, _, _ := strings.Cut(v, "/")
				return ip
			}
		}
	}
	return ""
}
