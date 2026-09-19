package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// Imported-workload control (workload.*, guest.*, container.recreate, db.probe): Proxmox guests
// through pvesh / vzdump on the hypervisor this agent runs on, Docker containers through the docker
// CLI. Every command is a fixed argv built from validated params — no shell — and every target is
// checked against Hosbah's own workloads (agentproto.IsHosbahOwned) before anything runs, including a
// guest's own configured name, read back from Proxmox.
//
// The commands go through an injectable runner so the argv each handler builds is unit-tested
// without pvesh or docker (workloads_test.go).

// runFunc runs name with args (extra environment in env, never logged) and returns its stdout. An
// error carries the command's stderr.
type runFunc func(ctx context.Context, env []string, name string, args ...string) ([]byte, error)

type workloads struct {
	run   runFunc
	dial  func(ctx context.Context, addr string) error
	sleep func(time.Duration)
	host  func() (string, error)
	// stream runs a long command with its output lines in the operation's log (vzdump).
	stream func(oc *OpContext, name string, args ...string) (int, error)
}

func newWorkloads() *workloads {
	return &workloads{run: execRun, dial: tcpDial, sleep: time.Sleep, host: os.Hostname,
		stream: func(oc *OpContext, name string, args ...string) (int, error) { return oc.Run(name, args...) }}
}

func execRun(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //#nosec G204 -- fixed argv from validated params
	cmd.Env = append(append(os.Environ(), "LC_ALL=C"), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("%s: %s", name, clip(msg, 600))
	}
	return out, nil
}

func tcpDial(ctx context.Context, addr string) error {
	d := net.Dialer{Timeout: 5 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return c.Close()
}

// cmd runs and logs one command ("$ argv"; never the environment).
func (w *workloads) cmd(oc *OpContext, env []string, name string, args ...string) (string, error) {
	oc.Info("$ %s", shellQuote(append([]string{name}, args...)))
	out, err := w.run(oc.Ctx, env, name, args...)
	return string(out), err
}

// quiet runs a read-only command without logging it (status reads, JSON queries).
func (w *workloads) quiet(oc *OpContext, name string, args ...string) (string, error) {
	out, err := w.run(oc.Ctx, nil, name, args...)
	return string(out), err
}

// ---- Proxmox ---------------------------------------------------------------------------------

// node is this hypervisor's Proxmox node name: the only node pvesh lists, else the one named like
// the host (a cluster member), else the short hostname.
func (w *workloads) node(oc *OpContext) (string, error) {
	host, _ := w.host()
	host, _, _ = strings.Cut(strings.TrimSpace(host), ".")
	out, err := w.quiet(oc, "pvesh", "get", "/nodes", "--output-format", "json")
	if err != nil {
		if _, lerr := exec.LookPath("pvesh"); lerr != nil && !errors.Is(err, context.Canceled) {
			return "", errors.New("pvesh is not available: this server is not a Proxmox VE node")
		}
		if host == "" {
			return "", err
		}
		return host, nil
	}
	var nodes []struct {
		Node string `json:"node"`
	}
	if json.Unmarshal([]byte(out), &nodes) == nil {
		if len(nodes) == 1 && nodes[0].Node != "" {
			return nodes[0].Node, nil
		}
		for _, n := range nodes {
			if n.Node == host {
				return host, nil
			}
		}
	}
	if host == "" {
		return "", errors.New("could not tell which Proxmox node this is")
	}
	return host, nil
}

func guestPath(node, typ, vmid string) string { return "/nodes/" + node + "/" + typ + "/" + vmid }

// guestConfig reads a guest's configuration (pvesh get …/config).
func (w *workloads) guestConfig(oc *OpContext, path string) (map[string]any, error) {
	out, err := w.quiet(oc, "pvesh", "get", path+"/config", "--output-format", "json")
	if err != nil {
		return nil, fmt.Errorf("guest not found on this node (%v)", err)
	}
	cfg := map[string]any{}
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		return nil, errors.New("pvesh returned a configuration that is not JSON")
	}
	return cfg, nil
}

// guest resolves the node and path of a guest and refuses Hosbah's own (by its configured name).
func (w *workloads) guest(oc *OpContext, vmid, typ string) (node, path string, cfg map[string]any, err error) {
	if node, err = w.node(oc); err != nil {
		return "", "", nil, err
	}
	path = guestPath(node, typ, vmid)
	if cfg, err = w.guestConfig(oc, path); err != nil {
		return "", "", nil, err
	}
	for _, k := range []string{"name", "hostname"} {
		if n, _ := cfg[k].(string); n != "" && agentproto.IsHosbahOwned(n) {
			return "", "", nil, fmt.Errorf("guest %s (%s) is one of Hosbah's own workloads — refused", vmid, n)
		}
	}
	return node, path, cfg, nil
}

