package deploy

import "testing"

func TestZoneOf(t *testing.T) {
	zones := []string{"3x1.io", "moharrik.com", "sub.moharrik.com"}
	for host, want := range map[string]string{
		"app.moharrik.com":      "moharrik.com",
		"moharrik.com":          "moharrik.com",
		"x.sub.moharrik.com":    "sub.moharrik.com",
		"browser.3x1.io":        "3x1.io",
		"evilmoharrik.com":      "",
		"moharrik.com.evil.net": "",
	} {
		if got := zoneOf(zones, host); got != want {
			t.Errorf("zoneOf(%q) = %q, want %q", host, got, want)
		}
	}
}
