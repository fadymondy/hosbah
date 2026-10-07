package agent

import "testing"

func TestBuildCacheKeepScalesWithDisk(t *testing.T) {
	const gb = uint64(1) << 30
	for _, c := range []struct {
		total uint64
		want  string
	}{
		{0, "8192mb"},        // unknown disk keeps the fixed cap
		{20 * gb, "3072mb"},  // a 20 GB LXC keeps 15%, not 40%
		{100 * gb, "8192mb"}, // large disks stay at the cap
		{4 * gb, "1024mb"},   // tiny disks never drop under one build's layers
		{6830 * gb / 100, "8192mb"},
	} {
		if got := buildCacheKeep(c.total); got != c.want {
			t.Errorf("buildCacheKeep(%d) = %s, want %s", c.total, got, c.want)
		}
	}
}
