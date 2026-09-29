package deploy

import (
	"testing"

	db "github.com/fadymondy/hosbah/internal/db/gen"
)

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

func TestDesktopBrowserTLS(t *testing.T) {
	ip := func(s string) db.Server { return db.Server{IpAddress: &s} }
	for srv, want := range map[*db.Server]string{
		ptr(ip("10.10.10.121")): "off", ptr(ip("127.0.0.1")): "off",
		ptr(ip("45.129.183.99")): "auto", {}: "auto",
	} {
		if got := desktopBrowserTLS(*srv); got != want {
			t.Errorf("desktopBrowserTLS(%v) = %q, want %q", srv.IpAddress, got, want)
		}
	}
}

