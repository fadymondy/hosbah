// Package sitetemplate is MH-576 B11: a site template is a declarative YAML document that scaffolds
// a ready-to-deploy app — the stack driver, a repository to build, env defaults (literal, generated
// or referencing a data service the caller already has running), and the data services it needs. A
// built-in template ships in this package (embedded, read-only, one per stack driver); a team may
// also save its own, stored per team (site_templates, RLS). Applying a template reuses the same
// creation code paths as the New app form: deploy.Service.CreateApp for the app itself and
// deploy.Service.SetEnv for its variables — this package only parses, validates and renders.
//
// This is deliberately not the app catalog (package catalog): a catalog recipe installs a pinned
// third-party container image next to a site; a site template scaffolds YOUR OWN app from a
// repository you own, on a build (Railpack or Dockerfile) Hosbah already knows how to run.
package sitetemplate

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// MaxYAMLBytes bounds a template document (saved or previewed). Generous for a deploy config plus
// notes, small enough that no single row can bloat the table.
const MaxYAMLBytes = 64 * 1024

const (
	maxName        = 80
	maxDescription = 2000
	maxNotes       = 4000
	maxHosbahTOML  = 4096
	maxEnvName     = 64
	maxEnvValue    = 4000
	maxEnv         = 40
	maxBranch      = 200
)

// From values an env entry may reference: a whole connection URL of a data service the caller
// already has running on the target server, chosen at apply time — never created by a template.
const (
	FromPostgresURL = "postgres_url"
	FromMySQLURL    = "mysql_url"
	FromRedisURL    = "redis_url"
)

var fromKind = map[string]string{FromPostgresURL: "postgres", FromMySQLURL: "mysql", FromRedisURL: "redis"}

// Builds a template's driver picks.
const (
	BuildRailpack   = "railpack"
	BuildDockerfile = "dockerfile"
)

