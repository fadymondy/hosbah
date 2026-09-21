package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

const (
	execApp = "11111111-1111-4111-8111-111111111111"
	execDep = "22222222-2222-4222-8222-222222222222"
	execOp  = "33333333-3333-4333-8333-333333333333"
)

// streamCall is one argv the exec handler handed to its streamer, element by element (fakeRunner
// joins an argv with spaces, which is exactly what this test must not do).
type streamCall struct {
	name string
	args []string
}

type fakeStream struct {
	mu    sync.Mutex
	calls []streamCall
	lines []string // stdout lines to hand over
	code  int
	block bool // run until the context ends, like a command that never finishes
}

func (f *fakeStream) stream(ctx context.Context, onLine func(stream, line string), name string, args ...string) (int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, streamCall{name: name, args: append([]string(nil), args...)})
	f.mu.Unlock()
	for _, l := range f.lines {
		onLine("stdout", l)
	}
	if f.block {
		<-ctx.Done()
		return -1, ctx.Err()
	}
	return f.code, nil
}

func execCtx(t *testing.T, p agentproto.ExecParams) (*OpContext, *[]string) {
	t.Helper()
	oc, lines := opCtx(t, p)
	oc.OpID = execOp
	return oc, lines
}

func newExecFake(answers map[string]string, fs *fakeStream) (*execHost, *fakeRunner) {
	f := &fakeRunner{answers: answers, fail: map[string]string{}}
	return &execHost{run: f.run, stream: fs.stream, killGrace: time.Second}, f
}

var execPS = "docker ps --filter label=cloudy.app=" + execApp + " --filter label=cloudy.process=web --filter label=cloudy.deployment=" + execDep

func TestAppExecHandsTheCommandToDockerExecAsOneElement(t *testing.T) {
	command := `php artisan schedule:run >> /dev/null; echo "$HOME" 'a b' $(id) && rm -rf /tmp/x | tee`
	fs := &fakeStream{lines: []string{"hello", "world"}}
	h, f := newExecFake(map[string]string{execPS: "cloudy-shop-web-22222222\n"}, fs)
	oc, lines := execCtx(t, agentproto.ExecParams{AppID: execApp, DeploymentID: execDep, Argv: agentproto.ShellArgv(command), TimeoutSeconds: 30})

	res, code, err := h.appExec(oc)
	if err != nil || code != 0 {
		t.Fatalf("appExec: %v (%d)", err, code)
	}
	if len(fs.calls) != 1 {
		t.Fatalf("want one exec, got %+v", fs.calls)
	}
	want := []string{"exec", "-e", "CLOUDY_EXEC_ID=" + execOp, "cloudy-shop-web-22222222", "sh", "-lc", command}
	if fs.calls[0].name != "docker" || !reflect.DeepEqual(fs.calls[0].args, want) {
		t.Fatalf("argv:\n got %q\nwant %q", fs.calls[0].args, want)
	}
	for _, a := range fs.calls[0].args {
		if a == "-t" || a == "-it" || a == "-i" || a == "--tty" {
			t.Fatalf("no TTY and no stdin: %q", fs.calls[0].args)
		}
	}
	// The only host command besides the exec is the lookup, and the user's line is in neither.
	if len(f.calls) != 1 || !strings.HasPrefix(f.calls[0].argv, execPS) || strings.Contains(f.calls[0].argv, "artisan") {
		t.Fatalf("host commands: %+v", f.calls)
	}
	r := res.(agentproto.ExecResult)
	if r.Container != "cloudy-shop-web-22222222" || r.ExitCode != 0 || r.TimedOut || r.Bytes != len("hello\nworld\n") {
		t.Fatalf("result: %+v", r)
	}
	if got := strings.Join(*lines, "\n"); !strings.Contains(got, "hello\nworld") {
		t.Fatalf("output not streamed: %q", got)
	}
}

// A caller that needs no shell sends a real argv (internal/queues): every element reaches docker exec
// as it is, after the container's name, and no sh is put in front of it.
func TestAppExecHandsARealArgvOverElementByElement(t *testing.T) {
	argv := []string{"php", "artisan", "queue:retry", "--no-ansi", "--no-interaction", "9f3c1a52-7d0e-4c55-b0a1-5a7d3f1e8c21"}
	fs := &fakeStream{}
	h, _ := newExecFake(map[string]string{execPS: "cloudy-shop-web-22222222\n"}, fs)
	oc, _ := execCtx(t, agentproto.ExecParams{AppID: execApp, DeploymentID: execDep, Argv: argv, TimeoutSeconds: 30})
	if _, code, err := h.appExec(oc); err != nil || code != 0 {
		t.Fatalf("appExec: %v (%d)", err, code)
	}
	want := append([]string{"exec", "-e", "CLOUDY_EXEC_ID=" + execOp, "cloudy-shop-web-22222222"}, argv...)
	if len(fs.calls) != 1 || fs.calls[0].name != "docker" || !reflect.DeepEqual(fs.calls[0].args, want) {
		t.Fatalf("argv:\n got %+v\nwant %q", fs.calls, want)
	}
}

func TestAppExecReportsTheExitCode(t *testing.T) {
	fs := &fakeStream{code: 3}
	h, _ := newExecFake(map[string]string{execPS: "cloudy-shop-web-22222222\n"}, fs)
	oc, _ := execCtx(t, agentproto.ExecParams{AppID: execApp, DeploymentID: execDep, Argv: agentproto.ShellArgv("exit 3"), TimeoutSeconds: 30})
	res, code, err := h.appExec(oc)
	if err == nil || code != 3 || res.(agentproto.ExecResult).ExitCode != 3 {
		t.Fatalf("want exit 3: %v %d %+v", err, code, res)
	}
}

