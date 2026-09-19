package agent

import (
	"context"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// Hardware parsers. They take the text of /proc files and tool output so tests can feed them
// fixtures; the collectors (hardware_linux.go) only read files and run tools. Every parser
// returns empty results on input it does not understand: missing hardware data is never an error.

// parseCPUInfo reads /proc/cpuinfo: the model name, physical cores and logical threads.
// Cores are distinct (physical id, core id) pairs; without those fields (some ARM and
// virtualised hosts) cores equal threads.
func parseCPUInfo(text string) (model string, cores, threads int) {
	pairs := map[string]bool{}
	phys, core := "", ""
	flush := func() {
		if core != "" {
			pairs[phys+"/"+core] = true
		}
		phys, core = "", ""
	}
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			if strings.TrimSpace(line) == "" {
				flush()
			}
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			threads++
		case "model name", "Model", "cpu model", "Processor":
			if model == "" && v != "" && !isDigits(v) {
				model = v
			}
		case "physical id":
			phys = v
		case "core id":
			core = v
		}
	}
	flush()
	cores = len(pairs)
	if cores == 0 || cores > threads {
		cores = threads
	}
	return model, cores, threads
}

func isDigits(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

// realFS are the filesystems that hold data. Everything else in /proc/mounts (tmpfs, overlay,
// proc, cgroup, squashfs snaps, fuse mounts…) is left out.
var realFS = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true, "f2fs": true,
	"jfs": true, "reiserfs": true, "bcachefs": true, "vfat": true, "exfat": true, "ntfs": true, "ntfs3": true,
}

type mountEntry struct{ Device, Mount, FS string }

// parseMounts reads /proc/self/mounts and keeps real filesystems, one entry per device (the
// shortest mount point wins, so bind mounts of the same device do not count its size twice).
// ZFS datasets share a pool, so they are keyed by pool.
func parseMounts(text string) []mountEntry {
	byDev := map[string]mountEntry{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || !realFS[f[2]] {
			continue
		}
		m := mountEntry{Device: f[0], Mount: unescapeMount(f[1]), FS: f[2]}
		key := m.Device
		if m.FS == "zfs" {
			key, _, _ = strings.Cut(m.Device, "/")
		}
		if cur, ok := byDev[key]; !ok || len(m.Mount) < len(cur.Mount) {
			byDev[key] = m
		}
	}
	out := make([]mountEntry, 0, len(byDev))
	for _, m := range byDev {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mount < out[j].Mount })
	return out
}

// unescapeMount undoes /proc/mounts' octal escapes (\040 for a space).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// nvidiaInventoryQuery / nvidiaMetricsQuery are the nvidia-smi --query-gpu field lists the two
// parsers below expect, in order.
const (
	nvidiaInventoryQuery = "index,name,memory.total,driver_version"
	nvidiaMetricsQuery   = "index,utilization.gpu,memory.used,memory.total,temperature.gpu"
)

