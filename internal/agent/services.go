package agent

import (
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// service.list / service.action (MH-576 D3): systemd .service units, read through `systemctl`
// directly — no shell, a fixed argv per call — and unit-tested against a fake systemctl on PATH
// (services_test.go), the same injectable-runner shape as workloads.go.
type unitHost struct {
	run runFunc
}

func newServiceHost() *unitHost { return &unitHost{run: execRun} }

func (h *unitHost) list(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.ServiceListParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	out, err := h.run(oc.Ctx, nil, "systemctl", "list-units", "--type=service", "--all", "--no-legend", "--no-pager", "--plain")
	if err != nil {
		return nil, 1, err
	}
	units := map[string]agentproto.ServiceUnit{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// UNIT LOAD ACTIVE SUB DESCRIPTION...
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		name := f[0]
		if !strings.HasSuffix(name, ".service") {
			continue
		}
		desc := ""
		if i := strings.Index(line, name); i >= 0 {
			rest := strings.TrimSpace(line[i+len(name):])
			rest = strings.TrimPrefix(rest, f[1])
			rest = strings.TrimSpace(rest)
			rest = strings.TrimPrefix(rest, f[2])
			rest = strings.TrimSpace(rest)
			rest = strings.TrimPrefix(rest, f[3])
			desc = strings.TrimSpace(rest)
		}
		units[name] = agentproto.ServiceUnit{Name: name, LoadState: f[1], ActiveState: f[2], SubState: f[3], Description: desc}
	}

	// list-unit-files also names a service that is installed but never started (LOAD=not-found
	// above would hide it), and carries the enabled/disabled/static state list-units does not.
	filesOut, err := h.run(oc.Ctx, nil, "systemctl", "list-unit-files", "--type=service", "--no-legend", "--no-pager", "--plain")
	if err == nil {
		for _, line := range strings.Split(string(filesOut), "\n") {
			f := strings.Fields(strings.TrimSpace(line))
			if len(f) < 2 || !strings.HasSuffix(f[0], ".service") {
				continue
			}
			u, ok := units[f[0]]
			if !ok {
				u = agentproto.ServiceUnit{Name: f[0], LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}
			}
			u.Enabled = f[1] == "enabled" || f[1] == "enabled-runtime" || f[1] == "static"
			units[f[0]] = u
		}
	}

	res := agentproto.ServiceListResult{}
	q := strings.ToLower(strings.TrimSpace(p.Query))
	for _, u := range units {
		if q != "" && !strings.Contains(strings.ToLower(u.Name), q) {
			continue
		}
		res.Units = append(res.Units, u)
	}
	sortServiceUnits(res.Units)
	return res, 0, nil
}

func sortServiceUnits(units []agentproto.ServiceUnit) {
	for i := 1; i < len(units); i++ {
		for j := i; j > 0 && units[j].Name < units[j-1].Name; j-- {
			units[j], units[j-1] = units[j-1], units[j]
		}
	}
}

func (h *unitHost) action(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.ServiceActionParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	oc.Info("%s %s", p.Action, p.Unit)
	if _, err := h.run(oc.Ctx, nil, "systemctl", p.Action, "--", p.Unit); err != nil {
		return nil, 1, err
	}
	state, _ := h.run(oc.Ctx, nil, "systemctl", "show", "--property=ActiveState", "--property=SubState", "--value", "--", p.Unit)
	res := agentproto.ServiceActionResult{Unit: p.Unit}
	lines := strings.Split(strings.TrimSpace(string(state)), "\n")
	if len(lines) > 0 {
		res.ActiveState = strings.TrimSpace(lines[0])
	}
	if len(lines) > 1 {
		res.SubState = strings.TrimSpace(lines[1])
	}
	return res, 0, nil
}
