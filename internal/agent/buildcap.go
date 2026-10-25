package agent

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

/*
Build CPU caps: an image build must never starve the apps the same server is serving (fadymondy.com,
2026-10-23: back-to-back builds pushed the load to 56 on 6 cores, and the site timed out).

BuildKit inside dockerd runs every RUN step in a cgroup of its own, outside docker.service and
outside systemd: /sys/fs/cgroup/system.slice/system.slice:docker:<id> with Docker's systemd cgroup
driver (read off a live server), /sys/fs/cgroup/docker/buildkit/<id> with the cgroupfs driver.
`--cgroup-parent` is not reliably honoured there, so while a build runs the agent finds those step
cgroups and caps them itself:

	cpu.weight  10 (apps' containers keep the default 100: under contention they get ~10x the CPU)
	cpu.max     the build's share of the server's CPUs (default half; agentproto CPUShare)
	memory.high the server's memory less what its apps need (buildMemHigh: total - max(2 GiB, 20%),
	            never below half of it)
	io.weight   10, where the io controller offers it

The weight is the guarantee (it gives way the moment an app needs the CPU, and costs nothing when
the server is idle); the quota bounds a build even on an otherwise idle server, so a burst of
requests never lands on a server that is already at 100%.

memory.high is what keeps the apps' disk reads fast. io.weight only works under the BFQ scheduler,
and servers usually run mq-deadline or none (fadymondy-web, an LXC on a loop-mounted raw disk, runs
mq-deadline), so it does nothing there. A big build's page cache (Go build cache, node_modules)
filled that LXC up to its own memory.high 28 million times (memory.events), so every task in it,
the site included, was throttled into reclaim and kept rereading its files from disk
(workingset_refault_file 4.6 billion pages, io pressure avg300 28%; fadymondy.com #706, 2026-10-25).
With memory.high on the step, the build reclaims its own cache first. It throttles, never kills.

memory.high must leave the compiler its working set, though: at half the server (5 GiB of
fadymondy-web's 10) `go build ./cmd/api` spent its time in reclaim and went from 6-8 minutes to 30+
(fadymondy.com #715-#718, 2026-10-25). The apps there need ~2.5 GiB, so the build gets the rest less
a margin, not a share. Go 1.25+ toolchains in the step read the quota for their GOMAXPROCS, and the
containers the agent runs a build in get GOMAXPROCS set to the quota (buildGoEnv) for older ones. Containers the agent starts itself for a build (the fast track's builder
and runtime modes, Trivy) take the same limits as `docker run` flags (buildRunFlags).
*/

// buildStepCgroups are where BuildKit puts its RUN steps (systemd driver; cgroupfs driver).
var buildStepCgroups = []string{
	"/sys/fs/cgroup/system.slice/system.slice:docker:*",
	"/sys/fs/cgroup/docker/buildkit/*",
}

const (
	defaultBuildCPUShare = 0.5
	buildCPUWeight       = "10"
	cgroupPeriodUS       = 100000
)

// buildCPUs is how many CPUs a build may use for this share (0 = uncapped): share of the server's
// CPUs, at least one. share <= 0 is the default; share >= 1 means no cap.
func buildCPUs(share float64, ncpu int) float64 {
	if share <= 0 {
		share = defaultBuildCPUShare
	}
	if share >= 1 || ncpu <= 0 {
		return 0
	}
	cpus := share * float64(ncpu)
	if cpus < 1 {
		cpus = 1
	}
	return cpus
}

// buildMemHigh is the memory.high of a build step on a server with total bytes (0 = no limit): all of
// it but what the server's apps need, i.e. less max(2 GiB, 20%), and never under half of it. A
// share of 1 or more means no cap, as for the CPUs; total 0 (unknown) means no limit.
func buildMemHigh(share float64, total int64) int64 {
	if share >= 1 || total <= 0 {
		return 0
	}
	reserve := max(int64(2)<<30, total/5)
	return max(total-reserve, total/2)
}

// serverMemory is the memory this server's processes may use: MemTotal, or the cgroup's own limit
// when that is lower (a container's /proc/meminfo is not always virtualised). 0 when unknown.
func serverMemory() int64 {
	var total int64
	if f, err := os.Open("/proc/meminfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if kb, ok := strings.CutPrefix(sc.Text(), "MemTotal:"); ok {
				n, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(kb), " kB"), 10, 64)
				total = n * 1024
				break
			}
		}
		_ = f.Close()
	}
	if raw, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64); err == nil && n > 0 && (total == 0 || n < total) {
			total = n
		}
	}
	return total
}

// cpuMax is the cgroup v2 cpu.max line for that many CPUs.
func cpuMax(cpus float64) string {
	return fmt.Sprintf("%d %d", int64(cpus*cgroupPeriodUS), cgroupPeriodUS)
}

