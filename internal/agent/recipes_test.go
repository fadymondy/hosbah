package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fadymondy/hosbah/catalog"
	"github.com/fadymondy/hosbah/internal/agentproto"
)

const (
	inID     = "00000000-0000-4000-8000-0000000000e1"
	inName   = "hosbah-install-" + inID
	inSecret = "SecretMarker0123456789SecretMark" // 32 alphanumerics
	inDBURL  = "postgres://app:DbPassMarker99@172.17.0.1:5432/app"
)

// The agent's own catalog in these tests: one recipe that uses everything the format has.
const testRecipe = `
id: demo
name: Demo
summary: A demo.
description: A demo recipe.
homepage: https://example.com
licence: MIT
kind: app
version: "1.2.0"
image: example/demo:4.5.6
port: 8080
healthcheck: /healthz
env:
  - name: DEMO_KEY
    generate: { length: 32, charset: alnum }
    secret: true
  - name: DEMO_AUTH
    value: "admin:{DEMO_KEY}"
    secret: true
  - name: DATABASE_URL
    from: postgres_url
    secret: true
  - name: DEMO_MODE
    value: production
volumes:
  - name: data
    mount_path: /data
  - name: cache
    mount_path: /var/cache/demo
limits: { memory_mb: 384, cpus: 0.75 }
caps: [CHOWN, DAC_OVERRIDE]
needs: { postgres: true }
`

type recipeFake struct {
	h       *recipeHost
	f       *fakeRunner
	envFile string // what the env file held while `docker run` ran
	envPath string
	envMode os.FileMode
	healthy []string
}

func newRecipeFake(t *testing.T, answers map[string]string) *recipeFake {
	t.Helper()
	cat, err := catalog.LoadFS(fstest.MapFS{"r/demo.yaml": {Data: []byte(testRecipe)}}, "r")
	if err != nil {
		t.Fatal(err)
	}
	rf := &recipeFake{f: &fakeRunner{answers: answers, fail: map[string]string{}}}
	rf.h = &recipeHost{dir: filepath.Join(t.TempDir(), "installs"), catalog: func() (*catalog.Catalog, error) { return cat, nil },
		healthy: func(_ context.Context, port int, path string) error {
			rf.healthy = append(rf.healthy, path)
			return nil
		}}
	rf.h.run = func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if name == "docker" && len(args) > 0 && args[0] == "run" && a == "--env-file" && i+1 < len(args) {
				rf.envPath = args[i+1]
				if st, err := os.Stat(rf.envPath); err == nil {
					rf.envMode = st.Mode().Perm()
				}
				b, _ := os.ReadFile(rf.envPath)
				rf.envFile = string(b)
			}
		}
		return rf.f.run(ctx, env, name, args...)
	}
	rf.h.stream = func(oc *OpContext, name string, args ...string) (int, error) {
		oc.log("stdout", "4.5.6: Pulling from example/demo")
		if _, err := rf.f.run(oc.Ctx, nil, name, args...); err != nil {
			return 1, err
		}
		return 0, nil
	}
	return rf
}

func installParams(t *testing.T) (agentproto.RecipeInstallParams, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	sec, _ := json.Marshal(agentproto.InstallSecrets{Env: map[string]string{"DEMO_KEY": inSecret, "DATABASE_URL": inDBURL}})
	box, err := agentproto.SealBox(pubB64(pub), agentproto.InstallSecretsContext(inID), sec)
	if err != nil {
		t.Fatal(err)
	}
	return agentproto.RecipeInstallParams{InstallID: inID, AppID: stAppID, RecipeID: "demo", RecipeVersion: "1.2.0", Image: "example/demo:4.5.6", Secrets: box}, priv
}

