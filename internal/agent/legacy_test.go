package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/catalog"
	"github.com/fadymondy/hosbah/internal/agentproto"
)

// Servers set up before the rename from Cloudy hold containers, volumes and labels named cloudy-….
// These tests pin that the agent still finds, stops and reuses them.

// A derived volume that exists under its old name keeps that name — its data stays where it is —
// and one that does not gets the new name.
func TestAnOldNamedVolumeIsReused(t *testing.T) {
	cur := agentproto.VolumeName(stAppID, "data")
	old := agentproto.LegacyName(cur)
	if !strings.HasPrefix(cur, "hosbah-vol-") || !strings.HasPrefix(old, "cloudy-vol-") {
		t.Fatalf("names: %s %s", cur, old)
	}
	f := &fakeRunner{answers: map[string]string{"docker volume inspect --format {{.Name}} " + old: old + "\n"}, fail: map[string]string{}}
	for _, asked := range []string{cur, old} { // whichever form the control plane sent
		if got := resolveName(context.Background(), f.run, kindVolume, asked); got != old {
			t.Errorf("%s: got %s, want the existing %s", asked, got, old)
		}
	}
	mounts := resolveMounts(context.Background(), f.run, []agentproto.Mount{{Volume: cur, MountPath: "/data"}})
	if mounts[0].Volume != old {
		t.Errorf("a deploy mounts %s, want %s", mounts[0].Volume, old)
	}
	args, _ := startArgs(agentproto.StartParams{AppID: stAppID, App: "shop", DeploymentID: stDepID, Image: "hosbah/shop:" + stDepID,
		Process: "worker:queue", Mounts: mounts}, "hosbah-shop-worker-queue-x", nil)
	if !strings.Contains(strings.Join(args, " "), "source="+old+",") {
		t.Errorf("docker run: %v", args)
	}

	none := &fakeRunner{answers: map[string]string{}, fail: map[string]string{"docker volume inspect": "Error: No such volume"}}
	for _, asked := range []string{cur, old} {
		if got := resolveName(context.Background(), none.run, kindVolume, asked); got != cur {
			t.Errorf("a new volume is %s, want %s", got, cur)
		}
	}
	// A name that is not Hosbah's (an imported service's container) is left alone and not looked up.
	quiet := &fakeRunner{answers: map[string]string{}, fail: map[string]string{}}
	if got := resolveName(context.Background(), quiet.run, kindContainer, "postgres-main"); got != "postgres-main" || len(quiet.calls) != 0 {
		t.Errorf("foreign name: %s %v", got, quiet.calls)
	}
}

// volume.usage and volume.remove find the old-named volume; the result keeps the name asked for.
func TestVolumeOpsFindTheOldNamedVolume(t *testing.T) {
	cur := agentproto.VolumeName(stAppID, "data")
	old := agentproto.LegacyName(cur)
	h, f := newStorageFake(map[string]string{
		"docker system df -v --format json":               `{"Volumes":[{"Name":"` + old + `","Links":"1","Size":"2MB"}]}`,
		"docker volume inspect --format {{.Name}} " + old: old + "\n",
	})
	oc, _ := opCtx(t, agentproto.VolumeUsageParams{AppID: stAppID, Volumes: []string{cur}})
	res, _, err := h.volumeUsage(oc)
	if err != nil {
		t.Fatal(err)
	}
	u := res.(agentproto.VolumeUsageResult).Volumes[0]
	if u.Volume != cur || !u.Exists || u.Bytes == nil || *u.Bytes != 2_000_000 {
		t.Fatalf("usage: %+v", u)
	}
	oc, _ = opCtx(t, agentproto.VolumeRemoveParams{AppID: stAppID, Volume: cur})
	if _, _, err := h.volumeRemove(oc); err != nil {
		t.Fatal(err)
	}
	if last := f.calls[len(f.calls)-1].argv; last != "docker volume rm "+old {
		t.Fatalf("removed %q, want the old-named volume", last)
	}
}

