package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// appLoads reads the load of every Hosbah app running on this host: one `docker ps` per label
// generation to map container to app, then one `docker stats --no-stream` for all of them, summed
// per app. It never fails the heartbeat: without Docker, or on any error, it reports nothing.
func appLoads(ctx context.Context, run runFunc) []agentproto.AppLoad {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	owner := map[string]string{} // container name -> app id
	for _, l := range labelSets {
		key := l(agentproto.LabelApp)
		out, err := run(ctx, nil, "docker", "ps", "--filter", "label="+key, "--format", "{{.Names}}\t{{.Label \""+key+"\"}}")
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			name, app, ok := strings.Cut(strings.TrimSpace(line), "\t")
			if ok && name != "" && app != "" && owner[name] == "" {
				owner[name] = app
			}
		}
	}
	if len(owner) == 0 {
		return nil
	}
	names := make([]string, 0, len(owner))
	for n := range owner {
		names = append(names, n)
	}
	sort.Strings(names)
	raw, err := run(ctx, nil, "docker", append([]string{"stats", "--no-stream", "--format", "{{json .}}"}, names...)...)
	if err != nil {
		return nil
	}
	byApp := map[string]*agentproto.AppLoad{}
	for _, line := range strings.Split(string(raw), "\n") {
		var row struct {
			Name     string `json:"Name"`
			CPUPerc  string `json:"CPUPerc"`
			MemUsage string `json:"MemUsage"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &row) != nil || owner[row.Name] == "" {
			continue
		}
		app := owner[row.Name]
		l := byApp[app]
		if l == nil {
			l = &agentproto.AppLoad{AppID: app}
			byApp[app] = l
		}
		l.Containers++
		l.CPUPercent += parsePercent(row.CPUPerc)
		if used, limit, ok := strings.Cut(row.MemUsage, "/"); ok {
			l.MemUsedBytes += parseDockerSize(strings.TrimSpace(used))
			l.MemLimitBytes += parseDockerSize(strings.TrimSpace(limit))
		}
	}
	out := make([]agentproto.AppLoad, 0, len(byApp))
	for _, l := range byApp {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AppID < out[j].AppID })
	return out
}
