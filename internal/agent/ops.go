package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/fadymondy/cloudy/internal/agentproto"
)

// ---- system.info -------------------------------------------------------------------------------

func systemInfo(oc *OpContext) (any, int, error) {
	f := CollectFacts(oc.Ctx)
	osLabel := f.OSPretty
	if osLabel == "" {
		osLabel = strings.TrimSpace(f.OSID + " " + f.OSVersion)
	}
	oc.Info("hostname        %s", f.Hostname)
	oc.Info("os              %s", osLabel)
	oc.Info("kernel          %s", f.Kernel)
	oc.Info("arch            %s, %d CPU(s)", f.Arch, f.CPUs)
	oc.Info("memory          %s", humanBytes(f.MemTotalBytes))
	oc.Info("systemd         %v", f.Systemd)
	if f.Virt != "" {
		oc.Info("virtualization  %s", f.Virt)
	}
	oc.Info("docker          %s", dockerSummary(f.Docker))
	m := NewSampler().Sample()
	oc.Info("cpu %.1f%% · memory %s of %s · disk %s of %s · load %.2f · up %s",
		m.CPUPercent, humanBytes(m.MemUsedBytes), humanBytes(m.MemTotalBytes),
		humanBytes(m.DiskUsedBytes), humanBytes(m.DiskTotalBytes), m.Load1,
		(time.Duration(m.UptimeSeconds) * time.Second).String())
	for _, c := range agentproto.EvaluateChecks(f) {
		oc.Info("check %-8s %-4s %s", c.Name, c.Status, c.Detail)
	}
	return map[string]any{"facts": f, "metrics": m, "agent_version": oc.Version}, 0, nil
}

func dockerSummary(d agentproto.DockerFacts) string {
	switch {
	case !d.Installed:
		return "not installed"
	case !d.Running:
		return "installed, engine not answering (" + d.Error + ")"
	default:
		return "Docker " + d.Version + ", storage driver " + d.StorageDriver
	}
}

// ---- docker.check ------------------------------------------------------------------------------

func dockerCheck(oc *OpContext) (any, int, error) {
	d, info := dockerInspect(oc.Ctx)
	res := map[string]any{"docker": d}
	switch {
	case !d.Installed:
		oc.Info("Docker is not installed on this server.")
		oc.Info("The provisioning step %s installs it.", agentproto.StepDockerInstall)
	case !d.Running:
		oc.Info("Docker is installed but the engine is not answering: %s", d.Error)
	default:
		oc.Info("Docker %s is running", d.Version)
		fs := d.BackingFS
		if fs == "" {
			fs = "unknown"
		}
		oc.Info("storage driver  %s (backing filesystem %s)", d.StorageDriver, fs)
		oc.Info("containers      %d (%d running)", info.Containers, info.ContainersRunning)
		oc.Info("images          %d", info.Images)
		res["containers"], res["containers_running"], res["images"] = info.Containers, info.ContainersRunning, info.Images
	}
	for _, c := range agentproto.EvaluateChecks(agentproto.Facts{Docker: d}) {
		if c.Name == "docker" {
			oc.Info("verdict         %s — %s", c.Status, c.Detail)
			res["check"] = c
		}
	}
	return res, 0, nil
}

// ---- provision.step ----------------------------------------------------------------------------

type provisionParams struct {
	Step   string `json:"step"`
	SwapMB int    `json:"swap_mb,omitempty"`
}

// provisionStep runs one idempotent step: each first checks whether the host is already in the
// wanted state and says so instead of re-applying. Commands are fixed argv lists — no shell, and
// nothing from params reaches a command line except the validated swap size.
func provisionStep(oc *OpContext) (any, int, error) {
	var p provisionParams
	if err := json.Unmarshal(oc.Params, &p); err != nil {
		return nil, 2, fmt.Errorf("bad params: %w", err)
	}
	if runtime.GOOS != "linux" {
		return nil, 2, errors.New("provisioning steps run on Linux only")
	}
	switch p.Step {
	case agentproto.StepBasePackages:
		return stepBasePackages(oc)
	case agentproto.StepDockerInstall:
		return stepDockerInstall(oc)
	case agentproto.StepSwap:
		return stepSwap(oc, p.SwapMB)
	}
	return nil, 2, fmt.Errorf("unknown provisioning step %q", p.Step)
}