// The exact docker argv — limits, loopback-only ephemeral publish, no-new-privileges, every
// capability dropped and only the recipe's added, derived volumes — and the secrets delivered through
// a 0600 env file that is gone afterwards: never an argv, never the log, never the result.
func TestRecipeInstallRunsTheAgentsOwnRecipeLockedDownWithSecretsInAnEnvFile(t *testing.T) {
	rf := newRecipeFake(t, map[string]string{"docker port " + inName + " 8080/tcp": "127.0.0.1:49200\n"})
	rf.f.fail["docker image inspect"] = "Error: No such image"
	p, priv := installParams(t)
	oc, lines := opCtx(t, p)
	oc.priv = priv
	res, _, err := rf.h.install(oc)
	if err != nil {
		t.Fatal(err)
	}
	if r := res.(agentproto.RecipeInstallResult); r.HostPort != 49200 || r.Container != inName {
		t.Fatalf("result: %+v", r)
	}
	want := "docker run -d --restart unless-stopped --name " + inName +
		" --label hosbah.install=" + inID + " --label hosbah.install-app=" + stAppID + " --label hosbah.install-recipe=demo@1.2.0" +
		" --log-opt max-size=10m --log-opt max-file=3 --memory 384m --cpus 0.75 --pids-limit 512" +
		" --security-opt no-new-privileges --cap-drop ALL --cap-add CHOWN --cap-add DAC_OVERRIDE" +
		" --mount type=volume,source=hosbah-ivol-" + inID + "-data,target=/data" +
		" --mount type=volume,source=hosbah-ivol-" + inID + "-cache,target=/var/cache/demo" +
		" --env-file " + filepath.Join(rf.h.dir, inID+".env") + " -p 127.0.0.1::8080 example/demo:4.5.6"
	var run string
	pulled := false
	for _, c := range rf.f.calls {
		if strings.HasPrefix(c.argv, "docker run ") {
			run = c.argv
		}
		pulled = pulled || c.argv == "docker pull example/demo:4.5.6"
		for _, s := range []string{inSecret, "DbPassMarker99"} {
			if strings.Contains(c.argv, s) || strings.Contains(strings.Join(c.env, " "), s) {
				t.Errorf("a secret reached an argv or a process env: %s %v", c.argv, c.env)
			}
		}
		for _, bad := range []string{"0.0.0.0", "--privileged", "--network", "--pid ", "--pid=", "--device", " -v ", "type=bind", "hosbah.app=", " -e "} {
			if strings.Contains(c.argv, bad) {
				t.Errorf("forbidden in an argv (%q): %s", bad, c.argv)
			}
		}
	}
	if run != want {
		t.Errorf("docker run argv\n got: %s\nwant: %s", run, want)
	}
	if !pulled {
		t.Error("a missing image must be pulled")
	}
	wantEnv := "DATABASE_URL=" + inDBURL + "\nDEMO_AUTH=admin:" + inSecret + "\nDEMO_KEY=" + inSecret + "\nDEMO_MODE=production\n"
	if rf.envFile != wantEnv {
		t.Errorf("env file\n got: %q\nwant: %q", rf.envFile, wantEnv)
	}
	if rf.envMode&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("the env file must be 0600, is %o", rf.envMode)
	}
	if _, err := os.Stat(rf.envPath); !os.IsNotExist(err) {
		t.Errorf("the env file must be removed once docker has read it: %v", err)
	}
	log := strings.Join(*lines, "\n")
	raw, _ := json.Marshal(res)
	for _, s := range []string{inSecret, "DbPassMarker99"} {
		if strings.Contains(log, s) || strings.Contains(string(raw), s) {
			t.Errorf("a secret is in the op log or the result")
		}
	}
	if !strings.Contains(log, "Pulling from") {
		t.Errorf("pull progress must reach the op log: %s", log)
	}
	if len(rf.healthy) != 1 || rf.healthy[0] != "/healthz" {
		t.Errorf("the recipe's health path must be awaited: %v", rf.healthy)
	}
}

// The image reference on the wire is only ever COMPARED. A different image, version or an unknown
// recipe is refused before docker is touched, with an error that says what to do.
func TestRecipeInstallRefusesWhatDoesNotMatchTheAgentsCatalog(t *testing.T) {
	for name, c := range map[string]struct {
		mut  func(*agentproto.RecipeInstallParams)
		want string
	}{
		"another image":   {func(p *agentproto.RecipeInstallParams) { p.Image = "evil/miner:1.0.0" }, "is not the image of recipe demo"},
		"another tag":     {func(p *agentproto.RecipeInstallParams) { p.Image = "example/demo:4.5.7" }, "update the agent"},
		"another version": {func(p *agentproto.RecipeInstallParams) { p.RecipeVersion = "1.3.0" }, "update the agent"},
		"unknown recipe":  {func(p *agentproto.RecipeInstallParams) { p.RecipeID = "newer-recipe" }, "update the agent"},
	} {
		rf := newRecipeFake(t, nil)
		p, priv := installParams(t)
		c.mut(&p)
		oc, _ := opCtx(t, p)
		oc.priv = priv
		_, _, err := rf.h.install(oc)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, c.want)
		}
		if len(rf.f.calls) != 0 {
			t.Errorf("%s: docker was touched: %v", name, rf.f.calls)
		}
	}
}

