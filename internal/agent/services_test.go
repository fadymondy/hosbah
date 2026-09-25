package agent

import (
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// service.list / service.action (MH-576 D3), unit-tested against a fake systemctl runner — the
// same injectable-runner shape used by workloads_test.go.

func newServiceFake(answers map[string]string) (*unitHost, *fakeRunner) {
	f := &fakeRunner{answers: answers, fail: map[string]string{}}
	return &unitHost{run: f.run}, f
}

var systemctlAnswers = map[string]string{
	"systemctl list-units --type=service --all --no-legend --no-pager --plain": "" +
		"caddy.service    loaded active running Caddy web server\n" +
		"nginx.service    loaded failed failed  A high performance web server\n",
	"systemctl list-unit-files --type=service --no-legend --no-pager --plain": "" +
		"caddy.service    enabled\n" +
		"nginx.service    disabled\n" +
		"postgresql.service enabled\n",
}

func TestServiceListMergesUnitsAndUnitFiles(t *testing.T) {
	h, _ := newServiceFake(systemctlAnswers)
	oc, _ := opCtx(t, agentproto.ServiceListParams{})
	res, code, err := h.list(oc)
	if err != nil || code != 0 {
		t.Fatalf("list: %v (%d)", err, code)
	}
	units := res.(agentproto.ServiceListResult).Units
	if len(units) != 3 {
		t.Fatalf("want 3 units, got %d: %+v", len(units), units)
	}
	byName := map[string]agentproto.ServiceUnit{}
	for _, u := range units {
		byName[u.Name] = u
	}
	caddy := byName["caddy.service"]
	if caddy.ActiveState != "active" || caddy.SubState != "running" || !caddy.Enabled || caddy.Description != "Caddy web server" {
		t.Fatalf("caddy.service: %+v", caddy)
	}
	nginx := byName["nginx.service"]
	if nginx.ActiveState != "failed" || nginx.Enabled {
		t.Fatalf("nginx.service: %+v", nginx)
	}
	// postgresql.service is installed (in list-unit-files) but never started, so list-units never
	// named it: it must still show up, sourced only from the unit-files pass.
	pg := byName["postgresql.service"]
	if pg.LoadState != "not-found" || pg.ActiveState != "inactive" || pg.SubState != "dead" || !pg.Enabled {
		t.Fatalf("postgresql.service: %+v", pg)
	}
}

func TestServiceListFiltersByQuery(t *testing.T) {
	h, _ := newServiceFake(systemctlAnswers)
	oc, _ := opCtx(t, agentproto.ServiceListParams{Query: "CAD"})
	res, code, err := h.list(oc)
	if err != nil || code != 0 {
		t.Fatalf("list: %v (%d)", err, code)
	}
	units := res.(agentproto.ServiceListResult).Units
	if len(units) != 1 || units[0].Name != "caddy.service" {
		t.Fatalf("query filter: %+v", units)
	}
}

func TestServiceListRefusesBadParams(t *testing.T) {
	h, f := newServiceFake(systemctlAnswers)
	oc, _ := opCtx(t, agentproto.ServiceListParams{Query: string(make([]byte, 300))})
	_, code, err := h.list(oc)
	if err == nil || code != 2 || len(f.calls) != 0 {
		t.Fatalf("want a validation refusal with no calls, got err=%v code=%d calls=%v", err, code, f.calls)
	}
}

func TestServiceActionRunsAndReadsBackState(t *testing.T) {
	answers := map[string]string{
		"systemctl restart -- caddy.service":                                                 "",
		"systemctl show --property=ActiveState --property=SubState --value -- caddy.service": "active\nrunning\n",
	}
	h, f := newServiceFake(answers)
	oc, lines := opCtx(t, agentproto.ServiceActionParams{Unit: "caddy.service", Action: "restart"})
	res, code, err := h.action(oc)
	if err != nil || code != 0 {
		t.Fatalf("action: %v (%d)", err, code)
	}
	out := res.(agentproto.ServiceActionResult)
	if out.Unit != "caddy.service" || out.ActiveState != "active" || out.SubState != "running" {
		t.Fatalf("result: %+v", out)
	}
	if len(f.calls) != 2 || f.calls[0].argv != "systemctl restart -- caddy.service" {
		t.Fatalf("calls: %+v", f.calls)
	}
	if len(*lines) == 0 {
		t.Fatalf("want a log line naming the action")
	}
}

func TestServiceActionRefusesTheAgentsOwnUnit(t *testing.T) {
	for _, unit := range []string{"hosbah-agent.service", "cloudy-agent.service"} {
		h, f := newServiceFake(nil)
		oc, _ := opCtx(t, agentproto.ServiceActionParams{Unit: unit, Action: "stop"})
		_, code, err := h.action(oc)
		if err == nil || code != 2 || len(f.calls) != 0 {
			t.Fatalf("%s: want a refusal with no calls, got err=%v code=%d calls=%v", unit, err, code, f.calls)
		}
	}
}

func TestServiceActionRefusesBadUnitNameAndAction(t *testing.T) {
	h, f := newServiceFake(nil)
	oc, _ := opCtx(t, agentproto.ServiceActionParams{Unit: "caddy; rm -rf /", Action: "restart"})
	if _, code, err := h.action(oc); err == nil || code != 2 {
		t.Fatalf("bad unit name: code=%d err=%v", code, err)
	}
	oc, _ = opCtx(t, agentproto.ServiceActionParams{Unit: "caddy.service", Action: "explode"})
	if _, code, err := h.action(oc); err == nil || code != 2 {
		t.Fatalf("bad action: code=%d err=%v", code, err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("no shelling out for a refused action, got %v", f.calls)
	}
}
