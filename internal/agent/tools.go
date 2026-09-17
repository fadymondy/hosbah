package agent

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fadymondy/cloudy/internal/agentproto"
)

// P2 provisioning steps: what a server needs to build and serve apps. Each checks first and says
// "nothing to do" when the host is already there.

// ---- build.tools ---------------------------------------------------------------------------------

// git clones; docker-buildx is the client for the BuildKit builder inside dockerd (Railpack's
// frontend needs BuildKit). Distribution packages, like docker.io in P1: no third-party repository.
var buildPackages = []string{"git", "docker-buildx"}

func stepBuildTools(oc *OpContext) (any, int, error) {
	missing := missingPackages(oc, buildPackages)
	if len(missing) == 0 {
		oc.Info("already installed: %s — nothing to do", strings.Join(buildPackages, ", "))
		return map[string]any{"changed": false}, 0, nil
	}
	oc.Info("missing: %s", strings.Join(missing, ", "))
	oc.Progress(1, 3)
	if code, err := oc.Run("apt-get", "update"); err != nil {
		return nil, code, err
	}
	oc.Progress(2, 3)
	if code, err := oc.Run("apt-get", append([]string{"install", "-y", "--no-install-recommends"}, missing...)...); err != nil {
		return nil, code, err
	}
	oc.Progress(3, 3)
	if code, err := oc.Run("docker", "buildx", "version"); err != nil {
		return nil, code, fmt.Errorf("docker buildx is not usable after installing it: %w", err)
	}
	return map[string]any{"changed": true, "installed": missing}, 0, nil
}

// ---- caddy.install -------------------------------------------------------------------------------

const (
	caddyBin      = "/usr/local/bin/caddy"
	caddyUnit     = "cloudy-caddy"
	caddyUnitPath = "/etc/systemd/system/cloudy-caddy.service"
	caddyConfig   = "/etc/caddy/cloudy.json"
	caddyAdmin    = "http://localhost:2019"
)

// The unit runs Caddy as its own system user with only the capability to bind 80/443. Its config
// is the file the agent rewrites on every route change (so a restart comes back with the current
// routes); live changes go through the admin API on localhost:2019.
const caddyUnitFile = `[Unit]
Description=Caddy (managed by Cloudy)
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
User=caddy
Group=caddy
Environment=XDG_DATA_HOME=/var/lib XDG_CONFIG_HOME=/var/lib/caddy/config
ExecStart=/usr/local/bin/caddy run --environ --config /etc/caddy/cloudy.json
TimeoutStopSec=5s
Restart=on-failure
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`