var (
	nameRE  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,79}$`)
	envNoRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	placeRE = regexp.MustCompile(`\{\{random:(\d+)\}\}`)
)

// Driver is a small, typed registry entry a template references by name: what a stack looks like
// before any repository-specific choice is made. Adding a driver needs a code change (it is not
// data a team can invent), exactly like the app catalog's recipes are code-reviewed before they ship.
type Driver struct {
	Name          string `json:"name"`
	Label         string `json:"label"`
	Build         string `json:"build" doc:"railpack or dockerfile"`
	Preset        string `json:"preset" doc:"The deploy preset this driver's Railpack build steps come from (internal/deploy PresetXxx)."`
	Runtime       string `json:"runtime"`
	DefaultPort   int    `json:"default_port"`
	DefaultHealth string `json:"default_health_path"`
	Summary       string `json:"summary"`
}

// Drivers is the whole registry, in a stable order.
var Drivers = []Driver{
	{Name: "laravel", Label: "Laravel", Build: BuildRailpack, Preset: "laravel", Runtime: "php",
		DefaultPort: 8080, DefaultHealth: "/up", Summary: "The Laravel application skeleton, migrated on release."},
	{Name: "nextjs", Label: "Next.js", Build: BuildRailpack, Preset: "next", Runtime: "node",
		DefaultPort: 3000, DefaultHealth: "/", Summary: "A Next.js app, built and started with npm."},
	{Name: "node-api", Label: "Node.js API", Build: BuildRailpack, Preset: "node", Runtime: "node",
		DefaultPort: 8080, DefaultHealth: "/health", Summary: "A plain Node.js HTTP service."},
	{Name: "static-site", Label: "Static site", Build: BuildRailpack, Preset: "static", Runtime: "static",
		DefaultPort: 80, DefaultHealth: "/", Summary: "Pre-built static files, served as-is."},
	{Name: "django", Label: "Django", Build: BuildRailpack, Preset: "python", Runtime: "python",
		DefaultPort: 8000, DefaultHealth: "/", Summary: "A Django project, served behind its own WSGI/ASGI server."},
	{Name: "wordpress-docker", Label: "WordPress (Dockerfile)", Build: BuildDockerfile, Preset: "dockerfile", Runtime: "php",
		DefaultPort: 80, DefaultHealth: "/", Summary: "A WordPress checkout with its own Dockerfile."},
}

// DriverByName looks a driver up by name.
func DriverByName(name string) (Driver, bool) {
	for _, d := range Drivers {
		if d.Name == name {
			return d, true
		}
	}
	return Driver{}, false
}

// Needs names the managed data services a template's app expects — the caller picks an existing,
// running service of that kind on the target server; a template never provisions one itself.
type Needs struct {
	Postgres bool `yaml:"postgres,omitempty" json:"postgres"`
	MySQL    bool `yaml:"mysql,omitempty" json:"mysql"`
	Redis    bool `yaml:"redis,omitempty" json:"redis"`
}

func (n Needs) has(kind string) bool {
	switch kind {
	case "postgres":
		return n.Postgres
	case "mysql":
		return n.MySQL
	case "redis":
		return n.Redis
	}
	return false
}

// Generate asks for a random value at render time, like catalog.RecipeGenerate.
type Generate struct {
	Length  int    `yaml:"length" json:"length"`
	Charset string `yaml:"charset" json:"charset" enum:"alnum,hex"`
}

// EnvEntry is one variable of the app a template creates. Exactly one of Value, Generate and From;
// a Value may also embed {{random:N}} placeholders, generated once at render time. secret: true env
// may not carry a literal Value — only generate or from — so a saved template never holds a secret
// in plain text.
type EnvEntry struct {
	Name     string    `yaml:"name" json:"name"`
	Value    string    `yaml:"value,omitempty" json:"value,omitempty"`
	Generate *Generate `yaml:"generate,omitempty" json:"generate,omitempty"`
	From     string    `yaml:"from,omitempty" json:"from,omitempty" enum:"postgres_url,mysql_url,redis_url"`
	Secret   bool      `yaml:"secret,omitempty" json:"secret"`
}

// Template is one parsed, validated site template document.
type Template struct {
	Name        string     `yaml:"name" json:"name"`
	Description string     `yaml:"description,omitempty" json:"description"`
	Driver      string     `yaml:"driver" json:"driver"`
	RepoURL     string     `yaml:"repo_url" json:"repo_url"`
	Branch      string     `yaml:"branch,omitempty" json:"branch,omitempty"`
	Port        int        `yaml:"port,omitempty" json:"port,omitempty"`
	HealthPath  string     `yaml:"health_path,omitempty" json:"health_path,omitempty"`
	Needs       Needs      `yaml:"needs,omitempty" json:"needs"`
	Env         []EnvEntry `yaml:"env,omitempty" json:"env"`
	HosbahTOML  string     `yaml:"hosbah_toml,omitempty" json:"hosbah_toml,omitempty" doc:"A suggested hosbah.toml for the repository; never applied automatically."`
	Notes       string     `yaml:"notes,omitempty" json:"notes,omitempty"`
}

// Parse decodes and validates a template document. Unknown keys are rejected (strict decoding) and
// the source is size-bounded before it is even parsed.
func Parse(raw []byte) (Template, error) {
	if len(raw) == 0 {
		return Template{}, errors.New("the template is empty")
	}
	if len(raw) > MaxYAMLBytes {
		return Template{}, fmt.Errorf("the template is at most %d bytes", MaxYAMLBytes)
	}
	if bytes.ContainsRune(raw, 0) {
		return Template{}, errors.New("the template has a NUL byte")
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var t Template
	if err := dec.Decode(&t); err != nil {
		return Template{}, fmt.Errorf("invalid YAML: %w", err)
	}
	var extra yaml.Node
	if dec.Decode(&extra) == nil {
		return Template{}, errors.New("a template is one YAML document")
	}
	if err := t.Validate(); err != nil {
		return Template{}, err
	}
	return t, nil
}

// Validate is the whole rule book of one template.
func (t Template) Validate() error {
	if !nameRE.MatchString(t.Name) {
		return errors.New("name must match ^[a-z][a-z0-9-]{0,79}$")
	}
	if len(t.Description) > maxDescription {
		return fmt.Errorf("description is at most %d characters", maxDescription)
	}
	if _, ok := DriverByName(t.Driver); !ok {
		names := make([]string, len(Drivers))
		for i, d := range Drivers {
			names[i] = d.Name
		}
		return fmt.Errorf("driver must be one of: %s", strings.Join(names, ", "))
	}
	if err := agentproto.CheckRepoURL(t.RepoURL); err != nil {
		return fmt.Errorf("repo_url: %w", err)
	}
	if len(t.Branch) > maxBranch {
		return fmt.Errorf("branch is at most %d characters", maxBranch)
	}
	if t.Port < 0 || t.Port > 65535 {
		return errors.New("port must be 1 to 65535")
	}
	if t.HealthPath != "" && !strings.HasPrefix(t.HealthPath, "/") {
		return errors.New("health_path must start with /")
	}
	if len(t.Env) > maxEnv {
		return fmt.Errorf("at most %d env entries", maxEnv)
	}
	if len(t.HosbahTOML) > maxHosbahTOML {
		return fmt.Errorf("hosbah_toml is at most %d characters", maxHosbahTOML)
	}
	if len(t.Notes) > maxNotes {
		return fmt.Errorf("notes is at most %d characters", maxNotes)
	}
	seen := map[string]bool{}
	for _, e := range t.Env {
		if !envNoRE.MatchString(e.Name) || len(e.Name) > maxEnvName {
			return fmt.Errorf("env name %q must match ^[A-Z_][A-Z0-9_]{0,63}$", e.Name)
		}
		if seen[e.Name] {
			return fmt.Errorf("env %s is declared twice", e.Name)
		}
		seen[e.Name] = true
		sources := 0
		if e.Value != "" {
			sources++
		}
		if e.Generate != nil {
			sources++
			if e.Generate.Length < 8 || e.Generate.Length > 128 {
				return fmt.Errorf("env %s: generate.length must be 8 to 128", e.Name)
			}
			if e.Generate.Charset != "alnum" && e.Generate.Charset != "hex" {
				return fmt.Errorf("env %s: generate.charset must be alnum or hex", e.Name)
			}
		}
		if e.From != "" {
			sources++
			kind, ok := fromKind[e.From]
			if !ok {
				return fmt.Errorf("env %s: from must be one of postgres_url, mysql_url, redis_url", e.Name)
			}
			if !t.Needs.has(kind) {
				return fmt.Errorf("env %s references %s, but needs.%s is not set", e.Name, e.From, kind)
			}
		}
		if sources != 1 {
			return fmt.Errorf("env %s: exactly one of value, generate and from", e.Name)
		}
		if e.Secret && e.Value != "" {
			return fmt.Errorf("env %s: secret env may not carry a literal value — use generate or from so no secret is ever stored in plain text", e.Name)
		}
		if len(e.Value) > maxEnvValue {
			return fmt.Errorf("env %s: value is at most %d characters", e.Name, maxEnvValue)
		}
	}
	return nil
}

// RenderedEnv is one variable of the app a render produced.
type RenderedEnv struct {
	Name   string
	Value  string
	Secret bool
}

// Rendered is what applying a template computes, before anything is created.
type Rendered struct {
	Driver     Driver
	RepoURL    string
	Branch     string
	Port       int
	HealthPath string
	Env        []RenderedEnv
	HosbahTOML string
	Notes      string
}

// ServiceURLs is the caller's resolved connection strings, one per kind the template's needs ask
// for — never created by Render, only substituted into `from` env entries.
type ServiceURLs struct {
	Postgres, MySQL, Redis string
}

// Render fills every placeholder: {{random:N}} inside a literal value, a generate block, or a
// `from` reference into the caller-supplied service URL. Missing a service URL the template needs
// is an error — the caller resolves and validates those refs before calling Render.
func (t Template) Render(urls ServiceURLs) (Rendered, error) {
	driver, ok := DriverByName(t.Driver)
	if !ok {
		return Rendered{}, fmt.Errorf("unknown driver %q", t.Driver)
	}
	out := Rendered{Driver: driver, RepoURL: t.RepoURL, Branch: t.Branch, Port: t.Port, HealthPath: t.HealthPath, HosbahTOML: t.HosbahTOML, Notes: t.Notes}
	if out.Branch == "" {
		out.Branch = "main"
	}
	if out.Port == 0 {
		out.Port = driver.DefaultPort
	}
	if out.HealthPath == "" {
		out.HealthPath = driver.DefaultHealth
	}
	for _, e := range t.Env {
		v := e.Value
		switch {
		case e.Generate != nil:
			v = randomValue(*e.Generate)
		case e.From != "":
			switch e.From {
			case FromPostgresURL:
				v = urls.Postgres
			case FromMySQLURL:
				v = urls.MySQL
			case FromRedisURL:
				v = urls.Redis
			}
			if v == "" {
				return Rendered{}, fmt.Errorf("env %s needs %s, but no matching service was given", e.Name, e.From)
			}
		default:
			var err error
			v, err = fillRandomPlaceholders(v)
			if err != nil {
				return Rendered{}, fmt.Errorf("env %s: %w", e.Name, err)
			}
		}
		out.Env = append(out.Env, RenderedEnv{Name: e.Name, Value: v, Secret: e.Secret})
	}
	return out, nil
}

// fillRandomPlaceholders replaces every {{random:N}} in a literal value with a freshly generated
// alnum string of length N (8..128).
func fillRandomPlaceholders(s string) (string, error) {
	var outerErr error
	out := placeRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := placeRE.FindStringSubmatch(m)
		n := 0
		fmt.Sscanf(sub[1], "%d", &n)
		if n < 8 || n > 128 {
			outerErr = fmt.Errorf("{{random:%s}} must be 8 to 128", sub[1])
			return m
		}
		return randFrom(alnumAlphabet, n)
	})
	if outerErr != nil {
		return "", outerErr
	}
	return out, nil
}

func randomValue(g Generate) string {
	if g.Charset == "hex" {
		return randFrom("0123456789abcdef", g.Length)
	}
	return randFrom(alnumAlphabet, g.Length)
}

const alnumAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func randFrom(alphabet string, n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// ToYAML marshals a Template back to its document form (used by Export, which builds one from an
// existing app rather than round-tripping a saved document).
func (t Template) ToYAML() ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(t); err != nil {
		return nil, err
	}
	_ = enc.Close()
	return b.Bytes(), nil
}

// SortedDriverNames returns every driver's name, alphabetically — for a stable enum in the API docs.
func SortedDriverNames() []string {
	names := make([]string, len(Drivers))
	for i, d := range Drivers {
		names[i] = d.Name
	}
	sort.Strings(names)
	return names
}
