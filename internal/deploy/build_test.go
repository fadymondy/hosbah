package deploy

import "testing"

func TestValidBuildEnvNames(t *testing.T) {
	if err := ValidBuildEnvNames(nil); err != nil {
		t.Errorf("nil should be fine: %v", err)
	}
	if err := ValidBuildEnvNames([]string{"NPM_TOKEN", "SENTRY_DSN", "_UNDERSCORE9"}); err != nil {
		t.Errorf("good names: %v", err)
	}
	for _, bad := range [][]string{
		{"9STARTS_WITH_DIGIT"},
		{"has space"},
		{"has-dash"},
		{""},
		{"lower;case;bad"},
	} {
		if err := ValidBuildEnvNames(bad); err == nil {
			t.Errorf("%v should be rejected", bad)
		}
	}
	var many []string
	for i := 0; i < MaxBuildEnvNames+1; i++ {
		many = append(many, "V")
	}
	if err := ValidBuildEnvNames(many); err == nil {
		t.Error("too many names should be rejected")
	}
}

func TestDedupBuildEnvNames(t *testing.T) {
	got := DedupBuildEnvNames([]string{"A", "B", "A", "C", "B"})
	want := []string{"A", "B", "C"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestMarshalUnmarshalBuildEnv(t *testing.T) {
	if got := MarshalBuildEnv(nil); got != "[]" {
		t.Errorf("nil -> %q, want []", got)
	}
	raw := MarshalBuildEnv([]string{"A", "B"})
	names := UnmarshalBuildEnv(raw)
	if len(names) != 2 || names[0] != "A" || names[1] != "B" {
		t.Fatalf("round trip: %v", names)
	}
	if got := UnmarshalBuildEnv(""); got != nil {
		t.Errorf(`""` + " should be nil, got %v", got)
	}
	if got := UnmarshalBuildEnv("not json"); got != nil {
		t.Errorf("invalid json should be nil, got %v", got)
	}
}

func TestValidDockerfilePathAndBuildContext(t *testing.T) {
	for _, ok := range []string{"", "Dockerfile", "docker/Dockerfile", "."} {
		if !ValidDockerfilePath(ok) {
			t.Errorf("ValidDockerfilePath(%q) should be true", ok)
		}
	}
	for _, bad := range []string{"..", "../etc/passwd", "/etc/passwd"} {
		if ValidDockerfilePath(bad) {
			t.Errorf("ValidDockerfilePath(%q) should be false", bad)
		}
		if ValidBuildContext(bad) {
			t.Errorf("ValidBuildContext(%q) should be false", bad)
		}
	}
}
