//go:build linux

package agent

import (
	"context"
	"os"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// collectHardware reads CPU, disks, interfaces and GPUs. Anything unreadable is left empty.
func collectHardware(ctx context.Context) *agentproto.Hardware {
	h := &agentproto.Hardware{Disks: []agentproto.Disk{}, GPUs: gpus(ctx)}
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		h.CPUModel, h.CPUCores, h.CPUThreads = parseCPUInfo(string(b))
	}
	h.MemTotalBytes = meminfo()["MemTotal"]
	if b, err := os.ReadFile("/proc/self/mounts"); err == nil {
		for _, m := range parseMounts(string(b)) {
			total, used := diskUsage(m.Mount)
			if total == 0 {
				continue
			}
			h.Disks = append(h.Disks, agentproto.Disk{Mount: m.Mount, Device: m.Device, FS: m.FS, SizeBytes: total, UsedBytes: used})
		}
	}
	h.Interfaces, h.PublicIP = interfaces()
	return h
}

// readNetDev is the host's summed interface counters.
func readNetDev() (netCounters, bool) {
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return netCounters{}, false
	}
	return parseNetDev(string(b))
}
