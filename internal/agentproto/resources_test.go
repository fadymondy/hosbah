package agentproto

import "testing"

func TestResourceLimitsValidate(t *testing.T) {
	cases := []struct {
		name string
		r    ResourceLimits
		ok   bool
	}{
		{"zero is unlimited", ResourceLimits{}, true},
		{"memory in range", ResourceLimits{MemoryMB: 512}, true},
		{"memory at min", ResourceLimits{MemoryMB: MinMemoryMB}, true},
		{"memory at max", ResourceLimits{MemoryMB: MaxMemoryMB}, true},
		{"memory below min", ResourceLimits{MemoryMB: MinMemoryMB - 1}, false},
		{"memory above max", ResourceLimits{MemoryMB: MaxMemoryMB + 1}, false},
		{"cpus in range", ResourceLimits{CPUs: 0.5}, true},
		{"cpus at min", ResourceLimits{CPUs: MinCPUs}, true},
		{"cpus at max", ResourceLimits{CPUs: MaxCPUs}, true},
		{"cpus below min", ResourceLimits{CPUs: MinCPUs / 2}, false},
		{"cpus above max", ResourceLimits{CPUs: MaxCPUs + 1}, false},
		{"both set and valid", ResourceLimits{MemoryMB: 1024, CPUs: 2}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.r.Validate()
			if c.ok && err != nil {
				t.Fatalf("Validate(%+v): unexpected error %v", c.r, err)
			}
			if !c.ok && err == nil {
				t.Fatalf("Validate(%+v): expected an error, got none", c.r)
			}
		})
	}
}

func TestResourceLimitsEmpty(t *testing.T) {
	if !(ResourceLimits{}).Empty() {
		t.Fatal("a zero-value ResourceLimits should be Empty")
	}
	if (ResourceLimits{MemoryMB: 256}).Empty() {
		t.Fatal("a memory limit alone should not be Empty")
	}
	if (ResourceLimits{CPUs: 0.5}).Empty() {
		t.Fatal("a CPU limit alone should not be Empty")
	}
}

func TestResourceLimitsDockerArgs(t *testing.T) {
	cases := []struct {
		name string
		r    ResourceLimits
		want []string
	}{
		{"unlimited: no flags", ResourceLimits{}, nil},
		{
			"memory only: --memory and --memory-swap match",
			ResourceLimits{MemoryMB: 512},
			[]string{"--memory", "512m", "--memory-swap", "512m"},
		},
		{"cpus only", ResourceLimits{CPUs: 0.5}, []string{"--cpus", "0.5"}},
		{
			"both, in order",
			ResourceLimits{MemoryMB: 1024, CPUs: 2},
			[]string{"--memory", "1024m", "--memory-swap", "1024m", "--cpus", "2"},
		},
		{"a whole-number CPU count has no trailing zeros or dot", ResourceLimits{CPUs: 4}, []string{"--cpus", "4"}},
		{"a fractional CPU count keeps only its significant digits", ResourceLimits{CPUs: 0.05}, []string{"--cpus", "0.05"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.r.DockerArgs()
			if len(got) != len(c.want) {
				t.Fatalf("DockerArgs(%+v) = %v, want %v", c.r, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("DockerArgs(%+v) = %v, want %v", c.r, got, c.want)
				}
			}
		})
	}
}
