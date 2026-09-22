package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// The imported-workload handlers never run pvesh or docker here: the runner is replaced by a fake
// that records every argv (and the environment it was given) and answers from a script.

type call struct {
	argv string
	env  []string
}

type fakeRunner struct {
	calls   []call
	answers map[string]string // argv prefix → stdout
	fail    map[string]string // argv prefix → error text
}

func (f *fakeRunner) run(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
	argv := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call{argv: argv, env: env})
	best := ""
	for p := range f.fail {
		if strings.HasPrefix(argv, p) && len(p) > len(best) {
			best = p
		}
	}
	if best != "" {
		return nil, errors.New(f.fail[best])
	}
	for p := range f.answers {
		if strings.HasPrefix(argv, p) && len(p) > len(best) {
			best = p
		}
	}
	return []byte(f.answers[best]), nil
}

// mutating is every recorded argv that changes something (not a read).
func (f *fakeRunner) mutating() []string {
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c.argv, "pvesh get") || strings.HasPrefix(c.argv, "docker inspect") ||
			strings.HasPrefix(c.argv, "docker image inspect") || strings.HasPrefix(c.argv, "docker stats") ||
			strings.HasPrefix(c.argv, "docker container inspect") || strings.HasPrefix(c.argv, "docker volume inspect") || strings.HasPrefix(c.argv, "docker ps") {
			continue
		}
		out = append(out, c.argv)
	}
	return out
}

func newFake(answers map[string]string) (*workloads, *fakeRunner) {
	f := &fakeRunner{answers: answers, fail: map[string]string{}}
	w := &workloads{run: f.run, dial: func(context.Context, string) error { return nil }, sleep: func(time.Duration) {},
		host: func() (string, error) { return "pve.example.lan", nil }}
	w.stream = func(oc *OpContext, name string, args ...string) (int, error) {
		if _, err := w.cmd(oc, nil, name, args...); err != nil {
			return 1, err
		}
		return 0, nil
	}
	return w, f
}

func opCtx(t *testing.T, params any) (*OpContext, *[]string) {
	t.Helper()
	raw, _ := json.Marshal(params)
	var lines []string
	return &OpContext{Ctx: context.Background(), OpID: "op", Params: raw,
		log:      func(_, l string) { lines = append(lines, l) },
		progress: func(int, int) {}}, &lines
}

var pveAnswers = map[string]string{
	"pvesh get /nodes --output-format json":                               `[{"node":"pve"}]`,
	"pvesh get /nodes/pve/lxc/101/config":                                 `{"hostname":"wordpress","cores":2,"memory":2048,"rootfs":"local-lvm:vm-101-disk-0,size=8G"}`,
	"pvesh get /nodes/pve/lxc/101/status/current":                         `{"status":"running","cpu":0.125,"cpus":2,"mem":536870912,"maxmem":2147483648,"disk":1073741824,"maxdisk":8589934592,"uptime":3600}`,
	"pvesh get /nodes/pve/qemu/102/config":                                `{"name":"shop-vm","cores":2,"memory":4096,"boot":"order=ide2;scsi0;net0","ide2":"local:iso/x.iso,media=cdrom","scsi0":"local-lvm:vm-102-disk-0,size=32G"}`,
	"pvesh get /nodes/pve/qemu/102/status/current":                        `{"status":"stopped"}`,
	"pvesh get /nodes/pve/lxc/101/snapshot":                               `[{"name":"before-upgrade","snaptime":1700000000,"description":"x"},{"name":"current","parent":"before-upgrade"}]`,
	"pvesh get /nodes/pve/storage --content backup":                       `[{"storage":"nas","active":0},{"storage":"local","active":1}]`,
	"pvesh get /nodes/pve/storage/local/content --content backup":         `[{"volid":"local:backup/vzdump-lxc-101-old.tar.zst","ctime":1},{"volid":"local:backup/vzdump-lxc-101-new.tar.zst","ctime":2,"size":1234}]`,
	"docker inspect --type container --format {{.State.Status}} shop-web": "running\n",
}