type guestStatus struct {
	Status  string  `json:"status"`
	CPU     float64 `json:"cpu"`
	CPUs    float64 `json:"cpus"`
	Mem     uint64  `json:"mem"`
	MaxMem  uint64  `json:"maxmem"`
	Disk    uint64  `json:"disk"`
	MaxDisk uint64  `json:"maxdisk"`
	Uptime  uint64  `json:"uptime"`
}

func (w *workloads) guestStatus(oc *OpContext, path string) (guestStatus, error) {
	var st guestStatus
	out, err := w.quiet(oc, "pvesh", "get", path+"/status/current", "--output-format", "json")
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		return st, errors.New("pvesh returned a status that is not JSON")
	}
	return st, nil
}

// ---- Docker ----------------------------------------------------------------------------------

func (w *workloads) containerStatus(oc *OpContext, name string) (string, error) {
	out, err := w.quiet(oc, "docker", "inspect", "--type", "container", "--format", "{{.State.Status}}", name)
	if err != nil {
		return "", fmt.Errorf("container %s not found on this server", name)
	}
	return strings.TrimSpace(out), nil
}

// ---- workload.power --------------------------------------------------------------------------

// guestPowerVerb maps an action to Proxmox's status endpoint: stop is a hard stop, shutdown is
// graceful, restart is a reboot.
func guestPowerVerb(action string) string {
	if action == agentproto.PowerRestart {
		return "reboot"
	}
	return action
}

func (w *workloads) power(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.PowerParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if p.Target == agentproto.TargetGuest {
		_, path, _, err := w.guest(oc, p.VMID, p.GuestType)
		if err != nil {
			return nil, 1, err
		}
		st, err := w.guestStatus(oc, path)
		if err != nil {
			return nil, 1, err
		}
		verb := guestPowerVerb(p.Action)
		switch {
		case p.Action == agentproto.PowerStart && st.Status == "running",
			(p.Action == agentproto.PowerStop || p.Action == agentproto.PowerShutdown) && st.Status == "stopped":
			oc.Info("guest %s is already %s — nothing to do", p.VMID, st.Status)
			return agentproto.PowerResult{Status: st.Status}, 0, nil
		case p.Action == agentproto.PowerRestart && st.Status == "stopped":
			verb = "start"
		}
		if out, err := w.cmd(oc, nil, "pvesh", "create", path+"/status/"+verb); err != nil {
			return nil, 1, err
		} else if s := strings.TrimSpace(out); s != "" {
			oc.Info("%s", clip(s, 2000))
		}
		after, err := w.guestStatus(oc, path)
		if err != nil {
			return nil, 1, err
		}
		return agentproto.PowerResult{Status: after.Status}, 0, nil
	}
	status, err := w.containerStatus(oc, p.Name)
	if err != nil {
		return nil, 1, err
	}
	verb := p.Action
	switch {
	case p.Action == agentproto.PowerShutdown:
		verb = "stop" // docker stop is already SIGTERM, a grace period, then SIGKILL
	case p.Action == agentproto.PowerStart && status == "running":
		oc.Info("%s is already running — nothing to do", p.Name)
		return agentproto.PowerResult{Status: status}, 0, nil
	}
	if _, err := w.cmd(oc, nil, "docker", verb, p.Name); err != nil {
		return nil, 1, err
	}
	after, err := w.containerStatus(oc, p.Name)
	if err != nil {
		return nil, 1, err
	}
	return agentproto.PowerResult{Status: after}, 0, nil
}

// ---- workload.stats --------------------------------------------------------------------------