// csvFields splits one line of nvidia-smi --format=csv,noheader,nounits output.
func csvFields(line string) []string {
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// num parses a number nvidia-smi printed; "[N/A]", "[Not Supported]" and blanks are 0.
func num(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

const mib = 1 << 20

// parseNvidiaInventory reads `nvidia-smi --query-gpu=<nvidiaInventoryQuery> --format=csv,noheader,nounits`.
func parseNvidiaInventory(text string) []agentproto.GPU {
	var out []agentproto.GPU
	for _, line := range strings.Split(text, "\n") {
		f := csvFields(line)
		if len(f) < 4 || !isDigits(f[0]) {
			continue
		}
		idx, _ := strconv.Atoi(f[0])
		out = append(out, agentproto.GPU{Index: idx, Name: f[1], Vendor: "NVIDIA",
			MemoryTotalBytes: uint64(num(f[2]) * mib), Driver: naToEmpty(f[3])})
	}
	return out
}

// parseNvidiaMetrics reads `nvidia-smi --query-gpu=<nvidiaMetricsQuery> --format=csv,noheader,nounits`.
func parseNvidiaMetrics(text string) []agentproto.GPUMetric {
	var out []agentproto.GPUMetric
	for _, line := range strings.Split(text, "\n") {
		f := csvFields(line)
		if len(f) < 5 || !isDigits(f[0]) {
			continue
		}
		idx, _ := strconv.Atoi(f[0])
		out = append(out, agentproto.GPUMetric{Index: idx, UtilPercent: num(f[1]),
			MemUsedBytes: uint64(num(f[2]) * mib), MemTotalBytes: uint64(num(f[3]) * mib), TempC: num(f[4])})
	}
	return out
}

func naToEmpty(s string) string {
	if strings.HasPrefix(s, "[") {
		return ""
	}
	return s
}

// displayClasses are the lspci device classes of a graphics adapter.
var displayClasses = map[string]bool{"VGA compatible controller": true, "3D controller": true, "Display controller": true}

// parseLspci reads `lspci -mm` (machine-readable: slot "class" "vendor" "device" …) and returns
// the display adapters, name and vendor only.
func parseLspci(text string) []agentproto.GPU {
	var out []agentproto.GPU
	for _, line := range strings.Split(text, "\n") {
		f := quotedFields(line)
		if len(f) < 4 || !displayClasses[f[1]] {
			continue
		}
		out = append(out, agentproto.GPU{Index: len(out), Vendor: shortVendor(f[2]), Name: f[3]})
	}
	return out
}

// quotedFields splits a line into whitespace-separated fields, a "double quoted" run being one.
func quotedFields(line string) []string {
	var out []string
	line = strings.TrimSpace(line)
	for line != "" {
		if line[0] == '"' {
			end := strings.IndexByte(line[1:], '"')
			if end < 0 {
				out = append(out, line[1:])
				break
			}
			out = append(out, line[1:1+end])
			line = strings.TrimSpace(line[end+2:])
			continue
		}
		i := strings.IndexAny(line, " \t")
		if i < 0 {
			out = append(out, line)
			break
		}
		out = append(out, line[:i])
		line = strings.TrimSpace(line[i:])
	}
	return out
}

// shortVendor turns lspci's vendor string into a familiar name.
func shortVendor(v string) string {
	l := strings.ToLower(v)
	switch {
	case strings.Contains(l, "nvidia"):
		return "NVIDIA"
	case strings.Contains(l, "advanced micro devices"), strings.Contains(l, "amd"), strings.Contains(l, "ati "):
		return "AMD"
	case strings.Contains(l, "intel"):
		return "Intel"
	}
	return v
}

// netCounters is one interface's total received / transmitted bytes.
type netCounters struct{ rx, tx uint64 }

// virtualIface names interfaces whose traffic is already counted on another one (container
// veths, bridges, Proxmox firewall and tap devices), so the host's rate is not counted twice.
func virtualIface(name string) bool {
	if name == "lo" {
		return true
	}
	for _, p := range []string{"veth", "docker", "br-", "virbr", "cni", "flannel", "tap", "fwpr", "fwln", "fwbr", "vmbr", "kube-ipvs"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// parseNetDev reads /proc/net/dev and returns the summed counters of non-virtual interfaces.
func parseNetDev(text string) (total netCounters, ok bool) {
	for _, line := range strings.Split(text, "\n") {
		name, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		f := strings.Fields(rest)
		if len(f) < 9 || virtualIface(name) {
			continue
		}
		rx, err1 := strconv.ParseUint(f[0], 10, 64)
		tx, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		total.rx += rx
		total.tx += tx
		ok = true
	}
	return total, ok
}

// netRate turns two readings into bytes per second. A counter that went backwards (an
// interface came or went, a wrap) yields 0 rather than a huge number.
func netRate(prev, cur netCounters, elapsed time.Duration) (rx, tx float64) {
	secs := elapsed.Seconds()
	if secs <= 0 {
		return 0, 0
	}
	if cur.rx >= prev.rx {
		rx = float64(cur.rx-prev.rx) / secs
	}
	if cur.tx >= prev.tx {
		tx = float64(cur.tx-prev.tx) / secs
	}
	return rx, tx
}

// interfaces lists non-loopback interfaces with their addresses (portable: net.Interfaces).
func interfaces() (out []agentproto.NetInterface, publicIP string) {
	ifs, err := net.Interfaces()
	if err != nil {
		return []agentproto.NetInterface{}, ""
	}
	out = []agentproto.NetInterface{}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		ni := agentproto.NetInterface{Name: ifc.Name, MAC: ifc.HardwareAddr.String(), Addrs: []string{}}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ni.Addrs = append(ni.Addrs, a.String())
			if ipn, ok := a.(*net.IPNet); ok && publicIP == "" && isPublicIP(ipn.IP) {
				publicIP = ipn.IP.String()
			}
		}
		out = append(out, ni)
	}
	return out, publicIP
}

// isPublicIP: a globally routable unicast address (not private, CGNAT, link-local or ULA).
func isPublicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 { // 100.64.0.0/10 (CGNAT, tailnets)
		return false
	}
	return true
}

// runTool runs a read-only tool with a short timeout; a missing tool or a failure is "" (the
// caller then reports nothing, never an error).
func runTool(ctx context.Context, name string, args ...string) string {
	if _, err := exec.LookPath(name); err != nil {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// nvidiaGPUs is the NVIDIA inventory, or nil without nvidia-smi.
func nvidiaGPUs(ctx context.Context) []agentproto.GPU {
	return parseNvidiaInventory(runTool(ctx, "nvidia-smi", "--query-gpu="+nvidiaInventoryQuery, "--format=csv,noheader,nounits"))
}

// nvidiaMetrics is one NVIDIA reading, or nil without nvidia-smi.
func nvidiaMetrics(ctx context.Context) []agentproto.GPUMetric {
	return parseNvidiaMetrics(runTool(ctx, "nvidia-smi", "--query-gpu="+nvidiaMetricsQuery, "--format=csv,noheader,nounits"))
}

// gpus prefers nvidia-smi (memory, driver) and falls back to lspci (name and vendor).
func gpus(ctx context.Context) []agentproto.GPU {
	if g := nvidiaGPUs(ctx); len(g) > 0 {
		return g
	}
	if g := parseLspci(runTool(ctx, "lspci", "-mm")); len(g) > 0 {
		return g
	}
	return []agentproto.GPU{}
}
