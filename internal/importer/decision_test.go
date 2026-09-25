package importer

import "testing"

// TestApplyDecision: a member's decision always overrides classification (MH-574 A4), except when
// there is none, in which case Classify's own call stands.
func TestApplyDecision(t *testing.T) {
	cases := []struct {
		name       string
		it         Item
		decision   string
		wantIgnore bool
		wantTarget string
		wantKind   string
	}{
		{"no decision keeps target", Item{Target: TargetApp, Kind: ""}, "", false, TargetApp, ""},
		{"unknown decision keeps target", Item{Target: TargetDataService, Kind: "postgres"}, "made-up", false, TargetDataService, "postgres"},
		{"ignore", Item{Target: TargetApp}, DecisionIgnore, true, TargetApp, ""},
		{"force app clears a detected db kind", Item{Target: TargetDataService, Kind: "postgres"}, DecisionApp, false, TargetApp, ""},
		{"force app on an already-app item is a no-op", Item{Target: TargetApp}, DecisionApp, false, TargetApp, ""},
		{"force database keeps whatever kind Classify found", Item{Target: TargetApp, Kind: ""}, DecisionDatabase, false, TargetDataService, ""},
		{"force database on a container already classified as a database", Item{Target: TargetDataService, Kind: "redis"}, DecisionDatabase, false, TargetDataService, "redis"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, ignore := applyDecision(c.it, c.decision)
			if ignore != c.wantIgnore {
				t.Fatalf("ignore = %v, want %v", ignore, c.wantIgnore)
			}
			if ignore {
				return
			}
			if out.Target != c.wantTarget {
				t.Errorf("target = %q, want %q", out.Target, c.wantTarget)
			}
			if out.Kind != c.wantKind {
				t.Errorf("kind = %q, want %q", out.Kind, c.wantKind)
			}
		})
	}
}
