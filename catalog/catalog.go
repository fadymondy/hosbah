// Package catalog is Hosbah's built-in, versioned catalog of recipes: "app" recipes (one pinned
// container image installed next to a site) and "stack" recipes (data that prefills the New app
// form). One YAML file per recipe under recipes/, embedded in BOTH binaries: the control plane reads
// it to offer and queue installs, the agent reads its own copy to decide what it runs — an image
// reference, a limit, a mount or a capability never travels over the wire.
//
// The format is deliberately closed: there is no field for a command, a shell, a host mount, a
// privileged flag, a network mode or a user-supplied image. What a recipe cannot say, it cannot do.
// Publishing recipes by users is a later step; a published recipe would be this same document, passed
// through this same Parse, stored per team instead of embedded.
//
// Written clean-room from each project's own public documentation.
package catalog

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

//go:embed recipes/*.yaml
var embedded embed.FS

// Kinds of recipe.
const (
	KindApp   = "app"
	KindStack = "stack"
)

// Where an env value comes from when it is neither literal nor generated: one whole connection URL,
// or one of its parts, of a managed data service on the same server as the install. Each needs the
// matching `needs` flag and the caller's choice of that service. The *_host, *_port and *_database
// parts are for images (WordPress, Odoo, …) that take a data service apart instead of one URL; only
// the credential parts (*_url, *_user, *_password) must say secret: true.
const (
	FromPostgresURL      = "postgres_url"
	FromPostgresHost     = "postgres_host"
	FromPostgresPort     = "postgres_port"
	FromPostgresHostPort = "postgres_hostport" // "host:port", for an image that takes one address field
	FromPostgresDatabase = "postgres_database"
	FromPostgresUser     = "postgres_user"
	FromPostgresPassword = "postgres_password"
	FromMySQLURL         = "mysql_url"
	FromMySQLHost        = "mysql_host"
	FromMySQLPort        = "mysql_port"
	FromMySQLHostPort    = "mysql_hostport" // "host:port", for an image that takes one address field (e.g. WordPress's WORDPRESS_DB_HOST)
	FromMySQLDatabase    = "mysql_database"
	FromMySQLUser        = "mysql_user"
	FromMySQLPassword    = "mysql_password"
	FromRedisURL         = "redis_url"
)

// fromRequirement names, for a `from`, which `needs` flag it requires.
var fromRequirement = map[string]string{
	FromPostgresURL: "postgres", FromPostgresHost: "postgres", FromPostgresPort: "postgres", FromPostgresHostPort: "postgres",
	FromPostgresDatabase: "postgres", FromPostgresUser: "postgres", FromPostgresPassword: "postgres",
	FromMySQLURL: "mysql", FromMySQLHost: "mysql", FromMySQLPort: "mysql", FromMySQLHostPort: "mysql",
	FromMySQLDatabase: "mysql", FromMySQLUser: "mysql", FromMySQLPassword: "mysql",
	FromRedisURL: "redis",
}

// fromNeedsSecret: the from values that carry a credential, which must say secret: true.
var fromNeedsSecret = map[string]bool{
	FromPostgresURL: true, FromPostgresUser: true, FromPostgresPassword: true,
	FromMySQLURL: true, FromMySQLUser: true, FromMySQLPassword: true,
	FromRedisURL: true,
}

func (n RecipeNeeds) has(what string) bool {
	switch what {
	case "postgres":
		return n.Postgres
	case "mysql":
		return n.MySQL
	case "redis":
		return n.Redis
	}
	return false
}

// Charsets of a generated value. Both are safe inside a URL, an env file and a template.
const (
	CharsetAlnum = "alnum"
	CharsetHex   = "hex"
)

// RecipeLimits of the format.
const (
	MaxEnv, MaxVolumes, MaxExposes    = 40, 5, 10
	MinGenerate, MaxGenerate          = 16, 64
	MinMemoryMB, MaxMemoryMB          = 64, 8192
	MinCPUs, MaxCPUs                  = 0.1, 4.0
	MaxValue, MaxNotes, MaxHosbahTOML = 1024, 2000, 4096
)

// AllowedCaps is every Linux capability a recipe may ask back after --cap-drop ALL. Nothing here
// reaches outside the container's own files, users and ports: no SYS_ADMIN, no NET_ADMIN, no
// NET_RAW, no SYS_PTRACE, no MKNOD.
var AllowedCaps = []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "NET_BIND_SERVICE", "SETGID", "SETUID"}