// The box may bring only what the recipe takes, and must bring all of it; a box for another install
// does not open.
func TestRecipeInstallChecksTheSecretsAgainstTheRecipe(t *testing.T) {
	seal := func(t *testing.T, pub ed25519.PublicKey, ctxID string, env map[string]string) string {
		raw, _ := json.Marshal(agentproto.InstallSecrets{Env: env})
		box, err := agentproto.SealBox(pubB64(pub), agentproto.InstallSecretsContext(ctxID), raw)
		if err != nil {
			t.Fatal(err)
		}
		return box
	}
	pub, priv, _ := ed25519.GenerateKey(nil)
	for name, c := range map[string]struct {
		box  string
		want string
	}{
		"an undeclared variable": {seal(t, pub, inID, map[string]string{"DEMO_KEY": inSecret, "DATABASE_URL": inDBURL, "LD_PRELOAD": "/x.so"}), "does not take"},
		"overriding a literal":   {seal(t, pub, inID, map[string]string{"DEMO_KEY": inSecret, "DATABASE_URL": inDBURL, "DEMO_MODE": "debug"}), "does not take"},
		"a missing value":        {seal(t, pub, inID, map[string]string{"DEMO_KEY": inSecret}), "no value for DATABASE_URL"},
		"a newline":              {seal(t, pub, inID, map[string]string{"DEMO_KEY": inSecret, "DATABASE_URL": "x\nLD_PRELOAD=/x.so"}), "malformed"},
		"another install's box":  {seal(t, pub, "00000000-0000-4000-8000-0000000000e2", map[string]string{"DEMO_KEY": inSecret, "DATABASE_URL": inDBURL}), "could not be opened"},
	} {
		rf := newRecipeFake(t, nil)
		p, _ := installParams(t)
		p.Secrets = c.box
		oc, _ := opCtx(t, p)
		oc.priv = priv
		if _, _, err := rf.h.install(oc); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, c.want)
		}
		for _, call := range rf.f.calls {
			if strings.HasPrefix(call.argv, "docker run") {
				t.Errorf("%s: the container was started", name)
			}
		}
	}
}

// A start that fails cleans up, and what docker said is scrubbed before it is logged.
func TestRecipeInstallFailureRemovesTheContainerAndScrubsTheLog(t *testing.T) {
	rf := newRecipeFake(t, map[string]string{"docker logs --tail 20 " + inName: "boot: key=" + inSecret + "\n"})
	rf.f.fail["docker run"] = "docker: invalid env DEMO_KEY=" + inSecret
	p, priv := installParams(t)
	oc, lines := opCtx(t, p)
	oc.priv = priv
	if _, _, err := rf.h.install(oc); err == nil || strings.Contains(err.Error(), inSecret) {
		t.Fatalf("want a failure without the secret, got %v", err)
	}
	if log := strings.Join(*lines, "\n"); strings.Contains(log, inSecret) || !strings.Contains(log, "invalid env") {
		t.Errorf("the log must carry the scrubbed detail: %s", log)
	}
	if argv := strings.Join(rf.f.mutating(), "\n"); !strings.HasSuffix(argv, "docker rm -f "+inName) {
		t.Errorf("the failed container must be removed:\n%s", argv)
	}
	if _, err := os.Stat(rf.envPath); !os.IsNotExist(err) {
		t.Errorf("the env file must be removed after a failed start too: %v", err)
	}
}

// recipe.remove: the container always; the volumes only with delete_data, found by the install's
// derived prefix (docker's name filter is a substring match, so every hit is checked again).
func TestRecipeRemoveKeepsTheVolumesUnlessAsked(t *testing.T) {
	other := "hosbah-ivol-00000000-0000-4000-8000-0000000000e9-data"
	answers := map[string]string{"docker volume ls -q --filter name=hosbah-ivol-" + inID + "-": "hosbah-ivol-" + inID + "-data\nhosbah-ivol-" + inID + "-cache\n" + other + "\nx-hosbah-ivol-" + inID + "-data\n"}
	rf := newRecipeFake(t, answers)
	oc, _ := opCtx(t, agentproto.RecipeRemoveParams{InstallID: inID, AppID: stAppID})
	res, _, err := rf.h.remove(oc)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(rf.f.mutating(), "\n")
	if !strings.Contains(argv, "docker rm -f "+inName) || strings.Contains(argv, "volume") {
		t.Errorf("without delete_data only the container goes:\n%s", argv)
	}
	if r := res.(agentproto.RecipeRemoveResult); !r.Removed || len(r.VolumesRemoved) != 0 {
		t.Errorf("result: %+v", r)
	}

	rf = newRecipeFake(t, answers)
	oc, _ = opCtx(t, agentproto.RecipeRemoveParams{InstallID: inID, AppID: stAppID, DeleteData: true})
	res, _, err = rf.h.remove(oc)
	if err != nil {
		t.Fatal(err)
	}
	argv = strings.Join(rf.f.mutating(), "\n")
	for _, v := range []string{"hosbah-ivol-" + inID + "-data", "hosbah-ivol-" + inID + "-cache"} {
		if !strings.Contains(argv, "docker volume rm "+v+"\n") && !strings.HasSuffix(argv, "docker volume rm "+v) {
			t.Errorf("%s was not removed:\n%s", v, argv)
		}
	}
	if strings.Contains(argv, "docker volume rm "+other) || strings.Contains(argv, "docker volume rm x-") {
		t.Errorf("another install's volume was removed:\n%s", argv)
	}
	if r := res.(agentproto.RecipeRemoveResult); len(r.VolumesRemoved) != 2 {
		t.Errorf("result: %+v", r)
	}

	// Already gone: still ok.
	rf = newRecipeFake(t, nil)
	rf.f.fail["docker rm -f"] = "Error response from daemon: No such container: " + inName
	oc, _ = opCtx(t, agentproto.RecipeRemoveParams{InstallID: inID, AppID: stAppID})
	if res, _, err := rf.h.remove(oc); err != nil || res.(agentproto.RecipeRemoveResult).Removed {
		t.Errorf("an install that is already gone: %+v %v", res, err)
	}
}