var basePackages = []string{"ca-certificates", "curl", "gnupg"}

func missingPackages(oc *OpContext, pkgs []string) []string {
	var missing []string
	for _, p := range pkgs {
		out, err := execOutput(oc, "dpkg-query", "-W", "-f=${Status}", p)
		if err != nil || !strings.Contains(out, "install ok installed") {
			missing = append(missing, p)
		}
	}
	return missing
}

func stepBasePackages(oc *OpContext) (any, int, error) {
	missing := missingPackages(oc, basePackages)
	if len(missing) == 0 {
		oc.Info("already installed: %s — nothing to do", strings.Join(basePackages, ", "))
		return map[string]any{"changed": false}, 0, nil
	}
	oc.Info("missing: %s", strings.Join(missing, ", "))
	oc.Progress(1, 2)
	if code, err := oc.Run("apt-get", "update"); err != nil {
		return nil, code, err
	}
	oc.Progress(2, 2)
	if code, err := oc.Run("apt-get", append([]string{"install", "-y", "--no-install-recommends"}, missing...)...); err != nil {
		return nil, code, err
	}
	return map[string]any{"changed": true, "installed": missing}, 0, nil
}

func stepDockerInstall(oc *OpContext) (any, int, error) {
	d := DockerInfo(oc.Ctx)
	if d.Running {
		oc.Info("Docker %s is already installed and running — nothing to do", d.Version)
		return map[string]any{"changed": false, "version": d.Version}, 0, nil
	}
	if !d.Installed {
		oc.Progress(1, 3)
		if code, err := oc.Run("apt-get", "update"); err != nil {
			return nil, code, err
		}
		oc.Progress(2, 3)
		// The distribution's docker.io package: no third-party apt repository or key to trust.
		if code, err := oc.Run("apt-get", "install", "-y", "--no-install-recommends", "docker.io"); err != nil {
			return nil, code, err
		}
	} else {
		oc.Info("Docker is installed but the engine is not answering; starting it")
	}
	oc.Progress(3, 3)
	if systemdRunning() {
		if code, err := oc.Run("systemctl", "enable", "--now", "docker"); err != nil {
			return nil, code, err
		}
	} else if code, err := oc.Run("service", "docker", "start"); err != nil {
		return nil, code, err
	}
	for i := 0; i < 30 && !d.Running; i++ {
		select {
		case <-oc.Ctx.Done():
			return nil, 124, oc.Ctx.Err()
		case <-time.After(time.Second):
		}
		d = DockerInfo(oc.Ctx)
	}
	if !d.Running {
		return nil, 1, fmt.Errorf("docker did not come up: %s", d.Error)
	}
	oc.Info("Docker %s is running (storage driver %s)", d.Version, d.StorageDriver)
	return map[string]any{"changed": true, "version": d.Version, "storage_driver": d.StorageDriver}, 0, nil
}

