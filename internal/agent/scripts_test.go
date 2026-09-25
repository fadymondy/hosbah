package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// script.run (MH-576 D4) is exercised against a real bash subprocess (scriptHost has no injectable
// runner — it shells out to bash exactly as production does), matching runScript's actual contract.

func init() {
	if _, err := exec.LookPath("bash"); err != nil {
		panic("these tests require bash on PATH: " + err.Error())
	}
}

// scriptOpCtx is opCtx (workloads_test.go) plus a private key, so OpenEnv can open a sealed
// EnvBox the same way a real agent would.
func scriptOpCtx(t *testing.T, priv ed25519.PrivateKey, params any) (*OpContext, *[]string) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	var lines []string
	return &OpContext{Ctx: context.Background(), OpID: "op", ServerID: "srv", priv: priv, Params: raw,
		log:      func(_, l string) { lines = append(lines, l) },
		progress: func(int, int) {}}, &lines
}

const testScriptRunID = "00000000-0000-0000-0000-000000000001"

func TestScriptRunPassesArgsEnvAndSecretsAsEnvVars(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	secretJSON, _ := json.Marshal(map[string]string{"MY_SECRET": "s3cr3t-marker"})
	box, err := agentproto.SealBox(pubB64(pub), agentproto.ScriptEnvContext(testScriptRunID), secretJSON)
	if err != nil {
		t.Fatalf("seal env box: %v", err)
	}

	h := newScriptHost()
	oc, lines := scriptOpCtx(t, priv, agentproto.ScriptRunParams{
		ScriptRunID:    testScriptRunID,
		Name:           "env-check",
		Body:           "#!/bin/bash\necho \"ARG=$MY_ARG ENV=$MY_ENV SECRET=$MY_SECRET\"\n",
		Args:           map[string]string{"MY_ARG": "arg-value"},
		Env:            map[string]string{"MY_ENV": "env-value"},
		EnvBox:         box,
		TimeoutSeconds: 10,
	})
	res, code, err := h.run(oc)
	if err != nil || code != 0 {
		t.Fatalf("run: %v (%d) lines=%v", err, code, *lines)
	}
	out := res.(agentproto.ScriptRunResult)
	if out.ExitCode != 0 || out.TimedOut || out.Truncated {
		t.Fatalf("result: %+v", out)
	}
	joined := strings.Join(*lines, "\n")
	if !strings.Contains(joined, "ARG=arg-value") || !strings.Contains(joined, "ENV=env-value") || !strings.Contains(joined, "SECRET=s3cr3t-marker") {
		t.Fatalf("expected args/env/secret in output, got %q", joined)
	}
	// None of it belongs in the body the control plane signed — Body is fixed and never templated.
	if strings.Contains(string(oc.Params), "s3cr3t-marker") {
		t.Fatalf("secret leaked into params")
	}
}

func TestScriptRunPropagatesExitCode(t *testing.T) {
	h := newScriptHost()
	oc, _ := scriptOpCtx(t, nil, agentproto.ScriptRunParams{
		ScriptRunID:    testScriptRunID,
		Body:           "#!/bin/bash\nexit 7\n",
		TimeoutSeconds: 10,
	})
	res, code, err := h.run(oc)
	if err == nil {
		t.Fatalf("expected an error for a non-zero exit")
	}
	if code != 7 {
		t.Fatalf("want handler code 7, got %d", code)
	}
	if res.(agentproto.ScriptRunResult).ExitCode != 7 {
		t.Fatalf("result: %+v", res)
	}
}

func TestScriptRunKillsOnTimeout(t *testing.T) {
	h := newScriptHost()
	oc, _ := scriptOpCtx(t, nil, agentproto.ScriptRunParams{
		ScriptRunID:    testScriptRunID,
		Body:           "#!/bin/bash\nsleep 30\n",
		TimeoutSeconds: agentproto.MinScriptTimeoutSeconds,
	})
	start := time.Now()
	res, code, err := h.run(oc)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected a timeout error")
	}
	if code != agentproto.ScriptExitTimeout {
		t.Fatalf("want code %d, got %d", agentproto.ScriptExitTimeout, code)
	}
	out := res.(agentproto.ScriptRunResult)
	if !out.TimedOut || out.ExitCode != agentproto.ScriptExitTimeout {
		t.Fatalf("result: %+v", out)
	}
	// The whole-process-group kill (configureGroup, scripts_linux.go) is Linux-only, matching
	// CLAUDE.md: the agent only ever ships built for Linux. Off Linux, ctx cancellation still ends
	// the direct child eventually via WaitDelay, but not necessarily promptly, so only assert the
	// timing on the platform the kill path is actually built for.
	if runtime.GOOS == "linux" && elapsed > 20*time.Second {
		t.Fatalf("took %s — the process group was not killed promptly", elapsed)
	}
}

func TestScriptRunTruncatesOutputPastTheCap(t *testing.T) {
	h := newScriptHost()
	// Each line is ~301 bytes counted (300 chars + the separator byte onLine adds); 5000 of them
	// is comfortably past agentproto.MaxScriptOutput (1 MiB), and the script still exits cleanly
	// afterwards so this also proves truncation does not stop the run.
	body := "#!/bin/bash\nline=$(printf 'x%.0s' $(seq 1 300))\nfor i in $(seq 1 5000); do echo \"$line\"; done\nexit 0\n"
	oc, lines := scriptOpCtx(t, nil, agentproto.ScriptRunParams{
		ScriptRunID:    testScriptRunID,
		Body:           body,
		TimeoutSeconds: 30,
	})
	res, code, err := h.run(oc)
	if err != nil || code != 0 {
		t.Fatalf("run: %v (%d)", err, code)
	}
	out := res.(agentproto.ScriptRunResult)
	if !out.Truncated {
		t.Fatalf("result: %+v", out)
	}
	if out.Bytes > agentproto.MaxScriptOutput {
		t.Fatalf("kept %d bytes, want at most %d", out.Bytes, agentproto.MaxScriptOutput)
	}
	if len(*lines) >= 5000 {
		t.Fatalf("expected fewer than 5000 output lines to be kept, got %d", len(*lines))
	}
}
