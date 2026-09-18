package deploy

import "testing"

func TestAppsDomainFromEnvAndHostname(t *testing.T) {
	t.Setenv("CLOUDY_APPS_DOMAIN", "FadyMondy.com.")
	t.Setenv("CLOUDY_APPS_TLS", "bogus")
	d := AppsDomainFromEnv()
	if !d.Enabled() || d.Domain != "fadymondy.com" || d.TLS != "auto" || d.DNS != nil {
		t.Fatalf("d = %+v", d)
	}
	if h := d.Hostname("healthdebug"); h != "cloudy-app-healthdebug.fadymondy.com" {
		t.Fatalf("hostname = %q", h)
	}
	if !d.Owns("cloudy-app-healthdebug.fadymondy.com") || d.Owns("www.fadymondy.com") || d.Owns("cloudy-app-x.evil.fadymondy.com") {
		t.Fatal("Owns")
	}

	t.Setenv("CLOUDY_APPS_PREFIX", "")
	t.Setenv("CLOUDY_APPS_DOMAIN", "apps.example.dev")
	t.Setenv("CLOUDY_CLOUDFLARE_TOKEN", "x")
	d = AppsDomainFromEnv()
	if d.Hostname("api") != "api.apps.example.dev" || d.DNS == nil {
		t.Fatalf("d = %+v", d)
	}

	t.Setenv("CLOUDY_APPS_DOMAIN", "")
	if d := AppsDomainFromEnv(); d.Enabled() || d.Hostname("api") != "" || d.Owns("api.example.com") {
		t.Fatal("off must be off")
	}
}
