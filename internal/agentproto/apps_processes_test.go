package agentproto

import "testing"

// ProcessesParams.Validate is checked on both sides of the wire: the control plane before it signs
// and enqueues app.processes, the agent before it runs. Stopping or starting web is refused, and so
// is naming no process at all for stop/start — only status and restart make sense for "every
// process".
func TestProcessesParamsValidate(t *testing.T) {
	appID := "00000000-0000-4000-8000-0000000000a1"
	cases := []struct {
		name string
		p    ProcessesParams
		ok   bool
	}{
		{"status, every process", ProcessesParams{AppID: appID, Action: ProcessActionStatus}, true},
		{"status, one process", ProcessesParams{AppID: appID, Action: ProcessActionStatus, Process: "web"}, true},
		{"restart, every process", ProcessesParams{AppID: appID, Action: ProcessActionRestart}, true},
		{"restart, web", ProcessesParams{AppID: appID, Action: ProcessActionRestart, Process: "web"}, true},
		{"restart, worker", ProcessesParams{AppID: appID, Action: ProcessActionRestart, Process: "worker:queue"}, true},
		{"stop, worker", ProcessesParams{AppID: appID, Action: ProcessActionStop, Process: "worker:queue"}, true},
		{"start, worker", ProcessesParams{AppID: appID, Action: ProcessActionStart, Process: "worker:queue"}, true},
		{"stop, web", ProcessesParams{AppID: appID, Action: ProcessActionStop, Process: "web"}, false},
		{"start, web", ProcessesParams{AppID: appID, Action: ProcessActionStart, Process: "web"}, false},
		{"stop, no process", ProcessesParams{AppID: appID, Action: ProcessActionStop}, false},
		{"start, no process", ProcessesParams{AppID: appID, Action: ProcessActionStart}, false},
		{"bad app id", ProcessesParams{AppID: "not-a-uuid", Action: ProcessActionStatus}, false},
		{"bad process", ProcessesParams{AppID: appID, Action: ProcessActionStatus, Process: "sidecar"}, false},
		{"bad action", ProcessesParams{AppID: appID, Action: "delete"}, false},
	}
	for _, c := range cases {
		err := c.p.Validate()
		if (err == nil) != c.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

// app.processes is a typed agent kind like every other app operation — an agent refuses anything
// off the allowlist, so it must be on it.
func TestAppProcessesIsAnAllowedAgentKind(t *testing.T) {
	found := false
	for _, k := range AgentKinds {
		if k == KindAppProcesses {
			found = true
		}
	}
	if !found {
		t.Fatal("app.processes is not in AgentKinds")
	}
}