func (w *workloads) stats(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.StatsParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if p.Target == agentproto.TargetGuest {
		_, path, _, err := w.guest(oc, p.VMID, p.GuestType)
		if err != nil {
			return nil, 1, err
		}
		st, err := w.guestStatus(oc, path)
		if err != nil {
			return nil, 1, err
		}
		return agentproto.WorkloadStats{Status: st.Status, CPUPercent: round2(st.CPU * 100), CPUs: st.CPUs,
			MemUsedBytes: st.Mem, MemTotalBytes: st.MaxMem, DiskUsedBytes: st.Disk, DiskTotalBytes: st.MaxDisk, UptimeSeconds: st.Uptime}, 0, nil
	}
	out, err := w.quiet(oc, "docker", "inspect", "--type", "container", "--size", p.Name)
	if err != nil {
		return nil, 1, fmt.Errorf("container %s not found on this server", p.Name)
	}
	var ins []struct {
		State struct {
			Status    string `json:"Status"`
			Running   bool   `json:"Running"`
			StartedAt string `json:"StartedAt"`
		} `json:"State"`
		SizeRw     int64 `json:"SizeRw"`
		SizeRootFs int64 `json:"SizeRootFs"`
		HostConfig struct {
			Memory   int64 `json:"Memory"`
			NanoCpus int64 `json:"NanoCpus"`
		} `json:"HostConfig"`
	}
	if json.Unmarshal([]byte(out), &ins) != nil || len(ins) == 0 {
		return nil, 1, errors.New("docker inspect returned nothing usable")
	}
	c := ins[0]
	res := agentproto.WorkloadStats{Status: c.State.Status, DiskUsedBytes: uint64(max(c.SizeRw, 0)), DiskTotalBytes: uint64(max(c.SizeRootFs, 0)),
		MemTotalBytes: uint64(max(c.HostConfig.Memory, 0)), CPUs: float64(c.HostConfig.NanoCpus) / 1e9}
	if c.State.Running {
		if t, err := time.Parse(time.RFC3339Nano, c.State.StartedAt); err == nil {
			res.UptimeSeconds = uint64(max(time.Since(t), 0) / time.Second)
		}
		if sout, err := w.quiet(oc, "docker", "stats", "--no-stream", "--format", "{{json .}}", p.Name); err == nil {
			var s struct{ CPUPerc, MemUsage string }
			if json.Unmarshal([]byte(strings.TrimSpace(firstLine(sout))), &s) == nil {
				res.CPUPercent = parsePercent(s.CPUPerc)
				used, limit, _ := strings.Cut(s.MemUsage, "/")
				res.MemUsedBytes = parseDockerSize(used)
				if res.MemTotalBytes == 0 {
					res.MemTotalBytes = parseDockerSize(limit)
				}
			}
		}
	}
	return res, 0, nil
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }

func parsePercent(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
	return f
}

// parseDockerSize reads docker's human sizes: "12.5MiB", "1.9GiB", "512kB", "3B".
func parseDockerSize(s string) uint64 {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		mult   float64
	}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40}, {"kB", 1e3}, {"KB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"TB", 1e12}, {"B", 1}}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), 64)
			if err != nil {
				return 0
			}
			return uint64(f * u.mult)
		}
	}
	f, _ := strconv.ParseFloat(s, 64)
	return uint64(f)
}

// ---- workload.logs ---------------------------------------------------------------------------

// workloadLogsArgs is the docker argv of workload.logs.
func workloadLogsArgs(p agentproto.WorkloadLogsParams) []string {
	args := []string{"logs", "--timestamps", "--tail", strconv.Itoa(p.Tail)}
	if p.Since != "" {
		args = append(args, "--since", p.Since)
	}
	if p.FollowSeconds > 0 {
		args = append(args, "--follow")
	}
	return append(args, p.Name)
}

// logs relays a container's lines as the operation's log, like app.logs: bounded in lines and time.
func (w *workloads) logs(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.WorkloadLogsParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if _, err := w.containerStatus(oc, p.Name); err != nil {
		return nil, 1, err
	}
	ctx, cancel := context.WithTimeout(oc.Ctx, time.Duration(p.FollowSeconds)*time.Second+5*time.Second)
	defer cancel()
	oc.Info("logs of %s: the last %d lines%s", p.Name, p.Tail, map[bool]string{true: fmt.Sprintf(", then following for %ds", p.FollowSeconds), false: ""}[p.FollowSeconds > 0])
	cmd := exec.CommandContext(ctx, "docker", workloadLogsArgs(p)...) //#nosec G204 -- fixed argv, validated name
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return nil, 1, err
	}
	lines := 0
	done := make(chan struct{}, 2)
	pump := func(r io.Reader, stream string) {
		defer func() { done <- struct{}{} }()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			if lines >= agentproto.MaxLogLines {
				cancel()
				continue
			}
			lines++
			oc.log(stream, clip(sc.Text(), maxLine))
		}
	}
	go pump(stdout, "stdout")
	go pump(stderr, "stderr")
	<-done
	<-done
	_ = cmd.Wait()
	return map[string]any{"lines": lines}, 0, nil
}

