package deploy

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/fadymondy/cloudy/internal/agentproto"
)

/*
cloudy.toml — optional, at the repository root. Everything in it is optional too:

	port = 8080                         # the port the web process listens on (PORT is set to it)
	release = "php artisan migrate"     # runs once per deploy in a one-off container, before traffic moves

	[healthcheck]
	path = "/health"                    # must answer 2xx before the new release takes traffic
	timeout = 60                        # seconds

	[processes]
	web = "node server.js"              # overrides the image's start command
	"worker:queue" = "node worker.js"   # workers: no port, restarted with every deploy

Precedence: what the repository says wins (config as code); the app's settings in Cloudy are the
defaults when it says nothing. Unknown keys are an error, so a typo does not silently do nothing.
*/

// AppConfig is a parsed cloudy.toml.
type AppConfig struct {
	Port        int    `toml:"port"`
	Release     string `toml:"release"`
	Healthcheck struct {
		Path    string `toml:"path"`
		Timeout int    `toml:"timeout"`
	} `toml:"healthcheck"`
	Processes map[string]string `toml:"processes"`
}

// ParseConfig reads cloudy.toml. An empty file (or none) is a zero AppConfig.
func ParseConfig(raw string) (AppConfig, error) {
	var c AppConfig
	if strings.TrimSpace(raw) == "" {
		return c, nil
	}
	md, err := toml.Decode(raw, &c)
	if err != nil {
		return c, fmt.Errorf("cloudy.toml: %w", err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return c, fmt.Errorf("cloudy.toml: unknown key(s) %s", strings.Join(keys, ", "))
	}
	if c.Port != 0 && (c.Port < 1 || c.Port > 65535) {
		return c, errors.New("cloudy.toml: port must be between 1 and 65535")
	}
	if c.Healthcheck.Path != "" && !strings.HasPrefix(c.Healthcheck.Path, "/") {
		return c, errors.New("cloudy.toml: healthcheck.path must start with /")
	}
	if c.Healthcheck.Timeout != 0 && (c.Healthcheck.Timeout < 5 || c.Healthcheck.Timeout > 600) {
		return c, errors.New("cloudy.toml: healthcheck.timeout must be between 5 and 600 seconds")
	}
	for name, cmd := range c.Processes {
		if !agentproto.ValidProcess(name) {
			return c, fmt.Errorf(`cloudy.toml: process %q — use "web" or "worker:<name>" (lowercase, digits, - or _)`, name)
		}
		if strings.TrimSpace(cmd) == "" {
			return c, fmt.Errorf("cloudy.toml: process %q has an empty command", name)
		}
	}
	if len(c.Release) > 4096 {
		return c, errors.New("cloudy.toml: the release command is too long")
	}
	return c, nil
}

// Defaults are the app's own settings (from Cloudy), used where cloudy.toml says nothing.
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

// Resolve merges cloudy.toml over the app's defaults.
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
