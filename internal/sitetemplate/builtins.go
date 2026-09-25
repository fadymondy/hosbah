package sitetemplate

import (
	"embed"
	"fmt"
	"sort"
)

//go:embed builtin/*.yaml
var embedded embed.FS

// Builtin is one read-only, shipped-with-the-binary template, keyed by name (like a driver name is
// unique, a built-in template's own name is unique among built-ins).
type Builtin struct {
	Template Template
	Source   string // the raw YAML, for export/copy
}

var builtins = func() map[string]Builtin {
	entries, err := embedded.ReadDir("builtin")
	if err != nil {
		panic(err)
	}
	out := map[string]Builtin{}
	for _, e := range entries {
		raw, err := embedded.ReadFile("builtin/" + e.Name())
		if err != nil {
			panic(err)
		}
		t, err := Parse(raw)
		if err != nil {
			panic(fmt.Sprintf("sitetemplate: builtin %s: %v", e.Name(), err))
		}
		out[t.Name] = Builtin{Template: t, Source: string(raw)}
	}
	return out
}()

// Builtins lists every built-in template, sorted by name.
func Builtins() []Builtin {
	names := make([]string, 0, len(builtins))
	for n := range builtins {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Builtin, len(names))
	for i, n := range names {
		out[i] = builtins[n]
	}
	return out
}

// BuiltinByName looks a built-in template up by name.
func BuiltinByName(name string) (Builtin, bool) {
	b, ok := builtins[name]
	return b, ok
}