func stepCaddy(oc *OpContext, p agentproto.ProvisionParams) (any, int, error) {
	if p.Tool == nil || p.Tool.Version == "" || p.Tool.SHA512 == "" {
		return nil, 2, errors.New("caddy.install needs a pinned version, url and sha512")
	}
	if !systemdRunning() {
		return nil, 2, errors.New("Cloudy runs Caddy under systemd; this server has no systemd")
	}
	if p.AskURL != "" {
		if err := setAskURL(p.AskURL); err != nil {
			return nil, 1, err
		}
	}
	have := toolVersion(oc.Ctx, caddyBin, "version")
	unitOK := fileHas(caddyUnitPath, caddyUnitFile)
	if strings.HasPrefix(have, "v"+p.Tool.Version) && unitOK && serviceActive(oc.Ctx, caddyUnit) && caddyUp(oc.Ctx) {
		oc.Info("Caddy %s is installed and its admin API answers on localhost:2019 — nothing to do", p.Tool.Version)
		// The config file may predate a control-plane change (the ask URL): re-render it.
		if err := applyRoutes(oc, nil); err != nil {
			return nil, 1, err
		}
		return map[string]any{"changed": false, "version": p.Tool.Version}, 0, nil
	}
	oc.Progress(1, 5)
	if !strings.HasPrefix(have, "v"+p.Tool.Version) {
		for _, port := range []string{"80", "443", "2019"} {
			if !portFree(port) && !serviceActive(oc.Ctx, caddyUnit) {
				return nil, 1, fmt.Errorf("port %s is in use by something other than Cloudy's Caddy — free it first", port)
			}
		}
		oc.Info("installing Caddy %s from %s", p.Tool.Version, p.Tool.URL)
		if err := installFromTarball(oc, *p.Tool, "caddy", caddyBin); err != nil {
			return nil, 1, err
		}
	}
	oc.Progress(2, 5)
	if _, err := exec.LookPath("useradd"); err == nil {
		if exec.CommandContext(oc.Ctx, "id", "caddy").Run() != nil { //#nosec G204 -- fixed argv
			if code, err := oc.Run("useradd", "--system", "--home-dir", "/var/lib/caddy", "--create-home",
				"--shell", "/usr/sbin/nologin", "caddy"); err != nil {
				return nil, code, err
			}
		}
	}
	for _, d := range []string{"/etc/caddy", "/var/lib/caddy", "/var/lib/caddy/config"} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, 1, err
		}
	}
	if code, err := oc.Run("chown", "-R", "caddy:caddy", "/var/lib/caddy"); err != nil {
		return nil, code, err
	}
	oc.Progress(3, 5)
	if err := writeConfigFile(); err != nil {
		return nil, 1, err
	}
	if err := os.WriteFile(caddyUnitPath, []byte(caddyUnitFile), 0o644); err != nil { //#nosec G306 -- a unit file
		return nil, 1, err
	}
	oc.Progress(4, 5)
	if code, err := oc.Run("systemctl", "daemon-reload"); err != nil {
		return nil, code, err
	}
	if code, err := oc.Run("systemctl", "enable", caddyUnit); err != nil {
		return nil, code, err
	}
	if code, err := oc.Run("systemctl", "restart", caddyUnit); err != nil {
		return nil, code, err
	}
	oc.Progress(5, 5)
	for i := 0; i < 30 && !caddyUp(oc.Ctx); i++ {
		select {
		case <-oc.Ctx.Done():
			return nil, 124, oc.Ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if !caddyUp(oc.Ctx) {
		_, _ = oc.Run("systemctl", "status", "--no-pager", "--lines", "20", caddyUnit)
		return nil, 1, errors.New("Caddy did not start: its admin API is not answering on localhost:2019")
	}
	oc.Info("Caddy %s is running; admin API on localhost:2019", toolVersion(oc.Ctx, caddyBin, "version"))
	return map[string]any{"changed": true, "version": p.Tool.Version}, 0, nil
}

// ---- railpack.install ----------------------------------------------------------------------------

const railpackBin = "/usr/local/bin/railpack"

func stepRailpack(oc *OpContext, p agentproto.ProvisionParams) (any, int, error) {
	if p.Tool == nil || p.Tool.Version == "" || p.Tool.SHA256 == "" {
		return nil, 2, errors.New("railpack.install needs a pinned version, url and sha256")
	}
	have := toolVersion(oc.Ctx, railpackBin, "--version")
	if strings.Contains(have, p.Tool.Version) {
		oc.Info("Railpack %s is installed — nothing to do", p.Tool.Version)
		return map[string]any{"changed": false, "version": p.Tool.Version}, 0, nil
	}
	oc.Info("installing Railpack %s from %s", p.Tool.Version, p.Tool.URL)
	if err := installFromTarball(oc, *p.Tool, "railpack", railpackBin); err != nil {
		return nil, 1, err
	}
	oc.Info("%s", toolVersion(oc.Ctx, railpackBin, "--version"))
	return map[string]any{"changed": true, "version": p.Tool.Version}, 0, nil
}

// ---- helpers -------------------------------------------------------------------------------------

// installFromTarball downloads a .tar.gz, checks it against the signed hash, and installs the one
// named binary from it (atomically: a temporary file, then a rename).
func installFromTarball(oc *OpContext, t agentproto.ToolInstall, member, dest string) error {
	u, err := url.Parse(t.URL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && strings.HasPrefix(t.URL, strings.TrimRight(oc.ControlPlane, "/")+"/"))) {
		return errors.New("tool downloads must be https (or http from the control plane this agent enrolled with)")
	}
	req, err := http.NewRequestWithContext(oc.Ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", "cloudy-tool-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	var h hash.Hash
	want := ""
	switch {
	case t.SHA512 != "":
		h, want = sha512.New(), strings.ToLower(t.SHA512)
	default:
		h, want = sha256.New(), strings.ToLower(t.SHA256)
	}
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, 512<<20))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("checksum mismatch: the download hashes to %s, the signed operation says %s — nothing was installed", got, want)
	}
	oc.Info("verified %d bytes against the signed checksum", n)

	f, err := os.Open(tmp.Name())
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("the archive has no %q", member)
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != member {
			continue
		}
		out := dest + ".download"
		w, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755) //#nosec G302 G304 -- a binary we install
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, io.LimitReader(tr, 256<<20)); err != nil {
			w.Close()
			os.Remove(out)
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		if err := os.Rename(out, dest); err != nil {
			return err
		}
		oc.Info("installed %s", dest)
		return nil
	}
}

func toolVersion(ctx context.Context, bin string, arg string) string {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin, arg).Output() //#nosec G204 -- fixed binaries
	if err != nil {
		return ""
	}
	return strings.TrimSpace(firstLine(string(out)))
}

func serviceActive(ctx context.Context, unit string) bool {
	return exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", unit).Run() == nil //#nosec G204 -- fixed argv
}

func fileHas(path, content string) bool {
	b, err := os.ReadFile(path) //#nosec G304 -- fixed paths
	return err == nil && string(b) == content
}
