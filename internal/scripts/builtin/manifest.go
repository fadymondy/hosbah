// Package builtin is Hosbah's read-only catalogue of one-click host software installs (MH-576 D1):
// one shell script per entry, embedded in the control-plane binary, for Debian/Ubuntu hosts. A
// built-in is named, not editable, and its params are validated here against a per-script allow
// list before anything reaches the agent — see (Script).ValidateParams. The agent never templates
// these scripts: the body sqlc-free text goes over the wire as agentproto.ScriptRunParams.Body
// exactly as embedded here, and a param becomes an environment variable the script reads (HOSBAH_
// PARAM_<NAME>), never text interpolated into the body.
//
// Written clean-room from each project's own public installation docs (see the comment atop every
// .sh file); nothing here is copied from Eddy or any other AGPL project.
package builtin

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed *.sh
var scripts embed.FS

// Param is one validated input of a built-in script, exposed to the caller as an env var named
// HOSBAH_PARAM_<NAME> (upper-cased). Enum, when set, is the only allow list this param accepts;
// Default is used when the caller omits it.
type Param struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"`
	Enum    []string `json:"enum,omitempty"`
	Default string   `json:"default,omitempty"`
}

// Script is one built-in of the catalogue.
type Script struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	Icon           string  `json:"icon" doc:"A grid icon name; the web app owns the actual set."`
	File           string  `json:"-"`
	RunAs          string  `json:"run_as"`
	TimeoutSeconds int     `json:"timeout_seconds"`
	Params         []Param `json:"params,omitempty"`
}

// Catalogue is every built-in, in a fixed display order (not alphabetical: an install order a
// reader would recognise — a web server, then a language and its package manager, then data
// services, then extras).
var Catalogue = []Script{
	{ID: "caddy", Name: "Caddy", Description: "Automatic-HTTPS reverse proxy and web server.", Icon: "server",
		File: "caddy.sh", RunAs: "root", TimeoutSeconds: 300},
	{ID: "php", Name: "PHP", Description: "PHP-FPM with a common extension set, from ondrej/sury.", Icon: "code",
		File: "php.sh", RunAs: "root", TimeoutSeconds: 300, Params: []Param{
			{Name: "version", Label: "PHP version", Enum: []string{"8.1", "8.2", "8.3", "8.4"}, Default: "8.3"},
		}},
	{ID: "composer", Name: "Composer", Description: "PHP's dependency manager, installed system-wide.", Icon: "package",
		File: "composer.sh", RunAs: "root", TimeoutSeconds: 180},
	{ID: "nodejs", Name: "Node.js", Description: "Node.js and npm, from NodeSource.", Icon: "code",
		File: "nodejs.sh", RunAs: "root", TimeoutSeconds: 300, Params: []Param{
			{Name: "version", Label: "Node.js major version", Enum: []string{"18", "20", "22", "24"}, Default: "22"},
		}},
	{ID: "mysql", Name: "MySQL (MariaDB)", Description: "MariaDB server, a drop-in MySQL replacement.", Icon: "database",
		File: "mysql.sh", RunAs: "root", TimeoutSeconds: 300},
	{ID: "redis", Name: "Redis", Description: "Redis server, listening on localhost.", Icon: "database",
		File: "redis.sh", RunAs: "root", TimeoutSeconds: 180},
	{ID: "phpmyadmin", Name: "phpMyAdmin", Description: "A web UI for MySQL/MariaDB, unpacked for your own web server.", Icon: "table",
		File: "phpmyadmin.sh", RunAs: "root", TimeoutSeconds: 180, Params: []Param{
			{Name: "version", Label: "phpMyAdmin version", Enum: []string{"5.2.1", "5.2.0"}, Default: "5.2.1"},
		}},
	{ID: "chromium", Name: "Chromium (headless)", Description: "Chromium plus the shared libraries headless automation needs.", Icon: "globe",
		File: "chromium.sh", RunAs: "root", TimeoutSeconds: 300},
}

var byID map[string]Script

func init() {
	byID = make(map[string]Script, len(Catalogue))
	for _, s := range Catalogue {
		byID[s.ID] = s
	}
}

// Get returns one built-in by id.
func Get(id string) (Script, bool) {
	s, ok := byID[id]
	return s, ok
}

// List returns the catalogue sorted by id (a stable order for the API, independent of Catalogue's
// display order).
func List() []Script {
	out := append([]Script(nil), Catalogue...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Body reads the embedded shell text of a built-in.
func (s Script) Body() (string, error) {
	b, err := scripts.ReadFile(s.File)
	if err != nil {
		return "", fmt.Errorf("built-in script %q: %w", s.ID, err)
	}
	return string(b), nil
}

// ValidateParams checks a caller's params against this script's declared allow list: every given
// name must be declared, and if the param has an Enum the value must be a member of it. Returns the
// full set of args to run with (an env var per param, defaults filled in for what the caller left
// out) — this, and never the caller's map directly, is what agentproto.ScriptRunParams.Args holds.
func (s Script) ValidateParams(given map[string]string) (map[string]string, error) {
	declared := make(map[string]Param, len(s.Params))
	for _, p := range s.Params {
		declared[p.Name] = p
	}
	for name := range given {
		if _, ok := declared[name]; !ok {
			return nil, fmt.Errorf("%s has no param %q", s.ID, name)
		}
	}
	out := make(map[string]string, len(s.Params))
	for _, p := range s.Params {
		v, ok := given[p.Name]
		if !ok || v == "" {
			v = p.Default
		}
		if len(p.Enum) > 0 && !contains(p.Enum, v) {
			return nil, fmt.Errorf("%s: param %q must be one of %s", s.ID, p.Name, strings.Join(p.Enum, ", "))
		}
		out["HOSBAH_PARAM_"+strings.ToUpper(p.Name)] = v
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