// ---- guest.resize ----------------------------------------------------------------------------

// qemuRootDisk is the disk a VM boots from: the first disk in its boot order, else bootdisk, else
// the first of scsi0 / virtio0 / sata0 / ide0 it has (never a CD-ROM).
func qemuRootDisk(cfg map[string]any) string {
	isDisk := func(k string) bool {
		v, ok := cfg[k].(string)
		return ok && !strings.Contains(v, "media=cdrom")
	}
	if boot, _ := cfg["boot"].(string); boot != "" {
		for _, part := range strings.Split(boot, ",") {
			if order, ok := strings.CutPrefix(strings.TrimSpace(part), "order="); ok {
				for _, dev := range strings.Split(order, ";") {
					if isDisk(dev) {
						return dev
					}
				}
			}
		}
	}
	if bd, _ := cfg["bootdisk"].(string); bd != "" && isDisk(bd) {
		return bd
	}
	for _, k := range []string{"scsi0", "virtio0", "sata0", "ide0"} {
		if isDisk(k) {
			return k
		}
	}
	return ""
}

func diskSize(v any) string {
	s, _ := v.(string)
	for _, kv := range strings.Split(s, ",") {
		if sz, ok := strings.CutPrefix(kv, "size="); ok {
			return sz
		}
	}
	return ""
}

func intOf(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}

func (w *workloads) resize(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.ResizeParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	_, path, cfg, err := w.guest(oc, p.VMID, p.GuestType)
	if err != nil {
		return nil, 1, err
	}
	if set := resizeConfigArgs(path, p); set != nil {
		if _, err := w.cmd(oc, nil, "pvesh", set...); err != nil {
			return nil, 1, err
		}
	}
	res := agentproto.ResizeResult{}
	if p.DiskGrowGB > 0 {
		disk := "rootfs"
		if p.GuestType == "qemu" {
			if disk = qemuRootDisk(cfg); disk == "" {
				return nil, 1, errors.New("could not find this VM's boot disk to grow")
			}
		}
		if _, err := w.cmd(oc, nil, "pvesh", "set", path+"/resize", "--disk", disk, "--size", "+"+strconv.Itoa(p.DiskGrowGB)+"G"); err != nil {
			return nil, 1, err
		}
		res.Disk = disk
	}
	if after, err := w.guestConfig(oc, path); err == nil {
		res.Cores, res.MemoryMB = intOf(after["cores"]), intOf(after["memory"])
		if res.Disk != "" {
			res.DiskSize = diskSize(after[res.Disk])
		}
	}
	return res, 0, nil
}

// resizeConfigArgs is the pvesh set …/config argv for cores and memory (nil when neither changes).
func resizeConfigArgs(path string, p agentproto.ResizeParams) []string {
	args := []string{"set", path + "/config"}
	if p.Cores > 0 {
		args = append(args, "--cores", strconv.Itoa(p.Cores))
	}
	if p.MemoryMB > 0 {
		args = append(args, "--memory", strconv.Itoa(p.MemoryMB))
	}
	if len(args) == 2 {
		return nil
	}
	return args
}

// ---- guest.snapshot --------------------------------------------------------------------------

// snapshotArgs is the pvesh argv of a snapshot action (nil for list).
func snapshotArgs(path string, p agentproto.SnapshotParams) []string {
	switch p.Action {
	case agentproto.SnapshotCreate:
		args := []string{"create", path + "/snapshot", "--snapname", p.Name}
		if p.Description != "" {
			args = append(args, "--description", p.Description)
		}
		return args
	case agentproto.SnapshotRollback:
		return []string{"create", path + "/snapshot/" + p.Name + "/rollback"}
	case agentproto.SnapshotDelete:
		return []string{"delete", path + "/snapshot/" + p.Name}
	}
	return nil
}

func (w *workloads) snapshot(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.SnapshotParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	_, path, _, err := w.guest(oc, p.VMID, p.GuestType)
	if err != nil {
		return nil, 1, err
	}
	if args := snapshotArgs(path, p); args != nil {
		if out, err := w.cmd(oc, nil, "pvesh", args...); err != nil {
			return nil, 1, err
		} else if s := strings.TrimSpace(out); s != "" {
			oc.Info("%s", clip(s, 2000))
		}
	}
	snaps, err := w.snapshots(oc, path)
	if err != nil {
		return nil, 1, err
	}
	return agentproto.SnapshotResult{Snapshots: snaps}, 0, nil
}

