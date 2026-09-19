//go:build linux

package agent

import (
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// Sampler reads the heartbeat metrics. CPU is the busy share since the previous sample (the
// first sample measures over a short window of its own).
type Sampler struct {
	mu          sync.Mutex
	idle, total uint64
	have        bool
	net         netCounters
	netAt       time.Time
	at          time.Time // last full reading, and its figures
	cpu, rx, tx float64
}

// NewSampler returns a sampler with no history.
func NewSampler() *Sampler { return &Sampler{} }

// Sample reads CPU, memory, root-disk usage, uptime and load.
func (s *Sampler) Sample() agentproto.Metrics {
	m := agentproto.Metrics{At: time.Now().UTC()}

	s.mu.Lock()
	// Two readings a moment apart (a heartbeat and a live reading on the same tick) would measure
	// over almost no time: the second reuses the first's CPU and network figures instead.
	if !s.at.IsZero() && time.Since(s.at) < time.Second {
		m.CPUPercent, m.NetRxBytesPerSec, m.NetTxBytesPerSec = s.cpu, s.rx, s.tx
	} else {
		idle, total, ok := readCPU()
		if ok && !s.have {
			s.idle, s.total, s.have = idle, total, true
			time.Sleep(250 * time.Millisecond)
			idle, total, ok = readCPU()
		}
		if ok && total > s.total {
			busy := 1 - float64(idle-s.idle)/float64(total-s.total)
			m.CPUPercent = math.Round(busy*1000) / 10
			s.idle, s.total = idle, total
		}
		if cur, ok := readNetDev(); ok {
			now := time.Now()
			if !s.netAt.IsZero() {
				rx, tx := netRate(s.net, cur, now.Sub(s.netAt))
				m.NetRxBytesPerSec, m.NetTxBytesPerSec = math.Round(rx), math.Round(tx)
			}
			s.net, s.netAt = cur, now
		}
		s.at, s.cpu, s.rx, s.tx = time.Now(), m.CPUPercent, m.NetRxBytesPerSec, m.NetTxBytesPerSec
	}
	s.mu.Unlock()

	mi := meminfo()
	m.MemTotalBytes = mi["MemTotal"]
	if avail := mi["MemAvailable"]; avail <= m.MemTotalBytes {
		m.MemUsedBytes = m.MemTotalBytes - avail
	}
	m.DiskTotalBytes, m.DiskUsedBytes = diskUsage("/")
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				m.UptimeSeconds = uint64(v)
			}
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			m.Load1, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	return m
}

// readCPU returns idle (idle+iowait) and total jiffies from the aggregate line of /proc/stat.
func readCPU() (idle, total uint64, ok bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	for i, f := range fields[1:] {
		if i >= 8 { // user nice system idle iowait irq softirq steal; guest time is already in user
			break
		}
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += v
		if i == 3 || i == 4 {
			idle += v
		}
	}
	return idle, total, true
}