func TestAppExecTimeoutKillsTheCommand(t *testing.T) {
	fs := &fakeStream{block: true}
	h, f := newExecFake(map[string]string{execPS: "cloudy-shop-web-22222222\n"}, fs)
	oc, _ := execCtx(t, agentproto.ExecParams{AppID: execApp, DeploymentID: execDep, Argv: agentproto.ShellArgv("sleep 600"), TimeoutSeconds: 1})

	started := time.Now()
	res, code, err := h.appExec(oc)
	if took := time.Since(started); took > 10*time.Second {
		t.Fatalf("the timeout did not end the exec: %s", took)
	}
	if err == nil || !strings.Contains(err.Error(), "timeout of 1 s") || code != agentproto.ExecExitTimeout {
		t.Fatalf("want a timeout: %v (%d)", err, code)
	}
	if r := res.(agentproto.ExecResult); !r.TimedOut || r.ExitCode != agentproto.ExecExitTimeout {
		t.Fatalf("result: %+v", r)
	}
	// What the exec started inside the container is killed there, found by the op's mark.
	last := f.calls[len(f.calls)-1].argv
	if !strings.HasPrefix(last, "docker exec cloudy-shop-web-22222222 sh -c ") || !strings.HasSuffix(last, " sh "+execOp) ||
		!strings.Contains(last, "kill -9") || strings.Contains(last, "sleep 600") {
		t.Fatalf("kill command: %q", last)
	}
}

func TestAppExecWithoutALiveContainerFailsCleanly(t *testing.T) {
	fs := &fakeStream{}
	h, _ := newExecFake(map[string]string{}, fs)
	oc, _ := execCtx(t, agentproto.ExecParams{AppID: execApp, DeploymentID: execDep, Argv: agentproto.ShellArgv("true"), TimeoutSeconds: 30})
	if _, code, err := h.appExec(oc); err == nil || code != 1 || !strings.Contains(err.Error(), "no running web container") {
		t.Fatalf("want a clean failure: %v (%d)", err, code)
	}
	if len(fs.calls) != 0 {
		t.Fatalf("nothing may run: %+v", fs.calls)
	}
}

func TestAppExecCapsItsOutput(t *testing.T) {
	line := strings.Repeat("x", 4000)
	fs := &fakeStream{}
	for i := 0; i < 200; i++ { // 800 KB
		fs.lines = append(fs.lines, line)
	}
	h, _ := newExecFake(map[string]string{execPS: "cloudy-shop-web-22222222\n"}, fs)
	oc, lines := execCtx(t, agentproto.ExecParams{AppID: execApp, DeploymentID: execDep, Argv: agentproto.ShellArgv("yes"), TimeoutSeconds: 30})
	res, _, err := h.appExec(oc)
	if err != nil {
		t.Fatal(err)
	}
	r := res.(agentproto.ExecResult)
	if !r.Truncated || r.Bytes > agentproto.MaxExecOutput {
		t.Fatalf("result: %+v", r)
	}
	total := 0
	for _, l := range *lines {
		total += len(l)
	}
	if total > agentproto.MaxExecOutput+1024 {
		t.Fatalf("relayed %d bytes", total)
	}
}

func TestAppExecRefusesBadParams(t *testing.T) {
	ok := agentproto.ExecParams{AppID: execApp, DeploymentID: execDep, Argv: agentproto.ShellArgv("true"), TimeoutSeconds: 30}
	for name, patch := range map[string]func(*agentproto.ExecParams){
		"app id":        func(p *agentproto.ExecParams) { p.AppID = "x; rm -rf /" },
		"deployment id": func(p *agentproto.ExecParams) { p.DeploymentID = "" },
		"no argv":       func(p *agentproto.ExecParams) { p.Argv = nil },
		"option argv0":  func(p *agentproto.ExecParams) { p.Argv = []string{"--privileged", "sh"} },
		"nul":           func(p *agentproto.ExecParams) { p.Argv = []string{"sh", "-lc", "a\x00b"} },
		"timeout":       func(p *agentproto.ExecParams) { p.TimeoutSeconds = 0 },
		"long timeout":  func(p *agentproto.ExecParams) { p.TimeoutSeconds = 3601 },
	} {
		p := ok
		patch(&p)
		fs := &fakeStream{}
		h, f := newExecFake(map[string]string{}, fs)
		oc, _ := execCtx(t, p)
		if _, code, err := h.appExec(oc); err == nil || code != 2 || len(f.calls) != 0 || len(fs.calls) != 0 {
			t.Errorf("%s: want a refusal before anything runs, got %v (%d)", name, err, code)
		}
	}
}

func TestAppExecIsAnAgentKindThatNeverQueuesBehindADeploy(t *testing.T) {
	raw, _ := json.Marshal(agentproto.ExecParams{AppID: execApp})
	if laneOf(agentproto.SignedOp{Kind: agentproto.KindAppExec, Params: raw}) != "" {
		t.Fatal("app.exec must not share the app's deploy lane")
	}
	found := false
	for _, k := range agentproto.AgentKinds {
		found = found || k == agentproto.KindAppExec
	}
	if !found || NewExecutor("s", nil, "v", "").handlers[agentproto.KindAppExec] == nil {
		t.Fatal("app.exec is not registered on both sides")
	}
}
