package agent

import (
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// app.processes: status reports every container's state and, for the ones running, one shared
// `docker stats` reading; an action restarts, stops or starts by container name found the same way
// app.drain finds them (both label generations); web can never be stopped or started from here.

func TestAppProcessesStatusReportsStateAndStats(t *testing.T) {
	webName := "hosbah-shop-web-aaaaaaaa"
	workerName := "hosbah-shop-worker-queue-bbbbbbbb"
	inspect := `[
		{"Name":"/` + webName + `","State":{"Status":"running","StartedAt":"2026-09-20T10:00:00Z"},"RestartCount":0,
		 "Config":{"Image":"hosbah/shop:dep1","Labels":{"hosbah.process":"web","hosbah.deployment":"dep1"}}},
		{"Name":"/` + workerName + `","State":{"Status":"exited","StartedAt":"2026-09-19T10:00:00Z"},"RestartCount":3,
		 "Config":{"Image":"hosbah/shop:dep1","Labels":{"hosbah.process":"worker:queue","hosbah.deployment":"dep1"}}}
	]`
	stats := `{"Name":"` + webName + `","CPUPerc":"1.23%","MemUsage":"12.5MiB / 512MiB"}`
	f := &fakeRunner{answers: map[string]string{
		"docker ps --filter label=hosbah.app=" + stAppID:          webName + "\n" + workerName + "\n",
		"docker ps --filter label=cloudy.app=" + stAppID:          "",
		"docker inspect " + webName + " " + workerName:            inspect,
		"docker stats --no-stream --format {{json .}} " + webName: stats,
	}, fail: map[string]string{}}
	oldDocker := appDocker
	t.Cleanup(func() { appDocker = oldDocker })
	appDocker = f.run

	oc, _ := opCtx(t, agentproto.ProcessesParams{AppID: stAppID, Action: agentproto.ProcessActionStatus})
	res, code, err := appProcesses(oc)
	if err != nil || code != 0 {
		t.Fatalf("status: %v %d", err, code)
	}
	out := res.(agentproto.ProcessesResult)
	if len(out.Processes) != 2 {
		t.Fatalf("processes: %+v", out.Processes)
	}
	web, worker := out.Processes[0], out.Processes[1]
	if web.Process != "web" || web.State != "running" || web.CPUPercent != 1.23 || web.MemBytes == 0 || web.MemLimitBytes == 0 {
		t.Fatalf("web: %+v", web)
	}
	if worker.Process != "worker:queue" || worker.State != "exited" || worker.RestartCount != 3 || worker.CPUPercent != 0 {
		t.Fatalf("worker (stopped, no stats): %+v", worker)
	}

	// docker stats was only asked about the running container.
	var statsCalls []string
	for _, c := range f.calls {
		if strings.HasPrefix(c.argv, "docker stats") {
			statsCalls = append(statsCalls, c.argv)
		}
	}
	if len(statsCalls) != 1 || !strings.Contains(statsCalls[0], webName) || strings.Contains(statsCalls[0], workerName) {
		t.Fatalf("stats calls: %v", statsCalls)
	}
}

func TestAppProcessesRestartRestartsEveryContainerOfTheApp(t *testing.T) {
	names := "hosbah-shop-web-aaaaaaaa\nhosbah-shop-worker-queue-bbbbbbbb"
	f := &fakeRunner{answers: map[string]string{
		"docker ps --filter label=hosbah.app=" + stAppID: names,
		"docker ps --filter label=cloudy.app=" + stAppID: "",
		"docker inspect": `[]`,
	}, fail: map[string]string{}}
	oldDocker, oldStep := appDocker, dockerStep
	t.Cleanup(func() { appDocker, dockerStep = oldDocker, oldStep })
	appDocker = f.run
	var restarted []string
	dockerStep = func(_ *OpContext, args ...string) (int, error) {
		restarted = append(restarted, strings.Join(args, " "))
		return 0, nil
	}

	oc, _ := opCtx(t, agentproto.ProcessesParams{AppID: stAppID, Action: agentproto.ProcessActionRestart})
	_, code, err := appProcesses(oc)
	if err != nil || code != 0 {
		t.Fatalf("restart: %v %d", err, code)
	}
	want := "restart hosbah-shop-web-aaaaaaaa\nrestart hosbah-shop-worker-queue-bbbbbbbb"
	if strings.Join(restarted, "\n") != want {
		t.Fatalf("restarted: %v", restarted)
	}
}

// Stopping or starting web is refused before any docker command runs, on both sides of the wire.
func TestAppProcessesRefusesToStopOrStartWeb(t *testing.T) {
	for _, action := range []string{agentproto.ProcessActionStop, agentproto.ProcessActionStart} {
		if err := (agentproto.ProcessesParams{AppID: stAppID, Action: action, Process: "web"}).Validate(); err == nil {
			t.Errorf("%s web: want a validation error", action)
		}
		if err := (agentproto.ProcessesParams{AppID: stAppID, Action: action}).Validate(); err == nil {
			t.Errorf("%s with no process: want a validation error", action)
		}
	}
	f := &fakeRunner{answers: map[string]string{}, fail: map[string]string{}}
	oldDocker := appDocker
	t.Cleanup(func() { appDocker = oldDocker })
	appDocker = f.run
	oc, _ := opCtx(t, agentproto.ProcessesParams{AppID: stAppID, Action: agentproto.ProcessActionStop, Process: "web"})
	if _, code, err := appProcesses(oc); err == nil || code != 2 {
		t.Fatalf("stop web: %v %d", err, code)
	}
	if len(f.calls) != 0 {
		t.Fatalf("no docker command should have run: %v", f.calls)
	}
}

func TestAppProcessesStopThenStartReuseTheSameContainer(t *testing.T) {
	worker := "hosbah-shop-worker-queue-bbbbbbbb"
	f := &fakeRunner{answers: map[string]string{
		"docker ps --filter label=hosbah.app=" + stAppID: worker,
		"docker ps --filter label=cloudy.app=" + stAppID: "",
		"docker inspect": `[]`,
	}, fail: map[string]string{}}
	oldDocker, oldStep := appDocker, dockerStep
	t.Cleanup(func() { appDocker, dockerStep = oldDocker, oldStep })
	appDocker = f.run
	var steps []string
	dockerStep = func(_ *OpContext, args ...string) (int, error) {
		steps = append(steps, strings.Join(args, " "))
		return 0, nil
	}
	oc, _ := opCtx(t, agentproto.ProcessesParams{AppID: stAppID, Action: agentproto.ProcessActionStop, Process: "worker:queue"})
	if _, code, err := appProcesses(oc); err != nil || code != 0 {
		t.Fatalf("stop: %v %d", err, code)
	}
	oc, _ = opCtx(t, agentproto.ProcessesParams{AppID: stAppID, Action: agentproto.ProcessActionStart, Process: "worker:queue"})
	if _, code, err := appProcesses(oc); err != nil || code != 0 {
		t.Fatalf("start: %v %d", err, code)
	}
	if strings.Join(steps, "\n") != "stop "+worker+"\nstart "+worker {
		t.Fatalf("steps: %v (stop must not remove the container)", steps)
	}
}
