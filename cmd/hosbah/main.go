// Command cloudy is Hosbah's CLI (Windows, macOS, Linux). It speaks to a team's MCP endpoint with a
// team token through the Go SDK (pkg/hosbah), so every tool the team token may call is a command:
//
//	hosbah login --url https://cloudy-app.fadymondy.com --token cld_…
//	hosbah tools                          # what this token can do
//	hosbah help deploy_app                # a tool's arguments
//	hosbah deploy shop --wait             # shortcut for deploy_app
//	cloudy add_proxy_host server=edge hostname=nas.example.com upstream=10.0.0.5:5000
//
// CLOUDY_URL and CLOUDY_TOKEN override the saved login (CI).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/fadymondy/hosbah/pkg/hosbah"
)

var version = "dev"

type config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hosbah", "config.json"), nil
}

func loadConfig() config {
	var c config
	if p, err := configPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &c)
		}
	}
	if v := os.Getenv("CLOUDY_URL"); v != "" {
		c.URL = v
	}
	if v := os.Getenv("CLOUDY_TOKEN"); v != "" {
		c.Token = v
	}
	return c
}

func saveConfig(c config) (string, error) {
	p, err := configPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return p, os.WriteFile(p, b, 0o600)
}

// shortcuts map friendly commands to tools; the first positional argument fills the named key.
var shortcuts = map[string]struct{ tool, first string }{
	"whoami":  {"whoami", ""},
	"servers": {"list_servers", ""},
	"server":  {"get_server", "server"},
	"apps":    {"list_apps", ""},
	"app":     {"get_app", "app"},
	"deploy":  {"deploy_app", "app"},
	"logs":    {"deploy_logs", "app"},
	"env":     {"list_env", "app"},
	"proxies": {"list_proxy_hosts", ""},
	"dbs":     {"list_databases", ""},
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, argv []string, out io.Writer) error {
	if len(argv) == 0 || argv[0] == "-h" || argv[0] == "--help" {
		usage(out)
		return nil
	}
	cmd, rest := argv[0], argv[1:]
	switch cmd {
	case "version", "--version":
		fmt.Fprintln(out, "cloudy", version)
		return nil
	case "login":
		flags := parseArgs(rest)
		c := loadConfig()
		if v, ok := flags["url"].(string); ok {
			c.URL = v
		}
		if v, ok := flags["token"].(string); ok {
			c.Token = v
		}
		if c.URL == "" || c.Token == "" {
			return errors.New("usage: hosbah login --url https://cloudy.example.com --token cld_…")
		}
		who, err := hosbah.New(c.URL, c.Token).Whoami(ctx)
		if err != nil {
			return fmt.Errorf("the token did not work, nothing was saved: %w", err)
		}
		p, err := saveConfig(c)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Saved to %s\n%s", p, who)
		return nil
	}

	c := loadConfig()
	if c.URL == "" || c.Token == "" {
		return errors.New("not logged in: run `hosbah login --url … --token …` (or set CLOUDY_URL and CLOUDY_TOKEN)")
	}
	client := hosbah.New(c.URL, c.Token)

	switch cmd {
	case "tools":
		tools, err := client.Tools(ctx)
		if err != nil {
			return err
		}
		sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
		for _, t := range tools {
			fmt.Fprintf(out, "  %-22s %s\n", t.Name, firstSentence(t.Description))
		}
		return nil
	case "help":
		if len(rest) == 0 {
			usage(out)
			return nil
		}
		tools, err := client.Tools(ctx)
		if err != nil {
			return err
		}
		for _, t := range tools {
			if t.Name == rest[0] {
				fmt.Fprintf(out, "%s\n\n%s\n\narguments:\n", t.Name, t.Description)
				props, _ := t.InputSchema["properties"].(map[string]any)
				req := map[string]bool{}
				if rs, ok := t.InputSchema["required"].([]any); ok {
					for _, r := range rs {
						req[fmt.Sprint(r)] = true
					}
				}
				keys := make([]string, 0, len(props))
				for k := range props {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					p, _ := props[k].(map[string]any)
					mark := ""
					if req[k] {
						mark = " (required)"
					}
					fmt.Fprintf(out, "  %-18s %v%s  %v\n", k, p["type"], mark, orEmpty(p["description"]))
				}
				return nil
			}
		}
		return fmt.Errorf("no tool %q (see `hosbah tools`)", rest[0])
	}

	tool := cmd
	args := map[string]any{}
	if s, ok := shortcuts[cmd]; ok {
		tool = s.tool
		if s.first != "" && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") && !strings.Contains(rest[0], "=") {
			args[s.first] = rest[0]
			rest = rest[1:]
		}
	}
	for k, v := range parseArgs(rest) {
		args[k] = v
	}
	text, err := client.Call(ctx, tool, args)
	if err != nil {
		return err
	}
	fmt.Fprint(out, text)
	if !strings.HasSuffix(text, "\n") {
		fmt.Fprintln(out)
	}
	return nil
}

// parseArgs reads key=value and --key value / --key=value / --flag. Values that look like JSON
// (true, 3, {"a":1}) are decoded; everything else stays a string.
func parseArgs(rest []string) map[string]any {
	out := map[string]any{}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		var k, v string
		hasValue := false
		switch {
		case strings.HasPrefix(a, "--"):
			k = strings.TrimPrefix(a, "--")
			if kk, vv, ok := strings.Cut(k, "="); ok {
				k, v, hasValue = kk, vv, true
			} else if i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "--") && !strings.Contains(rest[i+1], "=") {
				v, hasValue = rest[i+1], true
				i++
			}
		case strings.Contains(a, "="):
			k, v, _ = strings.Cut(a, "=")
			hasValue = true
		default:
			continue
		}
		k = strings.ReplaceAll(k, "-", "_")
		if !hasValue {
			out[k] = true
			continue
		}
		out[k] = typed(v)
	}
	return out
}

func typed(v string) any {
	if b, err := strconv.ParseBool(v); err == nil && (v == "true" || v == "false") {
		return b
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil && !strings.HasPrefix(v, "0") || v == "0" {
		return n
	}
	if strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[") {
		var j any
		if json.Unmarshal([]byte(v), &j) == nil {
			return j
		}
	}
	return v
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		s = s[:i+1]
	}
	if len(s) > 110 {
		s = s[:107] + "…"
	}
	return s
}

func orEmpty(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func usage(out io.Writer) {
	fmt.Fprint(out, `cloudy — manage Hosbah from the terminal

  hosbah login --url <cloudy url> --token <cld_… team token>
  hosbah tools                         list what the token can do
  hosbah help <tool>                   a tool's arguments
  hosbah <tool> key=value …            run any tool (e.g. add_proxy_host server=edge hostname=… upstream=…)

shortcuts: whoami, servers, server <s>, apps, app <a>, deploy <a> [--wait], logs <a>, env <a>, proxies, dbs
environment: CLOUDY_URL, CLOUDY_TOKEN override the saved login.
`)
}