func TestGuestPowerUsesPveshStatusEndpoints(t *testing.T) {
	for _, c := range []struct{ action, want string }{
		{"shutdown", "pvesh create /nodes/pve/lxc/101/status/shutdown"},
		{"stop", "pvesh create /nodes/pve/lxc/101/status/stop"},
		{"restart", "pvesh create /nodes/pve/lxc/101/status/reboot"},
	} {
		w, f := newFake(pveAnswers)
		oc, _ := opCtx(t, agentproto.PowerParams{WorkloadRef: agentproto.Guest("101", "lxc", "wordpress"), Action: c.action})
		res, code, err := w.power(oc)
		if err != nil || code != 0 {
			t.Fatalf("%s: %v (%d)", c.action, err, code)
		}
		if m := f.mutating(); len(m) != 1 || m[0] != c.want {
			t.Fatalf("%s: commands %v, want [%s]", c.action, m, c.want)
		}
		if res.(agentproto.PowerResult).Status != "running" {
			t.Fatalf("%s: result %+v", c.action, res)
		}
	}
	// Starting a running guest does nothing; restarting a stopped VM starts it.
	w, f := newFake(pveAnswers)
	oc, _ := opCtx(t, agentproto.PowerParams{WorkloadRef: agentproto.Guest("101", "lxc", ""), Action: "start"})
	if _, _, err := w.power(oc); err != nil || len(f.mutating()) != 0 {
		t.Fatalf("start of a running guest: %v %v", err, f.mutating())
	}
	w, f = newFake(pveAnswers)
	oc, _ = opCtx(t, agentproto.PowerParams{WorkloadRef: agentproto.Guest("102", "qemu", ""), Action: "restart"})
	if _, _, err := w.power(oc); err != nil || strings.Join(f.mutating(), "|") != "pvesh create /nodes/pve/qemu/102/status/start" {
		t.Fatalf("restart of a stopped VM: %v %v", err, f.mutating())
	}
}

func TestHosbahOwnedTargetsAreRefusedBeforeAnyChange(t *testing.T) {
	answers := map[string]string{}
	for k, v := range pveAnswers {
		answers[k] = v
	}
	answers["pvesh get /nodes/pve/lxc/101/config"] = `{"hostname":"hosbah-control-plane"}`
	for _, params := range []any{
		agentproto.PowerParams{WorkloadRef: agentproto.Guest("101", "lxc", ""), Action: "stop"},
		agentproto.DestroyParams{WorkloadRef: agentproto.Guest("101", "lxc", ""), Purge: true},
		agentproto.SnapshotParams{VMID: "101", GuestType: "lxc", Action: "delete", Name: "snap1"},
	} {
		w, f := newFake(answers)
		oc, _ := opCtx(t, params)
		var err error
		switch params.(type) {
		case agentproto.PowerParams:
			_, _, err = w.power(oc)
		case agentproto.DestroyParams:
			_, _, err = w.destroy(oc)
		default:
			_, _, err = w.snapshot(oc)
		}
		if err == nil || !strings.Contains(err.Error(), "Hosbah's own") || len(f.mutating()) != 0 {
			t.Fatalf("%T on a Hosbah guest: err %v, commands %v", params, err, f.mutating())
		}
	}
	// A Hosbah container name never even reaches a command.
	w, f := newFake(pveAnswers)
	oc, _ := opCtx(t, agentproto.DestroyParams{WorkloadRef: agentproto.WorkloadRef{Target: "container", Name: "hosbah-db-main"}})
	if _, code, err := w.destroy(oc); err == nil || code != 2 || len(f.calls) != 0 {
		t.Fatalf("destroy of hosbah-db-main: %v %d %v", err, code, f.calls)
	}
}

