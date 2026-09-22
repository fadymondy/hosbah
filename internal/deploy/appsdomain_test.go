package deploy

import (
	"os"
	"testing"
)

// clearAppsEnv unsets both generations of the apps-domain variables (a developer's shell may have
// either).
func clearAppsEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"APPS_DOMAIN", "APPS_TLS", "APPS_PROXIED", "CLOUDFLARE_TOKEN"} {
		t.Setenv("HOSBAH_"+k, "")
		t.Setenv("CLOUDY_"+k, "")
	}
	for _, k := range []string{"HOSBAH_APPS_PREFIX", "CLOUDY_APPS_PREFIX"} { // set-but-empty means "no prefix": unset
		if v, ok := os.LookupEnv(k); ok {
			t.Cleanup(func() { os.Setenv(k, v) })
			os.Unsetenv(k)
		}
	}
}

func TestAppsDomainFromEnvAndHostname(t *testing.T) {
	clearAppsEnv(t)
	t.Setenv("HOSBAH_APPS_DOMAIN", "FadyMondy.com.")
	t.Setenv("HOSBAH_APPS_TLS", "bogus")
	d := AppsDomainFromEnv()
	if !d.Enabled() || d.Domain != "fadymondy.com" || d.TLS != "auto" || d.DNS != nil {
		t.Fatalf("d = %+v", d)
	}
	if h := d.Hostname("healthdebug"); h != "hosbah-app-healthdebug.fadymondy.com" {
		t.Fatalf("hostname = %q", h)
	}
	if !d.Owns("hosbah-app-healthdebug.fadymondy.com") || d.Owns("www.fadymondy.com") || d.Owns("hosbah-app-x.evil.fadymondy.com") {
		t.Fatal("Owns")
	}
	// With the default prefix, hostnames given out before the rename (cloudy-app-…) stay the platform's.
	if !d.Owns("cloudy-app-healthdebug.fadymondy.com") || d.Owns("cloudy-app-x.evil.fadymondy.com") {
		t.Fatal("Owns: the pre-rename default prefix")
	}

	t.Setenv("HOSBAH_APPS_PREFIX", "")
	t.Setenv("HOSBAH_APPS_DOMAIN", "apps.example.dev")
	t.Setenv("HOSBAH_CLOUDFLARE_TOKEN", "x")
	d = AppsDomainFromEnv()
	if d.Hostname("shop") != "shop.apps.example.dev" || d.Hostname("www") != "" || d.DNS == nil || d.LegacyPrefix != "" {
		t.Fatalf("d = %+v", d)
	}

	t.Setenv("HOSBAH_APPS_DOMAIN", "")
	if d := AppsDomainFromEnv(); d.Enabled() || d.Hostname("api") != "" || d.Owns("api.example.com") {
		t.Fatal("off must be off")
	}
}

// An .env written before the rename (CLOUDY_APPS_*) configures the same apps domain; the HOSBAH_
// names win when both are set, and an explicit prefix only owns its own hostnames.
func TestAppsDomainReadsThePreRenameVariables(t *testing.T) {
	clearAppsEnv(t)
	t.Setenv("CLOUDY_APPS_DOMAIN", "hosbah.com")
	t.Setenv("CLOUDY_APPS_PREFIX", "")
	t.Setenv("CLOUDY_APPS_TLS", "internal")
	d := AppsDomainFromEnv()
	if d.Domain != "hosbah.com" || d.Prefix != "" || d.TLS != "internal" || d.Hostname("shop") != "shop.hosbah.com" {
		t.Fatalf("d = %+v", d)
	}
	t.Setenv("HOSBAH_APPS_DOMAIN", "apps.example.dev")
	t.Setenv("HOSBAH_APPS_PREFIX", "x-")
	d = AppsDomainFromEnv()
	if d.Domain != "apps.example.dev" || d.Prefix != "x-" || d.Owns("cloudy-app-shop.apps.example.dev") {
		t.Fatalf("d = %+v", d)
	}
}
