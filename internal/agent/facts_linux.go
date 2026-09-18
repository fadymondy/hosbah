//go:build linux

package agent

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/fadymondy/cloudy/internal/agentproto"
)

func collectPlatform(ctx context.Context, f *agentproto.Facts) {
	osr := readOSRelease()
	f.OSID, f.OSVersion, f.OSPretty = osr["ID"], osr["VERSION_ID"], osr["PRETTY_NAME"]
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		f.Kernel = strings.TrimSpace(string(b))
	}
	f.MemTotalBytes = meminfo()["MemTotal"]
	f.Systemd = systemdRunning()
	f.Virt = virtualization(ctx)
}

func readOSRelease() map[string]string {
	out := map[string]string{}
	for _, path := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(file)
		for sc.Scan() {
			k, v, ok := strings.Cut(sc.Text(), "=")
			if !ok || strings.HasPrefix(k, "#") {
				continue
			}
			out[k] = strings.Trim(v, `"'`)
		}
		_ = file.Close()
		return out
	}
	return out
}

// meminfo returns /proc/meminfo in bytes.
func meminfo() map[string]uint64 {
	out := map[string]uint64{}
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return out
	}
	defer file.Close()
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		if len(fields) > 2 && fields[2] == "kB" {
			n *= 1024
		}
		out[strings.TrimSuffix(fields[0], ":")] = n
	}
	return out
}

// systemdRunning is sd_booted(): /run/systemd/system exists only when systemd is PID 1.
func systemdRunning() bool {
	fi, err := os.Stat("/run/systemd/system")
	return err == nil && fi.IsDir()
}

func virtualization(ctx context.Context) string {
	if v := containerVirt(ctx); v != "" {
		return v
	}
	if out, _ := exec.CommandContext(ctx, "systemd-detect-virt").Output(); len(out) > 0 {
		if v := strings.TrimSpace(string(out)); v != "none" {
			return v
		}
	}
	return ""
}

// containerVirt names the container technology this host runs in, or "" for a VM / bare metal.
func containerVirt(ctx context.Context) string {
	if out, _ := exec.CommandContext(ctx, "systemd-detect-virt", "-c").Output(); len(out) > 0 {
		if v := strings.TrimSpace(string(out)); v != "none" && v != "" {
			return v
		}
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "docker"
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return "podman"
	}
	if b, err := os.ReadFile("/proc/1/environ"); err == nil && strings.Contains(string(b), "container=lxc") {
		return "lxc"
	}
	return ""
}

// activeSwap returns the first active swap device, or "".
func activeSwap() string {
	b, err := os.ReadFile("/proc/swaps")
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) < 2 {
		return ""
	}
	if f := strings.Fields(lines[1]); len(f) > 0 {
		return f[0]
	}
	return ""
}

func diskUsage(path string) (total, used uint64) {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return 0, 0
	}
	bs := uint64(st.Bsize) //nolint:gosec // block size is positive
	total = st.Blocks * bs
	return total, total - st.Bfree*bs
}

func readOSReleaseID() string { return readOSRelease()["ID"] }