func TestContainerPowerAndDestroy(t *testing.T) {
	w, f := newFake(pveAnswers)
	oc, _ := opCtx(t, agentproto.PowerParams{WorkloadRef: agentproto.Container("shop-web", ""), Action: "shutdown"})
	if _, _, err := w.power(oc); err != nil || strings.Join(f.mutating(), "|") != "docker stop shop-web" {
		t.Fatalf("shutdown of a container: %v %v", err, f.mutating())
	}
	w, f = newFake(pveAnswers)
	oc, _ = opCtx(t, agentproto.DestroyParams{WorkloadRef: agentproto.Container("shop-web", ""), Purge: true})
	if _, _, err := w.destroy(oc); err != nil || strings.Join(f.mutating(), "|") != "docker rm -f -v shop-web" {
		t.Fatalf("destroy with purge: %v %v", err, f.mutating())
	}
	w, f = newFake(pveAnswers)
	oc, _ = opCtx(t, agentproto.DestroyParams{WorkloadRef: agentproto.Container("shop-web", "")})
	if _, _, err := w.destroy(oc); err != nil || strings.Join(f.mutating(), "|") != "docker rm -f shop-web" {
		t.Fatalf("destroy without purge: %v %v", err, f.mutating())
	}
}

func TestGuestDestroyStopsThenDeletes(t *testing.T) {
	w, f := newFake(pveAnswers)
	oc, _ := opCtx(t, agentproto.DestroyParams{WorkloadRef: agentproto.Guest("101", "lxc", ""), Purge: true})
	if _, _, err := w.destroy(oc); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.mutating(), "|"); got != "pvesh create /nodes/pve/lxc/101/status/stop|pvesh delete /nodes/pve/lxc/101 --purge 1" {
		t.Fatalf("destroy: %s", got)
	}
	w, f = newFake(pveAnswers)
	oc, _ = opCtx(t, agentproto.DestroyParams{WorkloadRef: agentproto.Guest("102", "qemu", "")})
	if _, _, err := w.destroy(oc); err != nil || strings.Join(f.mutating(), "|") != "pvesh delete /nodes/pve/qemu/102" {
		t.Fatalf("destroy of a stopped VM without purge: %v %v", err, f.mutating())
	}
}

func TestGuestResizeGrowsTheBootDisk(t *testing.T) {
	w, f := newFake(pveAnswers)
	oc, _ := opCtx(t, agentproto.ResizeParams{VMID: "102", GuestType: "qemu", Cores: 4, MemoryMB: 8192, DiskGrowGB: 10})
	if _, _, err := w.resize(oc); err != nil {
		t.Fatal(err)
	}
	want := "pvesh set /nodes/pve/qemu/102/config --cores 4 --memory 8192|pvesh set /nodes/pve/qemu/102/resize --disk scsi0 --size +10G"
	if got := strings.Join(f.mutating(), "|"); got != want {
		t.Fatalf("resize:\n got %s\nwant %s", got, want)
	}
	w, f = newFake(pveAnswers)
	oc, _ = opCtx(t, agentproto.ResizeParams{VMID: "101", GuestType: "lxc", DiskGrowGB: 5})
	if _, _, err := w.resize(oc); err != nil || strings.Join(f.mutating(), "|") != "pvesh set /nodes/pve/lxc/101/resize --disk rootfs --size +5G" {
		t.Fatalf("lxc disk grow: %v %v", err, f.mutating())
	}
	// Nothing to change is refused before anything runs.
	w, f = newFake(pveAnswers)
	oc, _ = opCtx(t, agentproto.ResizeParams{VMID: "101", GuestType: "lxc"})
	if _, code, err := w.resize(oc); err == nil || code != 2 || len(f.calls) != 0 {
		t.Fatalf("empty resize: %v %d", err, code)
	}
}

func TestGuestSnapshots(t *testing.T) {
	for _, c := range []struct {
		p    agentproto.SnapshotParams
		want string
	}{
		{agentproto.SnapshotParams{Action: "create", Name: "pre-up", Description: "before the upgrade"}, "pvesh create /nodes/pve/lxc/101/snapshot --snapname pre-up --description before the upgrade"},
		{agentproto.SnapshotParams{Action: "rollback", Name: "pre-up"}, "pvesh create /nodes/pve/lxc/101/snapshot/pre-up/rollback"},
		{agentproto.SnapshotParams{Action: "delete", Name: "pre-up"}, "pvesh delete /nodes/pve/lxc/101/snapshot/pre-up"},
		{agentproto.SnapshotParams{Action: "list"}, ""},
	} {
		c.p.VMID, c.p.GuestType = "101", "lxc"
		w, f := newFake(pveAnswers)
		oc, _ := opCtx(t, c.p)
		res, _, err := w.snapshot(oc)
		if err != nil {
			t.Fatalf("%s: %v", c.p.Action, err)
		}
		if got := strings.Join(f.mutating(), "|"); got != c.want {
			t.Fatalf("%s: %q, want %q", c.p.Action, got, c.want)
		}
		snaps := res.(agentproto.SnapshotResult).Snapshots
		if len(snaps) != 1 || snaps[0].Name != "before-upgrade" {
			t.Fatalf("%s: the list must leave out \"current\": %+v", c.p.Action, snaps)
		}
	}
}

