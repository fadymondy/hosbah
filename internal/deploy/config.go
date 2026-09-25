package deploy

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
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

	[resources]                         # memory/CPU caps (MH-574 E13); every process gets these unless overridden below
	memory = "512M"                     # 32M .. 1T; suffix M/G/T, or a bare number of MiB
	cpus = 0.5                          # 0.05 .. 256; fractional CPUs, as docker run --cpus takes them

	[resources.processes."worker:mail"] # per-process override — anything not listed here uses [resources]
	memory = "256M"
	cpus = 0.1

Precedence: what the repository says wins (config as code); the app's settings in Hosbah are the
defaults when it says nothing. Unknown keys are an error, so a typo does not silently do nothing.
[resources] itself follows the same rule: any key it sets (memory, cpus, or a process override)
replaces the app's setting for that key; a key it leaves out keeps the app's setting.

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
	Resources ResourcesConfig   `toml:"resources"`
}

// ResourcesConfig is hosbah.toml's [resources] (and each [resources.processes."<name>"]): memory and
// CPU caps, MH-574 E13. Memory is a size like "512M", "1G" or a bare number of MiB; CPUs a fraction
// like 0.5. Empty/zero means "say nothing here" (the app's own setting, or a higher-up default,
// stands) — never "unlimited"; hosbah.toml has no way to force a process back to unlimited.
type ResourcesConfig struct {
	Memory    string                     `toml:"memory"`
	CPUs      float64                    `toml:"cpus"`
	Processes map[string]ResourcesConfig `toml:"processes"`
}

// parseMemorySize reads "512M", "1G", "2T" or a bare number of MiB.
func parseMemorySize(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	mult := 1
	unit := s[len(s)-1]
	switch unit {
	case 'M', 'm':
		mult, s = 1, s[:len(s)-1]
	case 'G', 'g':
		mult, s = 1024, s[:len(s)-1]
	case 'T', 't':
		mult, s = 1024*1024, s[:len(s)-1]
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("bad memory size %q — use a number of MiB, or a size like 512M, 1G, 1T", s)
	}
	return n * mult, nil
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
	if err := checkResources(file, "resources", c.Resources); err != nil {
		return c, err
	}
	for name, rc := range c.Resources.Processes {
		if !agentproto.ValidProcess(name) {
			return c, fmt.Errorf(`%s: resources.processes %q — use "web" or "worker:<name>"`, file, name)
		}
		if err := checkResources(file, `resources.processes."`+name+`"`, rc); err != nil {
			return c, err
		}
		if len(rc.Processes) > 0 {
			return c, fmt.Errorf("%s: resources.processes.%q may not nest another processes table", file, name)
		}
	}
	return c, nil
}

// checkResources validates one [resources] table: a parseable memory size in range, cpus in range.
func checkResources(file, where string, rc ResourcesConfig) error {
	mb, err := parseMemorySize(rc.Memory)
	if err != nil {
		return fmt.Errorf("%s: %s.%s", file, where, err)
	}
	if mb != 0 && (mb < agentproto.MinMemoryMB || mb > agentproto.MaxMemoryMB) {
		return fmt.Errorf("%s: %s.memory must be %d MiB to %d MiB", file, where, agentproto.MinMemoryMB, agentproto.MaxMemoryMB)
	}
	if rc.CPUs != 0 && (rc.CPUs < agentproto.MinCPUs || rc.CPUs > agentproto.MaxCPUs) {
		return fmt.Errorf("%s: %s.cpus must be %g to %g", file, where, agentproto.MinCPUs, agentproto.MaxCPUs)
	}
	return nil
}

// Defaults are the app's own settings (from Hosbah), used where hosbah.toml says nothing.
type Defaults struct {
	Port          int
	HealthPath    string
	HealthTimeout int
	// Resources (MH-574 E13): the app's own resource limits and any per-process overrides, as saved
	// through the app settings / resources API. Zero value: unlimited.
	Resources        agentproto.ResourceLimits
	ProcessResources map[string]agentproto.ResourceLimits
}

// Effective is what a deploy actually uses.
type Effective struct {
	Port          int               `json:"port"`
	HealthPath    string            `json:"health_path"`
	HealthTimeout int               `json:"health_timeout"`
	Release       string            `json:"release,omitempty"`
	Processes     map[string]string `json:"processes"` // web always present ("" = the image's command)
	// Resources: the base limits every process gets unless ProcessResources names it.
	Resources        agentproto.ResourceLimits            `json:"resources"`
	ProcessResources map[string]agentproto.ResourceLimits `json:"process_resources,omitempty"`
}

// ResourcesFor is the limits StartParams uses for one process: its own override if any, else the
// app's base resources.
func (e Effective) ResourcesFor(process string) agentproto.ResourceLimits {
	if r, ok := e.ProcessResources[process]; ok {
		return r
	}
	return e.Resources
}

// Resolve merges hosbah.toml over the app's defaults.
func Resolve(c AppConfig, d Defaults) Effective {
	e := Effective{Port: d.Port, HealthPath: d.HealthPath, HealthTimeout: d.HealthTimeout, Release: strings.TrimSpace(c.Release),
		Processes: map[string]string{"web": ""}, Resources: d.Resources, ProcessResources: map[string]agentproto.ResourceLimits{}}
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
	for k, v := range d.ProcessResources {
		e.ProcessResources[k] = v
	}
	if mb, err := parseMemorySize(c.Resources.Memory); err == nil && mb != 0 {
		e.Resources.MemoryMB = mb
	}
	if c.Resources.CPUs != 0 {
		e.Resources.CPUs = c.Resources.CPUs
	}
	for name, rc := range c.Resources.Processes {
		base := e.ProcessResources[name]
		if base.Empty() {
			base = e.Resources
		}
		if mb, err := parseMemorySize(rc.Memory); err == nil && mb != 0 {
			base.MemoryMB = mb
		}
		if rc.CPUs != 0 {
			base.CPUs = rc.CPUs
		}
		e.ProcessResources[name] = base
	}
	if len(e.ProcessResources) == 0 {
		e.ProcessResources = nil
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