func (w *workloads) snapshots(oc *OpContext, path string) ([]agentproto.Snapshot, error) {
	out, err := w.quiet(oc, "pvesh", "get", path+"/snapshot", "--output-format", "json")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parent      string `json:"parent"`
		SnapTime    int64  `json:"snaptime"`
		VMState     any    `json:"vmstate"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, errors.New("pvesh returned a snapshot list that is not JSON")
	}
	snaps := []agentproto.Snapshot{}
	for _, r := range raw {
		if r.Name == "current" {
			continue // "You are here", not a snapshot
		}
		snaps = append(snaps, agentproto.Snapshot{Name: r.Name, Description: strings.TrimSpace(r.Description), Parent: r.Parent,
			SnapTime: r.SnapTime, VMState: intOf(r.VMState) == 1})
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].SnapTime < snaps[j].SnapTime })
	return snaps, nil
}

// ---- guest.backup ----------------------------------------------------------------------------

// vzdumpArgs is the vzdump argv: a snapshot-mode, zstd-compressed archive to storage. --remove 0 so
// taking a backup never prunes older ones.
func vzdumpArgs(vmid, storage string) []string {
	return []string{vmid, "--mode", "snapshot", "--compress", "zstd", "--storage", storage, "--remove", "0"}
}

func (w *workloads) backup(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.GuestBackupParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	node, _, _, err := w.guest(oc, p.VMID, p.GuestType)
	if err != nil {
		return nil, 1, err
	}
	storage := p.Storage
	if storage == "" {
		if storage, err = w.backupStorage(oc, node); err != nil {
			return nil, 1, err
		}
	}
	oc.Info("backing up guest %s to %s (snapshot mode, zstd)", p.VMID, storage)
	if code, err := w.stream(oc, "vzdump", vzdumpArgs(p.VMID, storage)...); err != nil {
		return nil, code, err
	}
	out, err := w.quiet(oc, "pvesh", "get", "/nodes/"+node+"/storage/"+storage+"/content", "--content", "backup", "--vmid", p.VMID, "--output-format", "json")
	if err != nil {
		return nil, 1, fmt.Errorf("the backup ran, but its archive could not be listed: %v", err)
	}
	var items []struct {
		VolID string `json:"volid"`
		CTime int64  `json:"ctime"`
		Size  uint64 `json:"size"`
	}
	if json.Unmarshal([]byte(out), &items) != nil || len(items) == 0 {
		return nil, 1, errors.New("the backup ran, but no archive was found on " + storage)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CTime > items[j].CTime })
	oc.Info("archive %s", items[0].VolID)
	return agentproto.GuestBackupResult{Storage: storage, VolID: items[0].VolID, SizeBytes: items[0].Size}, 0, nil
}

// backupStorage is the node's first active storage that holds backups.
func (w *workloads) backupStorage(oc *OpContext, node string) (string, error) {
	out, err := w.quiet(oc, "pvesh", "get", "/nodes/"+node+"/storage", "--content", "backup", "--enabled", "1", "--output-format", "json")
	if err != nil {
		return "", err
	}
	var sts []struct {
		Storage string `json:"storage"`
		Active  int    `json:"active"`
	}
	if err := json.Unmarshal([]byte(out), &sts); err != nil {
		return "", errors.New("pvesh returned a storage list that is not JSON")
	}
	sort.SliceStable(sts, func(i, j int) bool { return sts[i].Storage < sts[j].Storage })
	for _, s := range sts {
		if s.Active == 1 && agentproto.ValidStorage(s.Storage) {
			return s.Storage, nil
		}
	}
	return "", errors.New("this node has no active storage for backups — give one explicitly")
}

// ---- workload.destroy ------------------------------------------------------------------------

// guestDestroyArgs is the pvesh argv that destroys a guest.
func guestDestroyArgs(path string, purge bool) []string {
	args := []string{"delete", path}
	if purge {
		args = append(args, "--purge", "1")
	}
	return args
}

// containerDestroyArgs is the docker argv that removes a container (and its anonymous volumes).
func containerDestroyArgs(name string, purge bool) []string {
	if purge {
		return []string{"rm", "-f", "-v", name}
	}
	return []string{"rm", "-f", name}
}

func (w *workloads) destroy(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DestroyParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if p.Target == agentproto.TargetGuest {
		_, path, _, err := w.guest(oc, p.VMID, p.GuestType)
		if err != nil {
			return nil, 1, err
		}
		if st, err := w.guestStatus(oc, path); err == nil && st.Status != "stopped" {
			if _, err := w.cmd(oc, nil, "pvesh", "create", path+"/status/stop"); err != nil {
				return nil, 1, err
			}
		}
		if _, err := w.cmd(oc, nil, "pvesh", guestDestroyArgs(path, p.Purge)...); err != nil {
			return nil, 1, err
		}
		oc.Info("guest %s destroyed", p.VMID)
		return map[string]any{"destroyed": true, "vmid": p.VMID}, 0, nil
	}
	if _, err := w.containerStatus(oc, p.Name); err != nil {
		oc.Info("%s is already gone", p.Name)
		return map[string]any{"destroyed": true, "name": p.Name}, 0, nil
	}
	if _, err := w.cmd(oc, nil, "docker", containerDestroyArgs(p.Name, p.Purge)...); err != nil {
		return nil, 1, err
	}
	return map[string]any{"destroyed": true, "name": p.Name}, 0, nil
}

// ---- db.probe --------------------------------------------------------------------------------

// probeArgs is the docker exec argv (and the environment carrying the password) that checks a set
// of credentials with the engine's own client inside the container. TCP to 127.0.0.1, so the server
// really checks the password (a local socket is often trusted).
func probeArgs(kind, container string, cred map[string]string, mariadb bool) (args, env []string) {
	user, pw := cred["user"], cred["password"]
	switch kind {
	case "postgres":
		dbname := cred["database"]
		if dbname == "" {
			dbname = "postgres"
		}
		return []string{"exec", "-i", "-e", "PGPASSWORD", container, "psql", "-h", "127.0.0.1", "-U", user, "-d", dbname, "-tAc", "SELECT version()"},
			[]string{"PGPASSWORD=" + pw}
	case "mysql":
		client := "mysql"
		if mariadb {
			client = "mariadb"
		}
		return []string{"exec", "-i", "-e", "MYSQL_PWD", container, client, "-h", "127.0.0.1", "-u", user, "-N", "-e", "SELECT VERSION()"},
			[]string{"MYSQL_PWD=" + pw}
	default: // redis
		args = []string{"exec", "-i"}
		if pw != "" {
			args = append(args, "-e", "REDISCLI_AUTH")
			env = []string{"REDISCLI_AUTH=" + pw}
		}
		args = append(args, container, "redis-cli", "-h", "127.0.0.1")
		if user != "" && user != "default" {
			args = append(args, "--user", user)
		}
		return append(args, "PING"), env
	}
}

func (w *workloads) probe(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DBProbeParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	cred, err := oc.openSecret(p.ServiceID, "root", p.Credentials)
	if err != nil {
		return nil, 1, err
	}
	if p.Kind != "redis" && cred["user"] == "" {
		return nil, 2, errors.New("a username is required")
	}
	if p.Target == agentproto.TargetGuest {
		addr := net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
		oc.Info("connecting to %s", addr)
		if err := w.dial(oc.Ctx, addr); err != nil {
			return agentproto.DBProbeResult{OK: false, Detail: "nothing answers on " + addr}, 1, fmt.Errorf("could not connect to %s: %v", addr, err)
		}
		return agentproto.DBProbeResult{OK: true, Verified: false,
			Detail: addr + " answers; the credentials are stored but could not be verified from the hypervisor"}, 0, nil
	}
	if status, err := w.containerStatus(oc, p.Container); err != nil {
		return nil, 1, err
	} else if status != "running" {
		return nil, 1, fmt.Errorf("%s is %s — start it first", p.Container, status)
	}
	args, env := probeArgs(p.Kind, p.Container, cred, false)
	oc.Info("checking the credentials of %s with its own client", p.Container)
	out, err := w.run(oc.Ctx, env, "docker", args...)
	if err != nil && p.Kind == "mysql" && strings.Contains(err.Error(), "executable file not found") {
		args, env = probeArgs(p.Kind, p.Container, cred, true)
		out, err = w.run(oc.Ctx, env, "docker", args...)
	}
	text := strings.TrimSpace(string(out))
	if err == nil && p.Kind == "redis" && !strings.Contains(text, "PONG") {
		err = errors.New(clip(text, 300))
	}
	if err != nil {
		return agentproto.DBProbeResult{OK: false, Detail: clip(err.Error(), 400)}, 1, errors.New("the credentials were refused: " + clip(err.Error(), 300))
	}
	oc.Info("the credentials work")
	return agentproto.DBProbeResult{OK: true, Verified: true, Version: clip(firstLine(text), 200)}, 0, nil
}
