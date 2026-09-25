package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// app.processes (MH-574 G1): the "Workers" page's manage actions — status, restart, stop or start of
// one or every process of an app's live release. Every command is a fixed docker argv (no shell):
// `docker inspect` and one `docker stats --no-stream` to report, `docker restart/stop/start` by
// container name found through the app's labels, the same way app.drain and app.logs find them.
// stop never removes the container (plain `docker stop`), so a later start reuses it; a drain or a
// deploy still cleans a stopped one up (appContainers lists with -a).

func appProcesses(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.ProcessesParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	switch p.Action {
	case agentproto.ProcessActionStatus:
		// nothing to change before reporting
	case agentproto.ProcessActionRestart:
		names, err := appContainers(oc, p.AppID, p.Process, true)
		if err != nil {
			return nil, 1, err
		}
		if len(names) == 0 {
			return nil, 1, errors.New("no containers of this app to restart")
		}
		for _, n := range names {
			oc.Info("restarting %s", n)
			if _, err := dockerStep(oc, "restart", n); err != nil {
				return nil, 1, fmt.Errorf("restart %s: %w", n, err)
			}
		}
	case agentproto.ProcessActionStop:
		names, err := appContainers(oc, p.AppID, p.Process, false)
		if err != nil {
			return nil, 1, err
		}
		if len(names) == 0 {
			return nil, 1, errors.New("no running container of this process")
		}
		for _, n := range names {
			oc.Info("stopping %s", n)
			// Plain stop, never rm: a later start must find the same container.
			if _, err := dockerStep(oc, "stop", n); err != nil {
				return nil, 1, fmt.Errorf("stop %s: %w", n, err)
			}
		}
	case agentproto.ProcessActionStart:
		names, err := appContainers(oc, p.AppID, p.Process, true)
		if err != nil {
			return nil, 1, err
		}
		if len(names) == 0 {
			return nil, 1, errors.New("no stopped container of this process to start")
		}
		for _, n := range names {
			oc.Info("starting %s", n)
			if _, err := dockerStep(oc, "start", n); err != nil {
				return nil, 1, fmt.Errorf("start %s: %w", n, err)
			}
		}
	}
	list, err := processStatus(oc, p.AppID, p.Process)
	if err != nil {
		return nil, 1, err
	}
	return agentproto.ProcessesResult{Processes: list}, 0, nil
}

// processInspect is the slice of `docker inspect` this handler reads.
type processInspect struct {
	Name  string `json:"Name"`
	State struct {
		Status    string `json:"Status"`
		StartedAt string `json:"StartedAt"`
	} `json:"State"`
	RestartCount int `json:"RestartCount"`
	Config       struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

// processStatus reports every one of the app's containers (one process, or all when process is
// empty), newest generation first: inspected state plus, for the ones still running, one shared
// `docker stats --no-stream` reading.
func processStatus(oc *OpContext, appID, process string) ([]agentproto.ProcessStatus, error) {
	names, err := appContainers(oc, appID, process, true)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return []agentproto.ProcessStatus{}, nil
	}
	raw, err := appDocker(oc.Ctx, nil, "docker", append([]string{"inspect"}, names...)...)
	if err != nil {
		return nil, errors.New("could not inspect the app's containers")
	}
	out := string(raw)
	var raws []processInspect
	if err := json.Unmarshal([]byte(out), &raws); err != nil {
		return nil, errors.New("could not read the app's container state")
	}
	var running []string
	for _, r := range raws {
		if r.State.Status == "running" {
			running = append(running, strings.TrimPrefix(r.Name, "/"))
		}
	}
	var stats map[string]dockerStatSample
	if len(running) > 0 {
		stats = collectStats(oc, running)
	}
	list := make([]agentproto.ProcessStatus, 0, len(raws))
	for _, r := range raws {
		name := strings.TrimPrefix(r.Name, "/")
		proc := r.Config.Labels[agentproto.LabelProcess]
		if proc == "" {
			proc = r.Config.Labels[agentproto.LegacyLabel(agentproto.LabelProcess)]
		}
		st := agentproto.ProcessStatus{
			Process:      proc,
			Container:    name,
			State:        r.State.Status,
			StartedAt:    r.State.StartedAt,
			RestartCount: r.RestartCount,
			Image:        r.Config.Image,
			Deployment:   r.Config.Labels[agentproto.LabelDeployment],
		}
		if st.Deployment == "" {
			st.Deployment = r.Config.Labels[agentproto.LegacyLabel(agentproto.LabelDeployment)]
		}
		if s, ok := stats[name]; ok {
			st.CPUPercent, st.MemBytes, st.MemLimitBytes = s.cpu, s.mem, s.limit
		}
		list = append(list, st)
	}
	return list, nil
}

// dockerStatSample is one line of `docker stats --no-stream --format {{json .}}`.
type dockerStatSample struct {
	cpu   float64
	mem   int64
	limit int64
}

// collectStats runs one `docker stats --no-stream` for every running container named, and never
// fails the whole read: a container that stopped between the inspect and the stats call is simply
// missing from the result.
func collectStats(oc *OpContext, names []string) map[string]dockerStatSample {
	result := map[string]dockerStatSample{}
	args := append([]string{"stats", "--no-stream", "--format", "{{json .}}"}, names...)
	raw, err := appDocker(oc.Ctx, nil, "docker", args...)
	if err != nil {
		return result
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row struct {
			Name     string `json:"Name"`
			CPUPerc  string `json:"CPUPerc"`
			MemUsage string `json:"MemUsage"`
		}
		if json.Unmarshal([]byte(line), &row) != nil || row.Name == "" {
			continue
		}
		var sample dockerStatSample
		sample.cpu = parsePercent(row.CPUPerc)
		if used, limit, ok := strings.Cut(row.MemUsage, "/"); ok {
			sample.mem = int64(parseDockerSize(strings.TrimSpace(used)))
			sample.limit = int64(parseDockerSize(strings.TrimSpace(limit)))
		}
		result[row.Name] = sample
	}
	return result
}