func stepSwap(oc *OpContext, mb int) (any, int, error) {
	if mb == 0 {
		mb = 1024
	}
	if mb < 256 || mb > 16384 {
		return nil, 2, errors.New("swap_mb must be between 256 and 16384")
	}
	if v := containerVirt(oc.Ctx); v != "" {
		oc.Info("this server is a %s container: swap belongs to the host — nothing to do", v)
		return map[string]any{"changed": false, "skipped": "container"}, 0, nil
	}
	if active := activeSwap(); active != "" {
		oc.Info("swap is already active (%s) — nothing to do", active)
		return map[string]any{"changed": false, "device": active}, 0, nil
	}
	const path = "/swapfile"
	if _, err := os.Stat(path); err != nil {
		if _, err := oc.Run("fallocate", "-l", fmt.Sprintf("%dM", mb), path); err != nil {
			oc.Info("fallocate failed; writing the file with dd instead")
			if code, err := oc.Run("dd", "if=/dev/zero", "of="+path, "bs=1M", fmt.Sprintf("count=%d", mb), "status=none"); err != nil {
				return nil, code, err
			}
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, 1, err
		}
		if code, err := oc.Run("mkswap", path); err != nil {
			return nil, code, err
		}
	}
	if code, err := oc.Run("swapon", path); err != nil {
		return nil, code, err
	}
	if err := ensureLine("/etc/fstab", path+" none swap sw 0 0"); err != nil {
		return nil, 1, fmt.Errorf("swap is on, but /etc/fstab was not updated: %w", err)
	}
	return map[string]any{"changed": true, "swap_mb": mb}, 0, nil
}

func ensureLine(path, line string) error {
	b, err := os.ReadFile(path) //#nosec G304 -- fixed path
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == line {
			return nil
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //#nosec G302 -- fstab is world-readable
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + line + "\n")
	return err
}

// ---- agent.update ------------------------------------------------------------------------------

type updateParams struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

// executablePath is overridable in tests (the real one is the running binary).
var executablePath = func() string { return selfPath }

// agentUpdate replaces the agent binary. The version, URL and SHA-256 are inside the signed
// params, so the control plane's signature covers the exact bytes installed: a download that does
// not hash to the signed value is discarded and nothing changes.
func (e *Executor) agentUpdate(oc *OpContext) (any, int, error) {
	var p updateParams
	if err := json.Unmarshal(oc.Params, &p); err != nil {
		return nil, 2, fmt.Errorf("bad params: %w", err)
	}
	p.SHA256 = strings.ToLower(strings.TrimSpace(p.SHA256))
	if _, err := hex.DecodeString(p.SHA256); err != nil || len(p.SHA256) != 64 || strings.TrimSpace(p.Version) == "" {
		return nil, 2, errors.New("params need a version, a url and a sha256")
	}
	u, err := url.Parse(p.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, 2, errors.New("the update url must be http(s)")
	}
	cp := strings.TrimRight(oc.ControlPlane, "/") + "/"
	if u.Scheme == "http" && !strings.HasPrefix(p.URL, cp) {
		return nil, 2, errors.New("plain-http downloads are accepted only from the control plane this agent enrolled with")
	}
	if p.Version == oc.Version {
		oc.Info("already running %s — nothing to do", p.Version)
		return map[string]any{"changed": false, "version": p.Version}, 0, nil
	}
	self := executablePath()
	if self == "" {
		return nil, 1, errors.New("cannot locate the agent binary")
	}

	oc.Info("downloading %s", p.URL)
	req, err := http.NewRequestWithContext(oc.Ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return nil, 1, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 1, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 1, fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	tmp := self + ".download"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755) //#nosec G302 G304 -- an executable next to ourselves
	if err != nil {
		return nil, 1, err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 256<<20))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmp)
		return nil, 1, fmt.Errorf("download: %v", errors.Join(copyErr, closeErr))
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != p.SHA256 {
		_ = os.Remove(tmp)
		return nil, 1, fmt.Errorf("checksum mismatch: the download hashes to %s, the signed operation says %s — nothing was installed", got, p.SHA256)
	}
	oc.Info("verified %d bytes, sha256 %s", n, got)
	if err := os.Rename(tmp, self); err != nil {
		_ = os.Remove(tmp)
		return nil, 1, fmt.Errorf("replace %s: %w", self, err)
	}
	oc.Info("installed %s at %s; restarting", p.Version, self)
	oc.RequestRestart()
	return map[string]any{"changed": true, "from": oc.Version, "version": p.Version}, 0, nil
}

// execOutput runs a fixed argv and returns stdout, without streaming it.
func execOutput(oc *OpContext, name string, args ...string) (string, error) {
	out, err := commandContext(oc, name, args...).Output()
	return string(out), err
}