func TestGuestBackupPicksAnActiveStorageAndReportsTheArchive(t *testing.T) {
	w, f := newFake(pveAnswers)
	oc, _ := opCtx(t, agentproto.GuestBackupParams{VMID: "101", GuestType: "lxc"})
	res, _, err := w.backup(oc)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.mutating(), "|"); got != "vzdump 101 --mode snapshot --compress zstd --storage local --remove 0" {
		t.Fatalf("vzdump: %s", got)
	}
	if r := res.(agentproto.GuestBackupResult); r.VolID != "local:backup/vzdump-lxc-101-new.tar.zst" || r.Storage != "local" || r.SizeBytes != 1234 {
		t.Fatalf("result: %+v", r)
	}
}

func TestGuestStats(t *testing.T) {
	w, _ := newFake(pveAnswers)
	oc, _ := opCtx(t, agentproto.StatsParams{WorkloadRef: agentproto.Guest("101", "lxc", "")})
	res, _, err := w.stats(oc)
	if err != nil {
		t.Fatal(err)
	}
	st := res.(agentproto.WorkloadStats)
	if st.Status != "running" || st.CPUPercent != 12.5 || st.MemTotalBytes != 2147483648 || st.UptimeSeconds != 3600 || st.DiskTotalBytes != 8589934592 {
		t.Fatalf("stats: %+v", st)
	}
}

func TestContainerStatsParsesDocker(t *testing.T) {
	answers := map[string]string{
		"docker inspect --type container --size shop-web": `[{"State":{"Status":"running","Running":true,"StartedAt":"` +
			time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano) + `"},"SizeRw":2048,"SizeRootFs":187000000,"HostConfig":{"Memory":0,"NanoCpus":1500000000}}]`,
		"docker stats --no-stream": `{"CPUPerc":"3.25%","MemUsage":"12.5MiB / 1.9GiB"}`,
	}
	w, _ := newFake(answers)
	oc, _ := opCtx(t, agentproto.StatsParams{WorkloadRef: agentproto.Container("shop-web", "")})
	res, _, err := w.stats(oc)
	if err != nil {
		t.Fatal(err)
	}
	st := res.(agentproto.WorkloadStats)
	if st.CPUPercent != 3.25 || st.MemUsedBytes != uint64(12.5*(1<<20)) || st.MemTotalBytes == 0 || st.CPUs != 1.5 || st.UptimeSeconds < 3500 || st.DiskUsedBytes != 2048 {
		t.Fatalf("container stats: %+v", st)
	}
}

func TestWorkloadLogsArgs(t *testing.T) {
	got := strings.Join(workloadLogsArgs(agentproto.WorkloadLogsParams{Name: "shop-web", Tail: 500, Since: "15m", FollowSeconds: 30}), " ")
	if got != "logs --timestamps --tail 500 --since 15m --follow shop-web" {
		t.Fatalf("logs argv: %s", got)
	}
	if err := (agentproto.WorkloadLogsParams{Name: "shop-web", Tail: 5000}).Validate(); err == nil {
		t.Fatal("a tail over 2000 must be refused")
	}
	if err := (agentproto.WorkloadLogsParams{Name: "shop-web", Tail: 10, Since: "$(reboot)"}).Validate(); err == nil {
		t.Fatal("a since that is not a duration or a time must be refused")
	}
}

