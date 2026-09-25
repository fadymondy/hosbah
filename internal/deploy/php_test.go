package deploy

import (
	"strings"
	"testing"
)

func TestValidPHPVersion(t *testing.T) {
	for _, v := range []string{"auto", "8.1", "8.2", "8.3", "8.4"} {
		if !ValidPHPVersion(v) {
			t.Errorf("ValidPHPVersion(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "8.0", "8.5", "php8.3", "8.3 "} {
		if ValidPHPVersion(v) {
			t.Errorf("ValidPHPVersion(%q) = true, want false", v)
		}
	}
}

func TestValidatePHPIni(t *testing.T) {
	ok := []map[string]string{
		{},
		{"memory_limit": "512M"},
		{"memory_limit": "-1"},
		{"date.timezone": "UTC"},
		{"opcache.enable": "1", "max_execution_time": "30"},
	}
	for _, m := range ok {
		if err := ValidatePHPIni(m); err != nil {
			t.Errorf("ValidatePHPIni(%v) = %v, want nil", m, err)
		}
	}
	bad := []map[string]string{
		{"disable_functions": "exec"},            // not an allowed key
		{"open_basedir": "/"},                    // not an allowed key
		{"memory_limit": "512M\r\nmax_input_vars = 99999"}, // newline injection
		{"memory_limit": "512M;evil=1"},           // ';' comment/separator
		{"memory_limit": "[foo]"},                 // section syntax
		{"memory_limit": strings.Repeat("a", 300)}, // too long
	}
	for _, m := range bad {
		if err := ValidatePHPIni(m); err == nil {
			t.Errorf("ValidatePHPIni(%v) = nil, want an error", m)
		}
	}
	if len(PHPIniKeys) > MaxPHPIniSettings {
		t.Fatalf("PHPIniKeys (%d) must fit under MaxPHPIniSettings (%d) for real configs to validate", len(PHPIniKeys), MaxPHPIniSettings)
	}
}

func TestRenderPHPIni(t *testing.T) {
	out := RenderPHPIni(map[string]string{"upload_max_filesize": "64M", "memory_limit": "512M"})
	// Sorted: date.timezone < memory_limit < upload_max_filesize alphabetically; here just memory/upload.
	iMem := strings.Index(out, "memory_limit = 512M")
	iUp := strings.Index(out, "upload_max_filesize = 64M")
	if iMem < 0 || iUp < 0 || iMem > iUp {
		t.Fatalf("RenderPHPIni not sorted or missing entries: %q", out)
	}
}

func TestPHPIniEnvValue(t *testing.T) {
	got := PHPIniEnvValue(map[string]string{"upload_max_filesize": "64M", "memory_limit": "512M"})
	want := "memory_limit=512M;upload_max_filesize=64M"
	if got != want {
		t.Errorf("PHPIniEnvValue = %q, want %q", got, want)
	}
	if PHPIniEnvValue(nil) != "" {
		t.Errorf("PHPIniEnvValue(nil) should be empty")
	}
}

func TestRailpackPHPEnv(t *testing.T) {
	if env := RailpackPHPEnv(""); env != nil {
		t.Errorf("RailpackPHPEnv(\"\") = %v, want nil", env)
	}
	if env := RailpackPHPEnv("auto"); env != nil {
		t.Errorf("RailpackPHPEnv(auto) = %v, want nil", env)
	}
	env := RailpackPHPEnv("8.3")
	if len(env) != 1 || env[0] != "RAILPACK_PHP_VERSION=8.3" {
		t.Errorf("RailpackPHPEnv(8.3) = %v", env)
	}
}

func TestPhpConfigActive(t *testing.T) {
	cases := []struct {
		version, ini string
		want         bool
	}{
		{"auto", "{}", false},
		{"", "{}", false},
		{"auto", `{"memory_limit":"512M"}`, true},
		{"8.2", "{}", true},
		{"8.2", `{"memory_limit":"512M"}`, true},
	}
	for _, c := range cases {
		if got := phpConfigActive(c.version, c.ini); got != c.want {
			t.Errorf("phpConfigActive(%q, %q) = %v, want %v", c.version, c.ini, got, c.want)
		}
	}
}
