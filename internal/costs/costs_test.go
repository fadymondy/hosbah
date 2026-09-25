package costs

import "testing"

func TestUsageHint(t *testing.T) {
	cases := []struct {
		name string
		u    Usage
		want ServerHint
	}{
		{"no readings", Usage{Readings: 0, MemTotal: 1000}, ServerHintNone},
		{"unknown total", Usage{Readings: 10, MemTotal: 0}, ServerHintNone},
		{"over-provisioned", Usage{Readings: 10, P95CPUPercent: 5, AvgMemUsed: 200, MemTotal: 1000}, ServerHintOverProvisioned},
		{"under pressure cpu", Usage{Readings: 10, P95CPUPercent: 90, AvgMemUsed: 200, MemTotal: 1000}, ServerHintUnderPressure},
		{"under pressure mem", Usage{Readings: 10, P95CPUPercent: 5, AvgMemUsed: 900, MemTotal: 1000}, ServerHintUnderPressure},
		{"fine", Usage{Readings: 10, P95CPUPercent: 50, AvgMemUsed: 500, MemTotal: 1000}, ServerHintNone},
		{"boundary not over", Usage{Readings: 10, P95CPUPercent: 10, AvgMemUsed: 300, MemTotal: 1000}, ServerHintNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.u.Hint(); got != c.want {
				t.Fatalf("Hint() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAppMemoryHint(t *testing.T) {
	if got := AppMemoryHint(0, 100); got != AppHintNone {
		t.Fatalf("no limit: got %q", got)
	}
	if got := AppMemoryHint(1024, 0); got != AppHintNone {
		t.Fatalf("unknown server usage: got %q", got)
	}
	// 1024 MB limit vs a server averaging 100 MB used: way over 4x.
	if got := AppMemoryHint(1024, 100*1024*1024); got != AppHintOverCap {
		t.Fatalf("over cap: got %q", got)
	}
	// 512 MB limit vs a server averaging 200 MB used: within 4x.
	if got := AppMemoryHint(512, 200*1024*1024); got != AppHintNone {
		t.Fatalf("within cap: got %q", got)
	}
}

func TestAllocateEqualSplitFallback(t *testing.T) {
	apps := []AppShare{{ID: "b"}, {ID: "a"}, {ID: "c"}}
	allocs, idle := Allocate(1000, apps, 2048)
	if idle != 0 {
		t.Fatalf("idle = %d, want 0", idle)
	}
	if len(allocs) != 3 {
		t.Fatalf("len = %d", len(allocs))
	}
	var sum int64
	for _, a := range allocs {
		sum += a.Cents
	}
	if sum != 1000 {
		t.Fatalf("sum = %d, want 1000", sum)
	}
	// sorted by id
	if allocs[0].ID != "a" || allocs[1].ID != "b" || allocs[2].ID != "c" {
		t.Fatalf("not sorted: %+v", allocs)
	}
}

func TestAllocateByMemoryShare(t *testing.T) {
	apps := []AppShare{{ID: "web", MemoryLimitMB: 512}, {ID: "worker", MemoryLimitMB: 256}}
	// server has 2048 MB total; apps claim 768 of it, so 1280 MB (62.5%) is idle capacity.
	allocs, idle := Allocate(10000, apps, 2048)
	byID := map[string]int64{}
	var sum int64
	for _, a := range allocs {
		byID[a.ID] = a.Cents
		sum += a.Cents
	}
	if sum+idle != 10000 {
		t.Fatalf("sum+idle = %d, want 10000", sum+idle)
	}
	if byID["web"] <= byID["worker"] {
		t.Fatalf("web should get more than worker: %+v", byID)
	}
	if idle <= 0 {
		t.Fatalf("idle should be > 0 given unclaimed memory, got %d", idle)
	}
}

func TestAllocateOvercommitClampsIdleToZero(t *testing.T) {
	apps := []AppShare{{ID: "a", MemoryLimitMB: 3000}, {ID: "b", MemoryLimitMB: 3000}}
	// server total (1024) is smaller than the sum of limits (6000): the whole cost is allocated.
	allocs, idle := Allocate(1000, apps, 1024)
	if idle != 0 {
		t.Fatalf("idle = %d, want 0", idle)
	}
	var sum int64
	for _, a := range allocs {
		sum += a.Cents
	}
	if sum != 1000 {
		t.Fatalf("sum = %d, want 1000", sum)
	}
}

func TestAllocateNoApps(t *testing.T) {
	allocs, idle := Allocate(500, nil, 1024)
	if allocs != nil || idle != 500 {
		t.Fatalf("got %+v, %d", allocs, idle)
	}
}