// A drain (and so a deploy's switch, a rollback and app.remove) stops the app's containers under
// both label sets: the ones this agent labelled hosbah.app and the ones an older agent labelled
// cloudy.app.
func TestDrainStopsOldLabelledContainersToo(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{
		"docker ps --filter label=hosbah.app=" + stAppID: "hosbah-shop-web-bbbbbbbb\n",
		"docker ps --filter label=cloudy.app=" + stAppID: "cloudy-shop-web-aaaaaaaa\ncloudy-shop-worker-queue-aaaaaaaa\n",
	}, fail: map[string]string{}}
	var steps []string
	oldDocker, oldStep := appDocker, dockerStep
	t.Cleanup(func() { appDocker, dockerStep = oldDocker, oldStep })
	appDocker = f.run
	dockerStep = func(_ *OpContext, args ...string) (int, error) {
		steps = append(steps, strings.Join(args, " "))
		return 0, nil
	}
	oc, _ := opCtx(t, agentproto.DrainParams{AppID: stAppID})
	stopped, _, err := drain(oc, agentproto.DrainParams{AppID: stAppID, Keep: []string{"hosbah-shop-web-bbbbbbbb"}, GraceSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(stopped, ",") != "cloudy-shop-web-aaaaaaaa,cloudy-shop-worker-queue-aaaaaaaa" {
		t.Fatalf("stopped %v", stopped)
	}
	for _, want := range []string{"stop -t 5 cloudy-shop-web-aaaaaaaa", "rm -f cloudy-shop-web-aaaaaaaa", "rm -f cloudy-shop-worker-queue-aaaaaaaa"} {
		if !strings.Contains(strings.Join(steps, "\n"), want) {
			t.Errorf("missing %q in %v", want, steps)
		}
	}
	// Both generations were asked, with the same filters.
	var ps []string
	for _, c := range f.calls {
		ps = append(ps, c.argv)
	}
	if len(ps) != 2 || !strings.Contains(ps[0], "label=hosbah.app=") || !strings.Contains(ps[1], "label=cloudy.app=") ||
		!strings.Contains(ps[1], " -a") {
		t.Fatalf("docker ps calls: %v", ps)
	}

	// One generation failing to list does not hide the other.
	f.fail["docker ps --filter label=hosbah.app="] = "boom"
	names, err := appContainers(oc, stAppID, "web", true)
	if err != nil || len(names) != 2 {
		t.Fatalf("%v %v", names, err)
	}
}

// app.exec finds the live web container of a release an older agent started (cloudy.* labels).
func TestExecFindsAnOldLabelledWebContainer(t *testing.T) {
	legacyPS := "docker ps --filter label=cloudy.app=" + execApp + " --filter label=cloudy.process=web --filter label=cloudy.deployment=" + execDep
	h, _ := newExecFake(map[string]string{legacyPS: "cloudy-shop-web-22222222\n"}, &fakeStream{})
	got, err := h.liveWebContainer(context.Background(), execApp, execDep)
	if err != nil || got != "cloudy-shop-web-22222222" {
		t.Fatalf("%q %v", got, err)
	}
}

// An install and a browser made before the rename are found under their old container names, and
// an install's old volumes are mounted again when it is re-installed.
func TestOldNamedInstallsAndBrowsersAreFound(t *testing.T) {
	oldInstall := agentproto.LegacyName(agentproto.InstallContainerName(inID))
	oldVol := agentproto.LegacyName(agentproto.InstallVolumeName(inID, "data"))
	rf := newRecipeFake(t, map[string]string{
		"docker container inspect --format {{.Name}} " + oldInstall: "/" + oldInstall + "\n",
		"docker volume inspect --format {{.Name}} " + oldVol:        oldVol + "\n",
	})
	if got := rf.h.containerOf(context.Background(), inID); got != oldInstall {
		t.Fatalf("install container: %s", got)
	}
	args := strings.Join(installRunArgs(agentproto.RecipeInstallParams{InstallID: inID, AppID: stAppID}, mustRecipe(t, rf), "", func(slug string) string {
		return resolveName(context.Background(), rf.f.run, kindVolume, agentproto.InstallVolumeName(inID, slug))
	}), " ")
	if !strings.Contains(args, "source="+oldVol+",target=/data") || !strings.Contains(args, "source=hosbah-ivol-"+inID+"-cache,") {
		t.Fatalf("re-install mounts: %s", args)
	}
	oc, _ := opCtx(t, agentproto.RecipeRemoveParams{InstallID: inID, AppID: stAppID})
	res, _, err := rf.h.remove(oc)
	if err != nil || res.(agentproto.RecipeRemoveResult).Container != oldInstall {
		t.Fatalf("remove: %+v %v", res, err)
	}
	if !strings.Contains(strings.Join(rf.f.mutating(), "\n"), "docker rm -f "+oldInstall) {
		t.Fatalf("the old container was not removed: %v", rf.f.mutating())
	}

	const brID2 = "00000000-0000-4000-8000-0000000000b7"
	oldBrowser := agentproto.LegacyName(agentproto.BrowserContainerName(brID2))
	bf := &fakeRunner{answers: map[string]string{"docker container inspect --format {{.Name}} " + oldBrowser: "/" + oldBrowser + "\n"}, fail: map[string]string{}}
	bh := &browserHost{run: bf.run, dir: t.TempDir(), gateways: map[string]*browserGateway{}}
	if got := bh.containerOf(context.Background(), brID2); got != oldBrowser {
		t.Fatalf("browser container: %s", got)
	}
}

func mustRecipe(t *testing.T, rf *recipeFake) catalog.Recipe {
	t.Helper()
	c, err := rf.h.catalog()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := c.Get("demo")
	if !ok {
		t.Fatal("no demo recipe")
	}
	return r
}

// bucket.remove removes the key's policy under both names (a key made before the rename has a
// cloudy-bucket-… policy).
func TestBucketRemoveRemovesTheOldNamedPolicyToo(t *testing.T) {
	if !agentproto.ValidBucketPolicy("cbAbCdEfGh0123456789", "cloudy-bucket-cbAbCdEfGh0123456789") ||
		!agentproto.ValidBucketPolicy("cbAbCdEfGh0123456789", "hosbah-bucket-cbAbCdEfGh0123456789") ||
		agentproto.ValidBucketPolicy("cbAbCdEfGh0123456789", "other-bucket-cbAbCdEfGh0123456789") {
		t.Fatal("ValidBucketPolicy")
	}
}

// ---- first start after the rename: state directories -------------------------------------------

// recordingFS moves real directories (rename works everywhere) and records symlinks instead of
// making them (Windows needs a privilege for that; the real one is exercised on Linux below).
func recordingFS(links *[]string) fsOps {
	return fsOps{lstat: os.Lstat, rename: os.Rename, symlink: func(oldname, newname string) error {
		*links = append(*links, newname+" -> "+oldname)
		return nil
	}}
}

func TestStateDirsMoveOnceAndLeaveALink(t *testing.T) {
	root := t.TempDir()
	from, to := filepath.Join(root, "etc", "cloudy"), filepath.Join(root, "etc", "hosbah")
	if err := os.MkdirAll(filepath.Join(from, "gh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(from, "agent.json"), []byte(`{"server_id":"s"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var links []string
	notes := migrateDirs(recordingFS(&links), []dirMove{{from, to}})
	if len(notes) != 1 || !strings.Contains(notes[0], "moved") {
		t.Fatalf("notes: %v", notes)
	}
	if b, err := os.ReadFile(filepath.Join(to, "agent.json")); err != nil || string(b) != `{"server_id":"s"}` {
		t.Fatalf("the identity did not move: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(to, "gh")); err != nil {
		t.Fatal("the gh login did not move with it")
	}
	if len(links) != 1 || links[0] != from+" -> "+to {
		t.Fatalf("links: %v", links)
	}
	// Idempotent: the new directory exists, so a second start changes nothing.
	if notes := migrateDirs(recordingFS(&links), []dirMove{{from, to}}); len(notes) != 0 || len(links) != 1 {
		t.Fatalf("second run: %v %v", notes, links)
	}
	// A server enrolled after the rename has no old directory: nothing happens.
	if notes := migrateDirs(recordingFS(&links), []dirMove{{filepath.Join(root, "none"), filepath.Join(root, "new")}}); len(notes) != 0 {
		t.Fatalf("fresh server: %v", notes)
	}
}

// A directory that cannot be moved (a mount point) stays, and the new path links to it; one that
// moved but could not be linked goes back, so old paths never break.
func TestStateDirThatCannotMoveIsLinkedInstead(t *testing.T) {
	root := t.TempDir()
	from, to := filepath.Join(root, "cloudy"), filepath.Join(root, "hosbah")
	if err := os.Mkdir(from, 0o700); err != nil {
		t.Fatal(err)
	}
	var links []string
	busy := recordingFS(&links)
	busy.rename = func(string, string) error { return errors.New("device or resource busy") }
	if notes := migrateDirs(busy, []dirMove{{from, to}}); len(notes) != 1 || !strings.Contains(notes[0], "kept") || links[0] != to+" -> "+from {
		t.Fatalf("%v %v", notes, links)
	}

	links = nil
	nolink := recordingFS(&links)
	calls := 0
	nolink.symlink = func(oldname, newname string) error {
		calls++
		if calls == 1 {
			return errors.New("read-only file system")
		}
		links = append(links, newname+" -> "+oldname)
		return nil
	}
	migrateDirs(nolink, []dirMove{{from, to}})
	if fi, err := os.Stat(from); err != nil || !fi.IsDir() {
		t.Fatalf("the directory must be back at %s: %v", from, err)
	}
	if len(links) != 1 || links[0] != to+" -> "+from {
		t.Fatalf("links: %v", links)
	}
}

// With real symlinks (Linux, or Windows with the privilege), the old path reads through to the new one.
func TestStateDirMoveWithRealSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(root, filepath.Join(root, "probe")); err != nil {
		t.Skip("symlinks need a privilege on this system:", err)
	}
	from, to := filepath.Join(root, "cloudy"), filepath.Join(root, "hosbah")
	if err := os.MkdirAll(filepath.Join(from, "restic"), 0o700); err != nil {
		t.Fatal(err)
	}
	migrateDirs(hostFS, []dirMove{{from, to}})
	fi, err := os.Lstat(from)
	if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("%s is not a link: %v", from, err)
	}
	if _, err := os.Stat(filepath.Join(from, "restic")); err != nil {
		t.Fatalf("the old path does not reach the moved directory: %v", err)
	}
}

// ---- first start after the rename: the systemd unit ---------------------------------------------

type fakeService struct {
	calls   []string
	fail    map[string]error
	written map[string]string
}

func (f *fakeService) host(self, cgroup string, systemd bool) serviceHost {
	f.written = map[string]string{}
	rec := func(s string) error { f.calls = append(f.calls, s); return f.fail[s] }
	return serviceHost{
		self:    self,
		systemd: func() bool { return systemd },
		cgroup:  func() (string, error) { return cgroup, nil },
		run: func(name string, args ...string) error {
			return rec(strings.Join(append([]string{name}, args...), " "))
		},
		install: func(src, dst string) error { return rec("install " + src + " " + dst) },
		write:   func(path string, b []byte) error { f.written[path] = string(b); return rec("write " + path) },
		link:    func(target, path string) error { return rec("link " + path + " -> " + target) },
	}
}

func TestAnAgentInTheOldUnitHandsOverToTheNewUnit(t *testing.T) {
	var f fakeService
	h := f.host("/usr/local/bin/cloudy-agent", "0::/system.slice/cloudy-agent.service\n", true)
	if !migrateService(h, t.Logf) {
		t.Fatalf("no handover: %v", f.calls)
	}
	want := []string{
		"install /usr/local/bin/cloudy-agent /usr/local/bin/hosbah-agent",
		"/usr/local/bin/hosbah-agent version",
		"write /etc/systemd/system/hosbah-agent.service",
		"systemctl daemon-reload",
		"systemctl enable hosbah-agent.service",
		"link /usr/local/bin/cloudy-agent -> /usr/local/bin/hosbah-agent",
		"systemctl disable cloudy-agent.service",
		"systemctl --no-block start hosbah-agent.service",
	}
	if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s", strings.Join(f.calls, "\n"))
	}
	unit := f.written["/etc/systemd/system/hosbah-agent.service"]
	for _, s := range []string{"ExecStart=/usr/local/bin/hosbah-agent run", "Conflicts=cloudy-agent.service", "After=network-online.target cloudy-agent.service", "Restart=always"} {
		if !strings.Contains(unit, s) {
			t.Errorf("the unit lacks %q:\n%s", s, unit)
		}
	}
}

// Nothing that decides what runs at boot changes unless the new binary starts, and a handover
// systemd refuses leaves the old unit enabled.
func TestAHandoverThatCannotHappenKeepsTheOldUnit(t *testing.T) {
	var f fakeService
	f.fail = map[string]error{"/usr/local/bin/hosbah-agent version": errors.New("exec format error")}
	if migrateService(f.host("/usr/local/bin/cloudy-agent", "0::/system.slice/cloudy-agent.service", true), t.Logf) {
		t.Fatal("handed over with a binary that does not start")
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "systemctl") || strings.HasPrefix(c, "write") || strings.HasPrefix(c, "link") {
			t.Fatalf("changed %q although the new binary does not start", c)
		}
	}

	f = fakeService{fail: map[string]error{"systemctl --no-block start hosbah-agent.service": errors.New("unit not found")}}
	if migrateService(f.host("/usr/local/bin/cloudy-agent", "0::/system.slice/cloudy-agent.service", true), t.Logf) {
		t.Fatal("reported a handover systemd refused")
	}
	tail := strings.Join(f.calls[len(f.calls)-2:], "\n")
	if tail != "systemctl enable cloudy-agent.service\nsystemctl disable hosbah-agent.service" {
		t.Fatalf("not reverted:\n%s", strings.Join(f.calls, "\n"))
	}
}

