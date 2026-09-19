package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// CollectFacts gathers what enrolment and system.info report about this host.
func CollectFacts(ctx context.Context) agentproto.Facts {
	f := agentproto.Facts{Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), Ports: map[string]bool{}}
	f.Hostname, _ = os.Hostname()
	f.PrimaryIP = primaryIP()
	collectPlatform(ctx, &f)
	f.Docker = DockerInfo(ctx)
	f.Proxmox = ProxmoxInfo(ctx)
	f.Services = DiscoverServices(ctx, f.Docker.Running, f.Proxmox.Installed)
	for _, p := range agentproto.PortsToCheck {
		f.Ports[p] = portFree(p)
	}
	return f
}

// portFree reports whether nothing is listening on the port. Only "address in use" counts as
// taken; any other bind error (no privilege, say) says nothing about the port, so it counts free.
func portFree(port string) bool {
	l, err := net.Listen("tcp", ":"+port)
	if err == nil {
		_ = l.Close()
		return true
	}
	msg := strings.ToLower(err.Error())
	return !strings.Contains(msg, "address already in use") && !strings.Contains(msg, "only one usage")
}

// dockerInfo is the part of `docker info --format '{{json .}}'` Hosbah reads.
type dockerInfo struct {
	ServerVersion     string     `json:"ServerVersion"`
	Driver            string     `json:"Driver"`
	DriverStatus      [][]string `json:"DriverStatus"`
	Containers        int        `json:"Containers"`
	ContainersRunning int        `json:"ContainersRunning"`
	Images            int        `json:"Images"`
	ServerErrors      []string   `json:"ServerErrors"`
}

// DockerInfo reports whether Docker is installed and answering, and its storage driver.
func DockerInfo(ctx context.Context) agentproto.DockerFacts {
	d, _ := dockerInspect(ctx)
	return d
}

func dockerInspect(ctx context.Context) (agentproto.DockerFacts, *dockerInfo) {
	var d agentproto.DockerFacts
	if _, err := exec.LookPath("docker"); err != nil {
		return d, nil
	}
	d.Installed = true
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(cctx, "docker", "info", "--format", "{{json .}}")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var info dockerInfo
	_ = json.Unmarshal(bytes.TrimSpace(out), &info)
	if info.ServerVersion == "" {
		switch {
		case len(info.ServerErrors) > 0:
			d.Error = firstLine(info.ServerErrors[0])
		case strings.TrimSpace(stderr.String()) != "":
			d.Error = firstLine(stderr.String())
		case err != nil:
			d.Error = err.Error()
		default:
			d.Error = "the engine did not report a version"
		}
		return d, nil
	}
	d.Running = true
	d.Version = info.ServerVersion
	d.StorageDriver = info.Driver
	for _, kv := range info.DriverStatus {
		if len(kv) == 2 && kv[0] == "Backing Filesystem" {
			d.BackingFS = kv[1]
		}
	}
	return d, &info
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// selfPath is the running binary, resolved once at start — before an update replaces the file.
var selfPath = func() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}()

// primaryIP is the address the host uses for outbound traffic: the local end of a UDP "connection"
// to a public address. No packet is sent (UDP connect only picks a route).
func primaryIP() string {
	c, err := net.Dial("udp", "1.1.1.1:53")
	if err != nil {
		return ""
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok && !a.IP.IsLoopback() {
		return a.IP.String()
	}
	return ""
}
