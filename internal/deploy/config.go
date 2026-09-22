package deploy

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
hosbah.toml — optional, at the repository root. Everything in it is optional too:

	port = 8080                         # the port the web process listens on (PORT is set to it)
	release = "php artisan migrate"     # runs once per deploy in a one-off container, before traffic moves

	[healthcheck]
	path = "/health"                    # must answer 2xx before the new release takes traffic
	timeout = 60                        # seconds

	[processes]
	web = "node server.js"              # overrides the image's start command
	"worker:queue" = "node worker.js"   # workers: no port, restarted with every deploy

Precedence: what the repository says wins (config as code); the app's settings in Hosbah are the
defaults when it says nothing. Unknown keys are an error, so a typo does not silently do nothing.

Before the rename the file was cloudy.toml. The agent reads hosbah.toml and, only when there is none,
cloudy.toml, so a repository that has not renamed its file deploys exactly as before.
*/

// ConfigFile is the app's config file at the repository root; LegacyConfigFile the name it had
// before the rename, read when ConfigFile is absent.
const (
	ConfigFile       = "hosbah.toml"
	LegacyConfigFile = "cloudy.toml"
)

// ConfigFileOf names the file an agent's detection read (older agents only knew cloudy.toml).
func ConfigFileOf(r agentproto.DetectResult) string {
	switch {
	case r.TomlFile == ConfigFile || r.TomlFile == LegacyConfigFile:
		return r.TomlFile
	case r.HosbahToml == "" && r.LegacyToml != "":
		return LegacyConfigFile
	}
	return ConfigFile
}

// AppConfig is a parsed hosbah.toml (or cloudy.toml).
type AppConfig struct {
	Port        int    `toml:"port"`
	Release     string `toml:"release"`
	Healthcheck struct {
		Path    string `toml:"path"`
		Timeout int    `toml:"timeout"`
	} `toml:"healthcheck"`
	Processes map[string]string `toml:"processes"`
}

// ParseConfig reads hosbah.toml. An empty file (or none) is a zero AppConfig.
func ParseConfig(raw string) (AppConfig, error) { return ParseConfigFile(raw, ConfigFile) }

// ParseConfigFile is ParseConfig for a file of that name (errors name it: hosbah.toml or cloudy.toml).
func ParseConfigFile(raw, file string) (AppConfig, error) {
	var c AppConfig
	if strings.TrimSpace(raw) == "" {
		return c, nil
	}
	md, err := toml.Decode(raw, &c)
	if err != nil {
		return c, fmt.Errorf("%s: %w", file, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return c, fmt.Errorf("%s: unknown key(s) %s", file, strings.Join(keys, ", "))
	}
	if c.Port != 0 && (c.Port < 1 || c.Port > 65535) {
		return c, errors.New(file + ": port must be between 1 and 65535")
	}
	if c.Healthcheck.Path != "" && !strings.HasPrefix(c.Healthcheck.Path, "/") {
		return c, errors.New(file + ": healthcheck.path must start with /")
	}
	if c.Healthcheck.Timeout != 0 && (c.Healthcheck.Timeout < 5 || c.Healthcheck.Timeout > 600) {
		return c, errors.New(file + ": healthcheck.timeout must be between 5 and 600 seconds")
	}
	for name, cmd := range c.Processes {
		if !agentproto.ValidProcess(name) {
			return c, fmt.Errorf(`%s: process %q — use "web" or "worker:<name>" (lowercase, digits, - or _)`, file, name)
		}
		if strings.TrimSpace(cmd) == "" {
			return c, fmt.Errorf("%s: process %q has an empty command", file, name)
		}
	}
	if len(c.Release) > 4096 {
		return c, errors.New(file + ": the release command is too long")
	}
	return c, nil
}

// Defaults are the app's own settings (from Hosbah), used where hosbah.toml says nothing.
type Defaults struct {
	Port          int
	HealthPath    string
	HealthTimeout int
}

// Effective is what a deploy actually uses.
type Effective struct {
	Port          int               `json:"port"`
	HealthPath    string            `json:"health_path"`
	HealthTimeout int               `json:"health_timeout"`
	Release       string            `json:"release,omitempty"`
	Processes     map[string]string `json:"processes"` // web always present ("" = the image's command)
}

// Resolve merges hosbah.toml over the app's defaults.
func Resolve(c AppConfig, d Defaults) Effective {
	e := Effective{Port: d.Port, HealthPath: d.HealthPath, HealthTimeout: d.HealthTimeout, Release: strings.TrimSpace(c.Release),
		Processes: map[string]string{"web": ""}}
	if c.Port != 0 {
		e.Port = c.Port
	}
	if c.Healthcheck.Path != "" {
		e.HealthPath = c.Healthcheck.Path
	}
	if c.Healthcheck.Timeout != 0 {
		e.HealthTimeout = c.Healthcheck.Timeout
	}
	for k, v := range c.Processes {
		e.Processes[k] = strings.TrimSpace(v)
	}
	return e
}

// Workers are the worker processes, sorted.
func (e Effective) Workers() []string {
	var out []string
	for k := range e.Processes {
		if k != "web" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
