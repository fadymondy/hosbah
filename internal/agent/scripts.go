package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// script.run (MH-576 D4): a team script or a built-in one, run with bash in a fresh temp directory
// as root. The body the control plane signed is written to a file and handed to `bash <file>` —
// never read by any shell from an untrusted string, and never templated on the agent. Args and env
// arrive as real environment variables (never an argv, never interpolated into the body), the
// secret ones opened from EnvBox with this server's own key exactly like a deploy's env.
type scriptHost struct {
	killGrace time.Duration
}

func newScriptHost() *scriptHost { return &scriptHost{killGrace: 15 * time.Second} }

func (h *scriptHost) run(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.ScriptRunParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	secretEnv, err := oc.OpenEnv(agentproto.ScriptEnvContext(p.ScriptRunID), p.EnvBox)
	if err != nil {
		return nil, 2, err
	}

	dir, err := os.MkdirTemp("", "hosbah-script-*")
	if err != nil {
		return nil, 1, fmt.Errorf("could not create a working directory: %w", err)
	}
	defer os.RemoveAll(dir)
	scriptPath := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(scriptPath, []byte(p.Body), 0o700); err != nil { //#nosec G306 -- the script must be executable by the bash we invoke
		return nil, 1, fmt.Errorf("could not write the script: %w", err)
	}

	extra := []string{"HOSBAH_SCRIPT_DIR=" + dir, "DEBIAN_FRONTEND=noninteractive", "LC_ALL=C"}
	for k, v := range p.Args {
		extra = append(extra, k+"="+v)
	}
	for k, v := range p.Env {
		extra = append(extra, k+"="+v)
	}
	for k, v := range secretEnv {
		extra = append(extra, k+"="+v)
	}

	oc.Info("running %s (timeout %ds)", firstNonEmpty(p.Name, "script"), p.TimeoutSeconds)

	res := agentproto.ScriptRunResult{}
	var mu sync.Mutex
	onLine := func(stream, line string) {
		mu.Lock()
		defer mu.Unlock()
		if res.Truncated {
			return
		}
		line = clip(line, maxLine)
		if res.Bytes+len(line)+1 > agentproto.MaxScriptOutput {
			res.Truncated = true
			oc.Info("output truncated after %d KiB; the script keeps running", agentproto.MaxScriptOutput>>10)
			return
		}
		res.Bytes += len(line) + 1
		oc.log(stream, line)
	}

	ctx, cancel := context.WithTimeout(oc.Ctx, time.Duration(p.TimeoutSeconds)*time.Second)
	defer cancel()
	code, runErr := runScript(ctx, extra, onLine, "bash", scriptPath)

	if ctx.Err() != nil {
		res.ExitCode = agentproto.ScriptExitTimeout
		if oc.Ctx.Err() != nil {
			return res, agentproto.ScriptExitTimeout, oc.Ctx.Err()
		}
		res.TimedOut = true
		return res, agentproto.ScriptExitTimeout, fmt.Errorf("the script was killed after its timeout of %d s", p.TimeoutSeconds)
	}
	res.ExitCode = code
	if runErr != nil && code == 0 {
		return res, 1, fmt.Errorf("could not run the script: %w", runErr)
	}
	if code != 0 {
		return res, code, fmt.Errorf("the script exited with status %d", code)
	}
	return res, 0, nil
}

// runScript runs name+args with the given extra environment (on top of the agent's own, sanitised
// by nothing more than the host it runs on), streaming stdout/stderr line by line to onLine, and
// killing the whole process group when ctx ends so nothing the script started (apt, a download) is
// left behind (configureGroup / killGroup, platform-specific: scripts_linux.go / scripts_other.go).
func runScript(ctx context.Context, extraEnv []string, onLine func(stream, line string), name string, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, name, args...) //#nosec G204 -- bash + a file the control plane signed; fixed argv
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.WaitDelay = 5 * time.Second
	configureGroup(cmd)
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
		_, _ = io.Copy(io.Discard, r)
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

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
