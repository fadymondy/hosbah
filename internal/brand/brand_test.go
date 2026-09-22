package brand

import "testing"

func TestGetenvPrefersHosbahAndFallsBackToCloudy(t *testing.T) {
	t.Setenv("HOSBAH_BRAND_TEST", "")
	t.Setenv("CLOUDY_BRAND_TEST", "old")
	if got := Getenv("BRAND_TEST"); got != "old" {
		t.Fatalf("CLOUDY_ fallback: got %q", got)
	}
	t.Setenv("HOSBAH_BRAND_TEST", "new")
	if got := Getenv("BRAND_TEST"); got != "new" {
		t.Fatalf("HOSBAH_ wins: got %q", got)
	}
	t.Setenv("CLOUDY_BRAND_TEST", "")
	if got := Getenv("BRAND_TEST"); got != "new" {
		t.Fatalf("HOSBAH_ alone: got %q", got)
	}
}

func TestLookupEnvTreatsAnEmptyHosbahVariableAsSet(t *testing.T) {
	t.Setenv("CLOUDY_BRAND_LOOKUP", "cloudy-app-")
	if v, ok := LookupEnv("BRAND_LOOKUP"); !ok || v != "cloudy-app-" {
		t.Fatalf("CLOUDY_ fallback: %q %v", v, ok)
	}
	t.Setenv("HOSBAH_BRAND_LOOKUP", "")
	if v, ok := LookupEnv("BRAND_LOOKUP"); !ok || v != "" {
		t.Fatalf("an empty HOSBAH_ variable is an explicit value: %q %v", v, ok)
	}
	if _, ok := LookupEnv("BRAND_LOOKUP_NEVER_SET"); ok {
		t.Fatal("neither set")
	}
}
