package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// app.exec: a command inside the live release's web container — a site's schedules (cron jobs, one
// shell line: sh -lc <line>) and its queues (internal/queues: a fixed argv with no shell at all).
//
// The argv the control plane signed is handed to `docker exec` element by element: no TTY, no
// stdin, the container's own environment and user. Nothing of it is ever read by a shell on the
// HOST; a schedule's shell line is one element (sh -lc <line>) and the sh is the container's.

// execIDEnv marks every process of one app.exec inside the container, so a timeout can find them:
// killing the `docker exec` client leaves what it started running.
const execIDEnv = "HOSBAH_EXEC_ID"

// execKillScript (a constant; the op id arrives as $1) kills every process carrying the mark.
const execKillScript = `for p in /proc/[0-9]*; do if tr '\0' '\n' < "$p/environ" 2>/dev/null | grep -qx "` + execIDEnv + `=$1"; then kill -9 "${p#/proc/}" 2>/dev/null; fi; done; exit 0`

// lineStreamer runs a fixed argv until ctx ends, handing over its output line by line, and returns
// its exit code.
type lineStreamer func(ctx context.Context, onLine func(stream, line string), name string, args ...string) (int, error)

type execHost struct {
	run    runFunc
	stream lineStreamer
	// killGrace bounds the clean-up inside the container after a timeout.
	killGrace time.Duration
}

func newExecHost() *execHost {
	return &execHost{run: execRun, stream: streamLines, killGrace: 15 * time.Second}
}

func streamLines(ctx context.Context, onLine func(stream, line string), name string, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, name, args...) //#nosec G204 -- docker + the signed argv; never a shell on the host
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.WaitDelay = 5 * time.Second // a grandchild holding the pipes open never holds the op
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 127, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 127, err
	}
	if err := cmd.Start(); err != nil {
		return 127, fmt.Errorf("%s: %w", name, err)
	}
	var wg sync.WaitGroup
	pump := func(r io.Reader, stream string) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			onLine(stream, sc.Text())
		}
		_, _ = io.Copy(io.Discard, r) // a line past the scanner's limit: keep draining so the command never blocks
	}
	wg.Add(2)
	go pump(stdout, "stdout")
	go pump(stderr, "stderr")
	wg.Wait()
	err = cmd.Wait()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), nil
	default:
		return 1, err
	}
}

// liveWebContainer is the running web container of the release a deployment made live.
func (h *execHost) liveWebContainer(ctx context.Context, appID, deploymentID string) (string, error) {
	// hosbah.* labels; cloudy.* when an agent from before the rename started the release. The first
	// generation that has it answers.
	listed := false
	for _, l := range labelSets {
		out, err := h.run(ctx, nil, "docker", "ps", "--filter", "label="+l(agentproto.LabelApp)+"="+appID, "--filter", "label="+l(agentproto.LabelProcess)+"=web",
			"--filter", "label="+l(agentproto.LabelDeployment)+"="+deploymentID, "--format", "{{.Names}}")
		if err != nil {
			continue
		}
		listed = true
		for _, name := range strings.Split(string(out), "\n") {
			if name = strings.TrimSpace(name); name != "" && !strings.HasPrefix(name, "-") {
				return name, nil
			}
		}
	}
	if !listed {
		return "", errors.New("could not list the app's containers")
	}
	return "", errors.New("the app's live release has no running web container on this server — deploy or restart the app first")
}

func (h *execHost) appExec(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.ExecParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	name, err := h.liveWebContainer(oc.Ctx, p.AppID, p.DeploymentID)
	if err != nil {
		return nil, 1, err
	}
	res := agentproto.ExecResult{Container: name}
	oc.Info("running in %s (timeout %ds)", name, p.TimeoutSeconds)

	var mu sync.Mutex
	onLine := func(stream, line string) {
		mu.Lock()
		defer mu.Unlock()
		if res.Truncated {
			return
		}
		line = clip(line, maxLine)
		if res.Bytes+len(line)+1 > agentproto.MaxExecOutput {
			res.Truncated = true
			oc.Info("output truncated after %d KiB; the command keeps running", agentproto.MaxExecOutput>>10)
			return
		}
		res.Bytes += len(line) + 1
		oc.log(stream, line)
	}

	ctx, cancel := context.WithTimeout(oc.Ctx, time.Duration(p.TimeoutSeconds)*time.Second)
	defer cancel()
	// No -t, no -i. The mark goes in through docker's own -e; the argv follows the container name,
	// so none of it can be taken for an option of docker's.
	args := append([]string{"exec", "-e", execIDEnv + "=" + oc.OpID, name}, p.Argv...)
	code, err := h.stream(ctx, onLine, "docker", args...)

	if ctx.Err() != nil { // the timeout, or the operation was cancelled: nothing of it stays behind
		h.killInside(name, oc.OpID)
		res.ExitCode = agentproto.ExecExitTimeout
		if oc.Ctx.Err() != nil {
			return res, agentproto.ExecExitTimeout, oc.Ctx.Err()
		}
		res.TimedOut = true
		return res, agentproto.ExecExitTimeout, fmt.Errorf("the command was killed after its timeout of %d s", p.TimeoutSeconds)
	}
	if err != nil {
		return res, 1, fmt.Errorf("could not run the command in %s: %w", name, err)
	}
	res.ExitCode = code
	if code != 0 {
		return res, code, fmt.Errorf("the command exited with status %d", code)
	}
	return res, 0, nil
}

// killInside ends what a timed-out exec left running in the container. Best effort: an image
// without sh, tr or grep has nothing a schedule could have started with `sh -lc` either.
func (h *execHost) killInside(container, opID string) {
	if !agentproto.ValidID(opID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.killGrace)
	defer cancel()
	_, _ = h.run(ctx, nil, "docker", "exec", container, "sh", "-c", execKillScript, "sh", opID)
}