// app.remove takes the app's installs with it — found by label, since they do not carry hosbah.app.
func TestAppRemoveTakesTheAppsInstalls(t *testing.T) {
	second := "00000000-0000-4000-8000-0000000000e2"
	rf := newRecipeFake(t, map[string]string{
		"docker ps -a --filter label=hosbah.install-app=" + stAppID: inID + "\n" + second + "\nnot-an-id\n"})
	oc, _ := opCtx(t, agentproto.RemoveParams{AppID: stAppID, App: "site"})
	res, _, err := rf.h.withAppRemove(func(*OpContext) (any, int, error) { return map[string]any{}, 0, nil })(oc)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(rf.f.mutating(), "\n")
	for _, id := range []string{inID, second} {
		if !strings.Contains(argv, "docker rm -f hosbah-install-"+id) {
			t.Errorf("%s was not removed:\n%s", id, argv)
		}
	}
	if strings.Contains(argv, "not-an-id") || strings.Contains(argv, "volume rm") {
		t.Errorf("only validated ids, and never the volumes:\n%s", argv)
	}
	if got := res.(map[string]any)["installs"].([]string); len(got) != 2 {
		t.Errorf("result: %v", res)
	}
}

func TestRecipeStatusReportsTheCurrentLoopbackPort(t *testing.T) {
	rf := newRecipeFake(t, map[string]string{
		"docker inspect --type container --format {{.State.Running}} {{.RestartCount}} " + inName: "true 2\n",
		"docker port " + inName + " 8080/tcp":                                                     "127.0.0.1:49777\n",
		"docker stats":                                                                            inName + "\t120MiB / 384MiB\n",
	})
	oc, _ := opCtx(t, agentproto.RecipeStatusParams{AppID: stAppID, InstallIDs: []string{inID}, RecipeIDs: []string{"demo"}})
	res, _, err := rf.h.status(oc)
	if err != nil {
		t.Fatal(err)
	}
	st := res.(agentproto.RecipeStatusResult).Installs
	if len(st) != 1 || !st[0].Running || st[0].Restarts != 2 || st[0].HostPort != 49777 || st[0].MemoryBytes == nil || *st[0].MemoryBytes != 120<<20 {
		t.Errorf("status: %+v", st)
	}
	if len(rf.f.mutating()) != 1 { // docker port only
		t.Errorf("recipe.status must not change anything: %v", rf.f.mutating())
	}
}

// The embedded catalog this binary ships is the one recipe.install runs: every app recipe in it
// yields an argv with the limits and without anything the format has no field for.
func TestEveryEmbeddedAppRecipeYieldsALockedDownArgv(t *testing.T) {
	c, err := catalog.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range c.List(catalog.KindApp, "") {
		argv := strings.Join(installRunArgs(agentproto.RecipeInstallParams{InstallID: inID, AppID: stAppID}, r, "/var/lib/hosbah/installs/x.env", nil), " ")
		for _, must := range []string{"--memory ", "--cpus ", "--pids-limit 512", "--security-opt no-new-privileges", "--cap-drop ALL", "--restart unless-stopped"} {
			if !strings.Contains(argv, must) {
				t.Errorf("%s: %q is missing: %s", r.ID, must, argv)
			}
		}
		if r.Port > 0 && !strings.Contains(argv, " -p 127.0.0.1::") {
			t.Errorf("%s: the port must be published on loopback only: %s", r.ID, argv)
		}
		if !strings.HasSuffix(argv, " "+r.Image) {
			t.Errorf("%s: the image is the last argument, nothing follows it: %s", r.ID, argv)
		}
	}
}