var (
	idRE            = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)
	versionRE       = regexp.MustCompile(`^[0-9]{1,4}(\.[0-9]{1,4}){0,2}$`)
	licenceRE       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+-]{0,40}$`)
	envNameRE       = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	healthRE        = regexp.MustCompile(`^/[A-Za-z0-9._/-]{0,100}$`)
	repoRE          = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
	branchRE        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)
	runtimeRE       = regexp.MustCompile(`^[a-z][a-z0-9-]{0,20}$`)
	imageRE         = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?(:[0-9]{1,5})?(/[a-z0-9]([a-z0-9._-]*[a-z0-9])?)+$`)
	tagRE           = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	extraPortNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,20}$`)
	digestRE        = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	placeRE         = regexp.MustCompile(`\{([^{}]*)\}`)
	floatingRE      = regexp.MustCompile(`^(latest|stable|edge|main|master|nightly|dev|develop)$`)
)

// Recipe is one document of the catalog. App fields are top-level; a stack's are under `stack`.
type Recipe struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Summary     string `yaml:"summary" json:"summary"`
	Description string `yaml:"description" json:"description"`
	Homepage    string `yaml:"homepage" json:"homepage"`
	Licence     string `yaml:"licence" json:"licence" doc:"SPDX identifier."`
	Kind        string `yaml:"kind" json:"kind" enum:"app,stack"`
	Version     string `yaml:"version" json:"version" doc:"The recipe's own version, not the software's."`
	Verified    bool   `yaml:"verified" json:"verified" doc:"false: written from the documentation but not yet run end to end by Hosbah."`
	HasWebUI    bool   `yaml:"-" json:"has_web_ui" doc:"Derived: an app with an HTTP port a hostname can be put in front of."`

	Image         string         `yaml:"image,omitempty" json:"image,omitempty" doc:"App: the image, pinned by tag or digest."`
	Port          int            `yaml:"port,omitempty" json:"port,omitempty" doc:"App: the container's HTTP port; 0 for a headless service."`
	Healthcheck   string         `yaml:"healthcheck,omitempty" json:"healthcheck,omitempty" doc:"App: a path that answers 2xx when it is up."`
	ExtraPort     int            `yaml:"extra_port,omitempty" json:"extra_port,omitempty" doc:"App: a second container port published on 127.0.0.1 (needs port), e.g. a separate admin console. Not attached to the install's hostname; its published port shows in the install's status."`
	ExtraPortName string         `yaml:"extra_port_name,omitempty" json:"extra_port_name,omitempty" doc:"A short label for extra_port, e.g. \"console\"."`
	Env           []RecipeEnv    `yaml:"env,omitempty" json:"env"`
	Volumes       []RecipeVolume `yaml:"volumes,omitempty" json:"volumes"`
	Limits        RecipeLimits   `yaml:"limits,omitempty" json:"limits"`
	Caps          []string       `yaml:"caps,omitempty" json:"caps" doc:"App: capabilities added back after --cap-drop ALL, from a fixed allow-list."`
	Needs         RecipeNeeds    `yaml:"needs,omitempty" json:"needs"`
	Exposes       []RecipeExpose `yaml:"exposes,omitempty" json:"exposes"`

	Stack *RecipeStack `yaml:"stack,omitempty" json:"stack,omitempty"`
}

// RecipeEnv is one variable of the install's container. Exactly one of Value, Generate and From.
type RecipeEnv struct {
	Name     string          `yaml:"name" json:"name"`
	Value    *string         `yaml:"value,omitempty" json:"value,omitempty" doc:"Literal; may reference generated variables as {NAME}."`
	Generate *RecipeGenerate `yaml:"generate,omitempty" json:"generate,omitempty"`
	From     string          `yaml:"from,omitempty" json:"from,omitempty" enum:"postgres_url,redis_url"`
	Secret   bool            `yaml:"secret,omitempty" json:"secret"`
	InjectAs string          `yaml:"inject_as,omitempty" json:"inject_as,omitempty" doc:"Also set on the linking site under this name."`
}

// RecipeGenerate asks the control plane for a random value.
type RecipeGenerate struct {
	Length  int    `yaml:"length" json:"length"`
	Charset string `yaml:"charset" json:"charset" enum:"alnum,hex"`
}

// RecipeVolume is a persistent Docker volume of the install; its Docker name is derived.
type RecipeVolume struct {
	Name      string `yaml:"name" json:"name"`
	MountPath string `yaml:"mount_path" json:"mount_path"`
}

// RecipeLimits of the container. Both are required for an app.
type RecipeLimits struct {
	MemoryMB int     `yaml:"memory_mb" json:"memory_mb"`
	CPUs     float64 `yaml:"cpus" json:"cpus"`
}

// RecipeNeeds names the managed data services the install uses. The caller chooses existing ones.
type RecipeNeeds struct {
	Postgres bool `yaml:"postgres,omitempty" json:"postgres"`
	MySQL    bool `yaml:"mysql,omitempty" json:"mysql"`
	Redis    bool `yaml:"redis,omitempty" json:"redis"`
}

// RecipeExpose is a variable set on the linking site. The template may use {host} and {port} — where the
// site reaches the install — and generated variables.
type RecipeExpose struct {
	Env      string `yaml:"env" json:"env"`
	Template string `yaml:"template" json:"template"`
}

// RecipeStack is data for the New app form. Installing a stack does nothing on a server.
type RecipeStack struct {
	Repo       string            `yaml:"repo" json:"repo" doc:"https://github.com/<owner>/<repo>"`
	Branch     string            `yaml:"branch" json:"branch"`
	Runtime    string            `yaml:"runtime" json:"runtime"`
	Env        []string          `yaml:"env,omitempty" json:"env" doc:"Suggested variable names."`
	Processes  map[string]string `yaml:"processes,omitempty" json:"processes"`
	HosbahTOML string            `yaml:"hosbah_toml,omitempty" json:"hosbah_toml" doc:"The hosbah.toml the stack suggests."`
	// LegacyTOML is the pre-rename key. A recipe may still use it (Parse moves it to HosbahTOML), and
	// the API still sends it, equal to hosbah_toml, for clients that predate the rename.
	LegacyTOML string `yaml:"cloudy_toml,omitempty" json:"cloudy_toml,omitempty" doc:"Deprecated: the same as hosbah_toml."`
	Notes      string `yaml:"notes,omitempty" json:"notes"`
}

// hasWebUI: the install has an HTTP port a proxy host can point at.
func (r Recipe) hasWebUI() bool { return r.Kind == KindApp && r.Port > 0 }

// Generated are the names of the generated variables.
func (r Recipe) Generated() []string {
	var out []string
	for _, e := range r.Env {
		if e.Generate != nil {
			out = append(out, e.Name)
		}
	}
	return out
}

// UsesAddress reports whether a template needs {host} or {port}.
func UsesAddress(tpl string) bool {
	return strings.Contains(tpl, "{host}") || strings.Contains(tpl, "{port}")
}

// Render fills a validated template. vars holds every placeholder it may use.
func Render(tpl string, vars map[string]string) (string, error) {
	var missing string
	out := placeRE.ReplaceAllStringFunc(tpl, func(m string) string {
		v, ok := vars[m[1:len(m)-1]]
		if !ok {
			missing = m
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("template placeholder %s has no value", missing)
	}
	return out, nil
}

// checkTemplate: only {allowed} placeholders, no stray braces, one line.
func checkTemplate(tpl string, allowed map[string]bool) error {
	if len(tpl) > MaxValue || strings.ContainsAny(tpl, "\r\n\x00") {
		return fmt.Errorf("a value is one line of at most %d characters", MaxValue)
	}
	for _, m := range placeRE.FindAllStringSubmatch(tpl, -1) {
		if !allowed[m[1]] {
			return fmt.Errorf("template references {%s}, which is not declared", m[1])
		}
	}
	if rest := placeRE.ReplaceAllString(tpl, ""); strings.ContainsAny(rest, "{}") {
		return errors.New("template has an unbalanced brace")
	}
	return nil
}

// ValidImage: a full reference pinned by tag or digest. A floating tag (latest, edge, …) is refused:
// what runs on a server must be what was reviewed.
func ValidImage(ref string) error {
	name, digest, hasDigest := strings.Cut(ref, "@")
	if hasDigest && !digestRE.MatchString(digest) {
		return fmt.Errorf("image %q: the digest is not sha256:<64 hex>", ref)
	}
	tag := ""
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name, tag = name[:i], name[i+1:]
	}
	switch {
	case len(ref) > 255 || !imageRE.MatchString(name):
		return fmt.Errorf("image %q is not a full reference (owner/name or registry/owner/name)", ref)
	case tag == "" && !hasDigest:
		return fmt.Errorf("image %q is not pinned: give a tag or a digest", ref)
	case tag != "" && !tagRE.MatchString(tag):
		return fmt.Errorf("image %q has a malformed tag", ref)
	case floatingRE.MatchString(strings.ToLower(tag)) && !hasDigest:
		return fmt.Errorf("image %q uses the floating tag %q: pin a version", ref, tag)
	}
	return nil
}

// Validate is the whole rule book of one recipe.
func (r Recipe) Validate() error {
	text := func(what, s string, max int) error {
		if strings.TrimSpace(s) == "" || len(s) > max || strings.ContainsRune(s, 0) {
			return fmt.Errorf("%s is required, at most %d characters", what, max)
		}
		return nil
	}
	if !idRE.MatchString(r.ID) {
		return errors.New("id must match ^[a-z][a-z0-9-]{1,40}$")
	}
	for _, c := range []struct {
		what, s string
		max     int
	}{{"name", r.Name, 60}, {"summary", r.Summary, 160}, {"description", r.Description, 2000}} {
		if err := text(c.what, c.s, c.max); err != nil {
			return err
		}
	}
	if u, err := url.Parse(r.Homepage); err != nil || u.Scheme != "https" || u.Host == "" || len(r.Homepage) > 200 {
		return errors.New("homepage must be an https URL")
	}
	if !licenceRE.MatchString(r.Licence) {
		return errors.New("licence must be an SPDX identifier")
	}
	if !versionRE.MatchString(r.Version) {
		return errors.New("version must look like 1, 1.2 or 1.2.3")
	}
	switch r.Kind {
	case KindApp:
		if r.Stack != nil {
			return errors.New("an app recipe has no stack block")
		}
		return r.validateApp()
	case KindStack:
		if r.Image != "" || r.Port != 0 || r.Healthcheck != "" || r.ExtraPort != 0 || r.ExtraPortName != "" ||
			len(r.Env)+len(r.Volumes)+len(r.Caps)+len(r.Exposes) > 0 || r.Limits != (RecipeLimits{}) || r.Needs != (RecipeNeeds{}) {
			return errors.New("a stack recipe has no app fields (image, port, env, volumes, limits, caps, needs, exposes)")
		}
		if r.Stack == nil {
			return errors.New("a stack recipe needs its stack block")
		}
		return r.Stack.validate()
	}
	return errors.New(`kind must be "app" or "stack"`)
}

func (r Recipe) validateApp() error {
	if err := ValidImage(r.Image); err != nil {
		return err
	}
	if r.Port < 0 || r.Port > 65535 {
		return errors.New("port must be 1 to 65535 (or absent for a headless service)")
	}
	if r.Healthcheck != "" && (r.Port == 0 || !healthRE.MatchString(r.Healthcheck)) {
		return errors.New("healthcheck is a path like /healthz and needs a port")
	}
	switch {
	case r.ExtraPort != 0 && r.Port == 0:
		return errors.New("extra_port needs a primary port")
	case r.ExtraPort != 0 && (r.ExtraPort < 1 || r.ExtraPort > 65535 || r.ExtraPort == r.Port):
		return errors.New("extra_port must be 1 to 65535 and differ from port")
	case r.ExtraPort != 0 && !extraPortNameRE.MatchString(r.ExtraPortName):
		return errors.New("extra_port_name is required, a short lowercase label")
	case r.ExtraPort == 0 && r.ExtraPortName != "":
		return errors.New("extra_port_name without extra_port")
	}
	if r.Limits.MemoryMB < MinMemoryMB || r.Limits.MemoryMB > MaxMemoryMB {
		return fmt.Errorf("limits.memory_mb is required, %d to %d", MinMemoryMB, MaxMemoryMB)
	}
	if !(r.Limits.CPUs >= MinCPUs && r.Limits.CPUs <= MaxCPUs) {
		return fmt.Errorf("limits.cpus is required, %g to %g", MinCPUs, MaxCPUs)
	}
	seenCap := map[string]bool{}
	for _, c := range r.Caps {
		if !contains(AllowedCaps, c) {
			return fmt.Errorf("capability %q is not allowed (allowed: %s)", c, strings.Join(AllowedCaps, ", "))
		}
		if seenCap[c] {
			return fmt.Errorf("capability %q is listed twice", c)
		}
		seenCap[c] = true
	}
	if len(r.Env) > MaxEnv || len(r.Volumes) > MaxVolumes || len(r.Exposes) > MaxExposes {
		return fmt.Errorf("at most %d env, %d volumes and %d exposes", MaxEnv, MaxVolumes, MaxExposes)
	}

	generated, names, injected := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range r.Env {
		if e.Generate != nil {
			generated[e.Name] = true
		}
	}
	inject := func(n string) error {
		if !envNameRE.MatchString(n) || n == "PORT" {
			return fmt.Errorf("%q cannot be set on a site (upper-case letters, digits and _; never PORT)", n)
		}
		if injected[n] {
			return fmt.Errorf("%s would be set on the site twice", n)
		}
		injected[n] = true
		return nil
	}
	for _, e := range r.Env {
		if !envNameRE.MatchString(e.Name) {
			return fmt.Errorf("env name %q must match ^[A-Z_][A-Z0-9_]{0,63}$", e.Name)
		}
		if names[e.Name] {
			return fmt.Errorf("env %s is declared twice", e.Name)
		}
		names[e.Name] = true
		sources := 0
		for _, set := range []bool{e.Value != nil, e.Generate != nil, e.From != ""} {
			if set {
				sources++
			}
		}
		if sources != 1 {
			return fmt.Errorf("env %s needs exactly one of value, generate and from", e.Name)
		}
		switch {
		case e.Value != nil:
			if err := checkTemplate(*e.Value, generated); err != nil {
				return fmt.Errorf("env %s: %w", e.Name, err)
			}
		case e.Generate != nil:
			g := e.Generate
			if g.Length < MinGenerate || g.Length > MaxGenerate || (g.Charset != CharsetAlnum && g.Charset != CharsetHex) {
				return fmt.Errorf("env %s: generate.length is %d to %d and charset is alnum or hex", e.Name, MinGenerate, MaxGenerate)
			}
			if !e.Secret {
				return fmt.Errorf("env %s is generated, so it must say secret: true", e.Name)
			}
		case e.From != "":
			need, known := fromRequirement[e.From]
			if !known || !r.Needs.has(need) {
				return fmt.Errorf("env %s: from %q is unknown, or the recipe does not declare the matching needs", e.Name, e.From)
			}
			if fromNeedsSecret[e.From] && !e.Secret {
				return fmt.Errorf("env %s carries a credential, so it must say secret: true", e.Name)
			}
		default:
			return fmt.Errorf("env %s needs exactly one of value, generate and from", e.Name)
		}
		if e.InjectAs != "" {
			if e.From != "" {
				return fmt.Errorf("env %s: a data service's connection string is attached to a site through the service, not a recipe", e.Name)
			}
			if err := inject(e.InjectAs); err != nil {
				return err
			}
		}
	}

	var mounts []string
	slugs := map[string]bool{}
	for _, v := range r.Volumes {
		if !agentproto.ValidVolumeSlug(v.Name) || slugs[v.Name] {
			return fmt.Errorf("volume name %q must be unique and match ^[a-z][a-z0-9-]{0,30}$", v.Name)
		}
		slugs[v.Name] = true
		clean, err := agentproto.CleanMountPath(v.MountPath)
		if err != nil {
			return fmt.Errorf("volume %s: %w", v.Name, err)
		}
		if clean != v.MountPath {
			return fmt.Errorf("volume %s: write the mount path normalised (%s)", v.Name, clean)
		}
		for _, o := range mounts {
			if agentproto.PathWithin(clean, o) || agentproto.PathWithin(o, clean) {
				return fmt.Errorf("mount paths %s and %s overlap", o, clean)
			}
		}
		mounts = append(mounts, clean)
	}

	allowed := map[string]bool{"host": true, "port": true}
	for n := range generated {
		allowed[n] = true
	}
	for _, x := range r.Exposes {
		if err := inject(x.Env); err != nil {
			return err
		}
		if strings.TrimSpace(x.Template) == "" {
			return fmt.Errorf("exposes %s has an empty template", x.Env)
		}
		if err := checkTemplate(x.Template, allowed); err != nil {
			return fmt.Errorf("exposes %s: %w", x.Env, err)
		}
		if UsesAddress(x.Template) && r.Port == 0 {
			return fmt.Errorf("exposes %s uses {host} or {port}, but the recipe has no port", x.Env)
		}
	}
	return nil
}

func (s RecipeStack) validate() error {
	if !repoRE.MatchString(s.Repo) || strings.HasSuffix(s.Repo, ".git") {
		return errors.New("stack.repo must be https://github.com/<owner>/<repo>")
	}
	if !branchRE.MatchString(s.Branch) || strings.Contains(s.Branch, "..") {
		return errors.New("stack.branch is required")
	}
	if !runtimeRE.MatchString(s.Runtime) {
		return errors.New("stack.runtime is a short lowercase hint (php, node, go, …)")
	}
	seen := map[string]bool{}
	for _, n := range s.Env {
		if !envNameRE.MatchString(n) || n == "PORT" || seen[n] {
			return fmt.Errorf("stack.env %q must be a unique variable name (never PORT)", n)
		}
		seen[n] = true
	}
	if len(s.Env) > MaxEnv || len(s.Processes) > 10 {
		return errors.New("stack: too many env names or processes")
	}
	for name, cmd := range s.Processes {
		if !agentproto.ValidProcess(name) || strings.TrimSpace(cmd) == "" || len(cmd) > 500 || strings.ContainsAny(cmd, "\r\n\x00") {
			return fmt.Errorf(`stack.processes %q: use "web" or "worker:<name>" and a one-line command`, name)
		}
	}
	if s.HosbahTOML != "" && s.LegacyTOML != "" && s.HosbahTOML != s.LegacyTOML {
		return errors.New("stack: hosbah_toml and cloudy_toml differ; keep hosbah_toml only")
	}
	if len(s.HosbahTOML) > MaxHosbahTOML || len(s.LegacyTOML) > MaxHosbahTOML || len(s.Notes) > MaxNotes {
		return errors.New("stack: hosbah_toml or notes is too long")
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Parse reads one recipe document, strictly: an unknown field is an error, and so is a second document.
func Parse(raw []byte) (Recipe, error) {
	var r Recipe
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return Recipe{}, err
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return Recipe{}, errors.New("one recipe per file")
	}
	if err := r.Validate(); err != nil {
		return Recipe{}, err
	}
	if r.Stack != nil {
		if r.Stack.HosbahTOML == "" {
			r.Stack.HosbahTOML = r.Stack.LegacyTOML
		}
		r.Stack.LegacyTOML = r.Stack.HosbahTOML
	}
	r.HasWebUI = r.hasWebUI()
	return r, nil
}

// Catalog is a loaded, validated set of recipes.
type Catalog struct {
	byID map[string]Recipe
	ids  []string
}

// LoadFS reads every *.yaml of dir. One bad recipe fails the whole load; a file is named after its id.
func LoadFS(fsys fs.FS, dir string) (*Catalog, error) {
	files, err := fs.Glob(fsys, path.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	c := &Catalog{byID: map[string]Recipe{}}
	for _, f := range files {
		raw, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		r, err := Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("recipe %s: %w", path.Base(f), err)
		}
		if _, dup := c.byID[r.ID]; dup {
			return nil, fmt.Errorf("recipe %s: id %q is already in the catalog", path.Base(f), r.ID)
		}
		if path.Base(f) != r.ID+".yaml" {
			return nil, fmt.Errorf("recipe %s: the file of recipe %q is named %s.yaml", path.Base(f), r.ID, r.ID)
		}
		c.byID[r.ID] = r
		c.ids = append(c.ids, r.ID)
	}
	sort.Strings(c.ids)
	return c, nil
}

var (
	once    sync.Once
	builtin *Catalog
	loadErr error
)

// Builtin is the embedded catalog, loaded and validated once. Both binaries call it at startup and
// refuse to start on an error; the whole-catalog unit test makes sure that never reaches production.
func Builtin() (*Catalog, error) {
	once.Do(func() { builtin, loadErr = LoadFS(embedded, "recipes") })
	return builtin, loadErr
}

// Get is one recipe by id.
func (c *Catalog) Get(id string) (Recipe, bool) {
	r, ok := c.byID[id]
	return r, ok
}

// List filters by kind ("" = all) and a case-insensitive query over id, name and summary; sorted by id.
func (c *Catalog) List(kind, query string) []Recipe {
	query = strings.ToLower(strings.TrimSpace(query))
	out := []Recipe{}
	for _, id := range c.ids {
		r := c.byID[id]
		if kind != "" && r.Kind != kind {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(r.ID+"\n"+r.Name+"\n"+r.Summary), query) {
			continue
		}
		out = append(out, r)
	}
	return out
}