func TestProbeKeepsPasswordsOffTheArgv(t *testing.T) {
	cred := map[string]string{"user": "app", "password": "s3cret!", "database": "shop"}
	for kind, want := range map[string]string{
		"postgres": "exec -i -e PGPASSWORD pg-main psql -h 127.0.0.1 -U app -d shop -tAc SELECT version()",
		"mysql":    "exec -i -e MYSQL_PWD pg-main mysql -h 127.0.0.1 -u app -N -e SELECT VERSION()",
		"redis":    "exec -i -e REDISCLI_AUTH pg-main redis-cli -h 127.0.0.1 --user app PING",
	} {
		args, env := probeArgs(kind, "pg-main", cred, false)
		if got := strings.Join(args, " "); got != want {
			t.Fatalf("%s probe argv:\n got %s\nwant %s", kind, got, want)
		}
		if strings.Contains(strings.Join(args, " "), "s3cret") || len(env) != 1 || !strings.HasSuffix(env[0], "=s3cret!") {
			t.Fatalf("%s: the password must travel in the environment only: %v %v", kind, args, env)
		}
	}
}

func TestProbeRunsInTheContainer(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	_ = pub
	box, err := agentproto.SealBox(pubB64(pub), agentproto.DBSecretContext("00000000-0000-4000-8000-000000000001", "root"),
		[]byte(`{"user":"app","password":"pw"}`))
	if err != nil {
		t.Fatal(err)
	}
	w, f := newFake(map[string]string{"docker inspect --type container --format {{.State.Status}} pg-main": "running",
		"docker exec": "PostgreSQL 16.4 on x86_64"})
	oc, _ := opCtx(t, agentproto.DBProbeParams{ServiceID: "00000000-0000-4000-8000-000000000001", Kind: "postgres", Target: "container",
		Container: "pg-main", Credentials: box})
	oc.priv = priv
	res, _, err := w.probe(oc)
	if err != nil {
		t.Fatal(err)
	}
	if r := res.(agentproto.DBProbeResult); !r.OK || !r.Verified || !strings.HasPrefix(r.Version, "PostgreSQL 16") {
		t.Fatalf("probe: %+v", r)
	}
	last := f.calls[len(f.calls)-1]
	if !strings.HasPrefix(last.argv, "docker exec -i -e PGPASSWORD pg-main psql") || len(last.env) != 1 || last.env[0] != "PGPASSWORD=pw" {
		t.Fatalf("probe call: %+v", last)
	}
	// Refused credentials fail the operation.
	f.fail["docker exec"] = "psql: FATAL: password authentication failed"
	if _, _, err := w.probe(oc); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("a wrong password must fail: %v", err)
	}
}

func TestImportedCredentialsLogInAsTheirUser(t *testing.T) {
	root := map[string]string{"user": "admin", "password": "pw", "database": "shop"}
	args, env := dbDumpCommand("postgres", "", root, "pg-main")
	if got := strings.Join(args, " "); got != "docker exec -i -e PGPASSWORD pg-main pg_dumpall -h 127.0.0.1 -U admin -d shop" || env[0] != "PGPASSWORD=pw" {
		t.Fatalf("pg_dumpall of an imported service: %s", got)
	}
	args, _ = dbDumpCommand("postgres", "orders", map[string]string{"password": "pw"}, "hosbah-db-x")
	if got := strings.Join(args, " "); got != "docker exec -i -e PGPASSWORD hosbah-db-x pg_dump -U postgres -d orders -Fc" {
		t.Fatalf("pg_dump of a managed service: %s", got)
	}
	args, _ = dbDumpCommand("mysql", "", root, "maria")
	if got := strings.Join(args, " "); got != "docker exec -i -e MYSQL_PWD maria mysqldump -h 127.0.0.1 -u admin --single-transaction --all-databases" {
		t.Fatalf("mysqldump of an imported service: %s", got)
	}
	args, env = dbDumpCommand("redis", "", map[string]string{"password": "pw"}, "cache")
	if got := strings.Join(args, " "); got != "docker exec -i -e REDISCLI_AUTH cache redis-cli --rdb -" || env[0] != "REDISCLI_AUTH=pw" {
		t.Fatalf("redis with a password: %s %v", got, env)
	}
}

func pubB64(pub ed25519.PublicKey) string {
	b, _ := json.Marshal([]byte(pub))
	return strings.Trim(string(b), `"`)
}
