package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildCPUs(t *testing.T) {
	cases := []struct {
		share float64
		ncpu  int
		want  float64
	}{
		{0, 6, 3},   // default: half
		{0.5, 6, 3}, // fadymondy.com's CT 123
		{0.25, 6, 1.5},
		{0.5, 1, 1}, // never under one CPU
		{1, 6, 0},   // no cap
		{1.5, 6, 0},
	}
	for _, c := range cases {
		if got := buildCPUs(c.share, c.ncpu); got != c.want {
			t.Errorf("buildCPUs(%v, %d) = %v, want %v", c.share, c.ncpu, got, c.want)
		}
	}
	if got := cpuMax(3); got != "300000 100000" {
		t.Errorf("cpuMax(3) = %q", got)
	}
	if got := cpuMax(1.5); got != "150000 100000" {
		t.Errorf("cpuMax(1.5) = %q", got)
	}
}

func TestBuildRunFlags(t *testing.T) {
	if buildRunFlags(1) != nil {
		t.Error("share 1 must add no flags")
	}
	f := buildRunFlags(0.5)
	if len(f) != 4 || f[0] != "--cpus" || f[2] != "--cpu-shares" {
		t.Errorf("flags %v", f)
	}
}

func TestBuildMemHigh(t *testing.T) {
	const gib = int64(1) << 30
	for _, tc := range []struct {
		share float64
		total int64
		want  int64
	}{
		{0, 10 * gib, 5 * gib},   // default half of fadymondy-web's 10 GiB
		{0.3, 10 * gib, 3 * gib}, // the app's own share
		{0.5, gib, gib},          // never under 1 GiB
		{1, 10 * gib, 0},         // share 1: uncapped
		{0.5, 0, 0},              // memory unknown: no limit
	} {
		if got := buildMemHigh(tc.share, tc.total); got != tc.want {
			t.Errorf("buildMemHigh(%v, %d) = %d, want %d", tc.share, tc.total, got, tc.want)
		}
	}
}

// The cap writes cpu.weight / cpu.max / memory.high / io.weight into every BuildKit step cgroup it finds, once.
func TestBuildCapSweep(t *testing.T) {
	root := t.TempDir()
	step := func(name string) string {
		dir := filepath.Join(root, "system.slice", "system.slice:docker:"+name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"cpu.weight", "cpu.max", "memory.high", "io.weight"} {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	app := filepath.Join(root, "system.slice", "docker-abc.scope") // an app's container: never touched
	_ = os.MkdirAll(app, 0o755)
	_ = os.WriteFile(filepath.Join(app, "cpu.weight"), []byte("100"), 0o644)

	a := step("one")
	c := &buildCap{cpus: 3, memHigh: 5 << 30, globs: []string{filepath.Join(root, "system.slice", "system.slice:docker:*")}, every: 10 * time.Millisecond,
		stop: make(chan struct{}), done: make(chan struct{}), seen: map[string]bool{}}
	go c.loop()
	time.Sleep(30 * time.Millisecond)
	b := step("two") // a step that starts later in the build
	time.Sleep(50 * time.Millisecond)
	close(c.stop)
	<-c.done

	for _, dir := range []string{a, b} {
		read := func(f string) string { raw, _ := os.ReadFile(filepath.Join(dir, f)); return string(raw) }
		if read("cpu.weight") != "10" || read("cpu.max") != "300000 100000" || read("memory.high") != "5368709120" || read("io.weight") != "default 10" {
			t.Errorf("%s: weight %q max %q mem %q io %q", dir, read("cpu.weight"), read("cpu.max"), read("memory.high"), read("io.weight"))
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(app, "cpu.weight")); string(raw) != "100" {
		t.Errorf("an app container's cgroup was changed: %q", raw)
	}
	if c.capped != 2 || c.failed != 0 {
		t.Errorf("capped %d failed %d", c.capped, c.failed)
	}
}