// The job stops this unit at once and systemd kills systemctl with it (seen on the win-docker test
// server): that is the handover happening, so nothing is reverted.
func TestSystemctlKilledByTheHandoverIsNotARefusal(t *testing.T) {
	f := fakeService{fail: map[string]error{"systemctl --no-block start hosbah-agent.service": errors.New("signal: terminated")}}
	if !migrateService(f.host("/usr/local/bin/cloudy-agent", "0::/system.slice/cloudy-agent.service", true), t.Logf) {
		t.Fatal("a handover carried out was reported as refused")
	}
	for _, c := range f.calls {
		if c == "systemctl enable cloudy-agent.service" || c == "systemctl disable hosbah-agent.service" {
			t.Fatalf("reverted a handover that happened: %q", c)
		}
	}
}

// Already moved (or installed after the rename), or not under systemd: no unit work.
func TestNoHandoverOutsideTheOldUnit(t *testing.T) {
	var f fakeService
	if migrateService(f.host("/usr/local/bin/hosbah-agent", "0::/system.slice/hosbah-agent.service", true), t.Logf) || len(f.calls) != 0 {
		t.Fatalf("new unit: %v", f.calls)
	}
	f = fakeService{}
	if migrateService(f.host("/usr/local/bin/cloudy-agent", "", false), t.Logf) {
		t.Fatal("no systemd, no handover")
	}
	if strings.Join(f.calls, "\n") != "install /usr/local/bin/cloudy-agent /usr/local/bin/hosbah-agent\nlink /usr/local/bin/cloudy-agent -> /usr/local/bin/hosbah-agent" {
		t.Fatalf("without systemd only the binary moves: %v", f.calls)
	}
}

// hosbah.toml is read first; a repository that still has only cloudy.toml deploys as before. The
// result carries the file under both keys (a control plane from before the rename reads cloudy_toml).
func TestDetectReadsHosbahTomlThenCloudyToml(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "cloudy.toml"), []byte("port = 3000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oc, _ := opCtx(t, agentproto.DetectParams{})
	r, _, err := detect(oc, src, t.TempDir())
	if err != nil || r.TomlFile != "cloudy.toml" || r.Toml() != "port = 3000\n" || r.LegacyToml != r.HosbahToml {
		t.Fatalf("cloudy.toml alone: %+v %v", r, err)
	}
	if err := os.WriteFile(filepath.Join(src, "hosbah.toml"), []byte("port = 4000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, _, err = detect(oc, src, t.TempDir())
	if err != nil || r.TomlFile != "hosbah.toml" || r.Toml() != "port = 4000\n" {
		t.Fatalf("hosbah.toml wins: %+v %v", r, err)
	}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"hosbah_toml":"port = 4000\n"`) || !strings.Contains(string(b), `"cloudy_toml":"port = 4000\n"`) {
		t.Fatalf("both keys on the wire: %s", b)
	}
}