// buildGoEnv is the `docker run`/`docker exec` environment that keeps a Go toolchain from older
// than 1.25 (which ignores the CPU quota) to the build's CPUs. Never pass it to a container that
// becomes the app's image (`docker create` + commit): the app would keep it.
func buildGoEnv(share float64) []string {
	cpus := buildCPUs(share, runtime.NumCPU())
	if cpus == 0 {
		return nil
	}
	return []string{"-e", "GOMAXPROCS=" + strconv.Itoa(max(1, int(cpus)))}
}

// buildRunFlags are the `docker run` / `docker create` limits for a container a build starts.
func buildRunFlags(share float64) []string {
	cpus := buildCPUs(share, runtime.NumCPU())
	if cpus == 0 {
		return nil
	}
	return []string{"--cpus", strconv.FormatFloat(cpus, 'f', 2, 64), "--cpu-shares", "128"}
}

// buildCap caps BuildKit's step cgroups while a build runs.
type buildCap struct {
	cpus    float64
	memHigh int64 // memory.high per step in bytes; 0 = none
	globs   []string
	every   time.Duration
	stop    chan struct{}
	done    chan struct{}
	mu      sync.Mutex
	seen    map[string]bool
	capped  int
	failed  int
	lastErr error
}

// startBuildCap starts capping (a no-op cap when share is 1 or more); Stop ends it and logs what
// it did.
func startBuildCap(oc *OpContext, share float64) *buildCap {
	c := &buildCap{cpus: buildCPUs(share, runtime.NumCPU()), memHigh: buildMemHigh(share, serverMemory()), globs: buildStepCgroups, every: time.Second,
		stop: make(chan struct{}), done: make(chan struct{}), seen: map[string]bool{}}
	if c.cpus == 0 {
		close(c.done)
		oc.Info("build CPU cap: off (this build may use every CPU)")
		return c
	}
	mem := "memory uncapped"
	if c.memHigh > 0 {
		mem = fmt.Sprintf("memory high %.1f GiB", float64(c.memHigh)/(1<<30))
	}
	oc.Info("build CPU cap: %.2g of %d CPUs, CPU weight %s, %s (apps keep priority)", c.cpus, runtime.NumCPU(), buildCPUWeight, mem)
	go c.loop()
	return c
}

func (c *buildCap) loop() {
	defer close(c.done)
	t := time.NewTicker(c.every)
	defer t.Stop()
	for {
		c.sweep()
		select {
		case <-c.stop:
			return
		case <-t.C:
		}
	}
}

// sweep caps every step cgroup it has not seen yet.
func (c *buildCap) sweep() {
	for _, g := range c.globs {
		dirs, _ := filepath.Glob(g)
		for _, dir := range dirs {
			c.mu.Lock()
			seen := c.seen[dir]
			c.seen[dir] = true
			c.mu.Unlock()
			if seen {
				continue
			}
			err := capCgroup(dir, c.cpus, c.memHigh)
			c.mu.Lock()
			if err != nil {
				c.failed++
				c.lastErr = err
			} else {
				c.capped++
			}
			c.mu.Unlock()
		}
	}
}

// capCgroup writes the limits into one cgroup directory. The weight is what matters most, so it is
// written first; memory.high (0 = skip) and io.weight are best-effort (the memory controller may
// not be delegated to the step, and not every kernel/io scheduler offers io.weight).
func capCgroup(dir string, cpus float64, memHigh int64) error {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil // the step finished before we got to it
	}
	if err := os.WriteFile(filepath.Join(dir, "cpu.weight"), []byte(buildCPUWeight), 0o644); err != nil { //#nosec G306 -- a cgroup file
		if os.IsNotExist(err) {
			return nil // the step's cgroup went away meanwhile
		}
		return fmt.Errorf("cpu.weight: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cpu.max"), []byte(cpuMax(cpus)), 0o644); err != nil && !os.IsNotExist(err) { //#nosec G306 -- a cgroup file
		return fmt.Errorf("cpu.max: %w", err)
	}
	if memHigh > 0 {
		_ = os.WriteFile(filepath.Join(dir, "memory.high"), []byte(strconv.FormatInt(memHigh, 10)), 0o644) //#nosec G306 -- a cgroup file
	}
	_ = os.WriteFile(filepath.Join(dir, "io.weight"), []byte("default "+buildCPUWeight), 0o644) //#nosec G306 -- a cgroup file
	return nil
}

// Stop ends the capping and logs how many build steps it capped.
func (c *buildCap) Stop(oc *OpContext) {
	if c.cpus == 0 {
		return
	}
	close(c.stop)
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.failed > 0:
		oc.Info("build CPU cap: %d build steps capped, %d could not be (%v)", c.capped, c.failed, c.lastErr)
	case c.capped == 0:
		oc.Info("build CPU cap: no BuildKit step ran long enough to cap (everything cached, or this Docker keeps its steps elsewhere)")
	default:
		oc.Info("build CPU cap: %d build steps capped", c.capped)
	}
}
