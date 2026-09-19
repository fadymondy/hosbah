package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// container.recreate: pull the image a container runs and recreate it with the same configuration,
// read from `docker inspect`. The plan (the `docker create` argv, plus any extra networks) is built
// completely BEFORE anything changes, and an option this code cannot reproduce exactly refuses the
// whole operation — a container is never half-recreated. The old container is renamed aside and
// stopped, the new one takes its name and is started; only once it is running is the old one
// removed. Any failure removes the new one and puts the old one back, running if it was.

// inspected is the part of `docker inspect` that decides how a container runs.
type inspected struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	Image string `json:"Image"` // the image id it runs
	State struct {
		Running bool   `json:"Running"`
		Status  string `json:"Status"`
	} `json:"State"`
	Config struct {
		Hostname     string            `json:"Hostname"`
		Domainname   string            `json:"Domainname"`
		User         string            `json:"User"`
		Env          []string          `json:"Env"`
		Cmd          []string          `json:"Cmd"`
		Entrypoint   []string          `json:"Entrypoint"`
		Image        string            `json:"Image"`
		WorkingDir   string            `json:"WorkingDir"`
		Labels       map[string]string `json:"Labels"`
		Tty          bool              `json:"Tty"`
		OpenStdin    bool              `json:"OpenStdin"`
		StopSignal   string            `json:"StopSignal"`
		StopTimeout  *int              `json:"StopTimeout"`
		Healthcheck  *healthcheck      `json:"Healthcheck"`
		ExposedPorts map[string]any    `json:"ExposedPorts"`
	} `json:"Config"`
	HostConfig struct {
		Binds         []string                 `json:"Binds"`
		NetworkMode   string                   `json:"NetworkMode"`
		PortBindings  map[string][]portBinding `json:"PortBindings"`
		RestartPolicy struct {
			Name              string
			MaximumRetryCount int
		} `json:"RestartPolicy"`
		AutoRemove        bool              `json:"AutoRemove"`
		VolumesFrom       []string          `json:"VolumesFrom"`
		Links             []string          `json:"Links"`
		CapAdd            []string          `json:"CapAdd"`
		CapDrop           []string          `json:"CapDrop"`
		DNS               []string          `json:"Dns"`
		DNSOptions        []string          `json:"DnsOptions"`
		DNSSearch         []string          `json:"DnsSearch"`
		ExtraHosts        []string          `json:"ExtraHosts"`
		GroupAdd          []string          `json:"GroupAdd"`
		IpcMode           string            `json:"IpcMode"`
		Cgroup            string            `json:"Cgroup"`
		CgroupParent      string            `json:"CgroupParent"`
		PidMode           string            `json:"PidMode"`
		Privileged        bool              `json:"Privileged"`
		PublishAllPorts   bool              `json:"PublishAllPorts"`
		ReadonlyRootfs    bool              `json:"ReadonlyRootfs"`
		SecurityOpt       []string          `json:"SecurityOpt"`
		Tmpfs             map[string]string `json:"Tmpfs"`
		UTSMode           string            `json:"UTSMode"`
		UsernsMode        string            `json:"UsernsMode"`
		ShmSize           int64             `json:"ShmSize"`
		Sysctls           map[string]string `json:"Sysctls"`
		Runtime           string            `json:"Runtime"`
		Memory            int64             `json:"Memory"`
		MemoryReservation int64             `json:"MemoryReservation"`
		MemorySwap        int64             `json:"MemorySwap"`
		NanoCpus          int64             `json:"NanoCpus"`
		CPUShares         int64             `json:"CpuShares"`
		CpusetCpus        string            `json:"CpusetCpus"`
		PidsLimit         *int64            `json:"PidsLimit"`
		Init              *bool             `json:"Init"`
		Devices           []struct {
			PathOnHost, PathInContainer, CgroupPermissions string
		} `json:"Devices"`
		DeviceRequests []any `json:"DeviceRequests"`
		Ulimits        []struct {
			Name       string
			Soft, Hard int64
		} `json:"Ulimits"`
		LogConfig struct {
			Type   string            `json:"Type"`
			Config map[string]string `json:"Config"`
		} `json:"LogConfig"`
		Mounts []struct {
			Type     string `json:"Type"`
			Source   string `json:"Source"`
			Target   string `json:"Target"`
			ReadOnly bool   `json:"ReadOnly"`
		} `json:"Mounts"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]struct {
			Aliases    []string `json:"Aliases"`
			IPAMConfig *struct {
				IPv4Address string `json:"IPv4Address"`
				IPv6Address string `json:"IPv6Address"`
			} `json:"IPAMConfig"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

type portBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type healthcheck struct {
	Test        []string `json:"Test"`
	Interval    int64    `json:"Interval"`
	Timeout     int64    `json:"Timeout"`
	StartPeriod int64    `json:"StartPeriod"`
	Retries     int      `json:"Retries"`
}

// recreatePlan is everything needed to recreate a container, decided before anything changes.
type recreatePlan struct {
	Image   string
	Create  []string   // docker argv: create --name … IMAGE [cmd…]
	Env     []string   // NAME=value for the create's `-e NAME` flags (never on an argv)
	Connect [][]string // docker argv: network connect … (extra networks)
}

// defaultShm is Docker's default /dev/shm size.
const defaultShm = 64 << 20

// planRecreate turns an inspected container into the argv that recreates it, or refuses with every
// option it cannot reproduce. imageHealth is the image's own healthcheck (nil if none).
func planRecreate(c inspected, imageHealth *healthcheck) (recreatePlan, error) {
	name := strings.TrimPrefix(c.Name, "/")
	var plan recreatePlan
	var unsupported []string
	no := func(what string) { unsupported = append(unsupported, what) }

	img := c.Config.Image
	switch {
	case img == "" || strings.HasPrefix(img, "sha256:") || isHexID(img):
		no("it was created from an image id, not a name that can be pulled")
	}
	plan.Image = img
	h := c.HostConfig
	if len(h.VolumesFrom) > 0 {
		no("--volumes-from")
	}
	if len(h.Links) > 0 {
		no("legacy --link")
	}
	if len(h.DeviceRequests) > 0 {
		no("--gpus / device requests")
	}
	if h.Cgroup != "" || h.CgroupParent != "" {
		no("a custom cgroup")
	}
	for _, m := range []struct{ what, v string }{{"--ipc", h.IpcMode}, {"--pid", h.PidMode}, {"--network", h.NetworkMode}} {
		if strings.HasPrefix(m.v, "container:") {
			no(m.what + " sharing another container's namespace")
		}
	}

	args := []string{"create", "--name", name}
	// Environment: `-e NAME` flags, values through the process environment.
	envNames := map[string]string{}
	for _, kv := range c.Config.Env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		envNames[k] = v
	}
	keys := sortedKeys(envNames)
	for _, k := range keys {
		args = append(args, "-e", k)
		plan.Env = append(plan.Env, k+"="+envNames[k])
	}
	for _, k := range sortedKeys(c.Config.Labels) {
		args = append(args, "--label", k+"="+c.Config.Labels[k])
	}
	if c.Config.Hostname != "" && !strings.HasPrefix(c.ID, c.Config.Hostname) && h.NetworkMode != "host" {
		args = append(args, "--hostname", c.Config.Hostname)
	}
	if c.Config.Domainname != "" {
		args = append(args, "--domainname", c.Config.Domainname)
	}
	if c.Config.User != "" {
		args = append(args, "--user", c.Config.User)
	}
	if c.Config.WorkingDir != "" {
		args = append(args, "--workdir", c.Config.WorkingDir)
	}
	if c.Config.Tty {
		args = append(args, "--tty")
	}
	if c.Config.OpenStdin {
		args = append(args, "--interactive")
	}
	if c.Config.StopSignal != "" {
		args = append(args, "--stop-signal", c.Config.StopSignal)
	}
	if c.Config.StopTimeout != nil {
		args = append(args, "--stop-timeout", strconv.Itoa(*c.Config.StopTimeout))
	}
	// Healthcheck: the image's own is inherited; a different one is reproduced when it is a shell
	// command or "none", refused otherwise (an exec-form test has no exact flag).
	if hc := c.Config.Healthcheck; hc != nil && len(hc.Test) > 0 && !reflect.DeepEqual(hc, imageHealth) {
		switch hc.Test[0] {
		case "NONE":
			args = append(args, "--no-healthcheck")
		case "CMD-SHELL":
			args = append(args, "--health-cmd", strings.Join(hc.Test[1:], " "))
			if hc.Interval > 0 {
				args = append(args, "--health-interval", time.Duration(hc.Interval).String())
			}
			if hc.Timeout > 0 {
				args = append(args, "--health-timeout", time.Duration(hc.Timeout).String())
			}
			if hc.StartPeriod > 0 {
				args = append(args, "--health-start-period", time.Duration(hc.StartPeriod).String())
			}
			if hc.Retries > 0 {
				args = append(args, "--health-retries", strconv.Itoa(hc.Retries))
			}
		default:
			no("an exec-form healthcheck that differs from the image's")
		}
	}

	// Ports.
	for _, port := range sortedKeys(h.PortBindings) {
		for _, b := range h.PortBindings[port] {
			spec := port
			switch {
			case b.HostIP != "" && strings.Contains(b.HostIP, ":"):
				spec = "[" + b.HostIP + "]:" + b.HostPort + ":" + port
			case b.HostIP != "":
				spec = b.HostIP + ":" + b.HostPort + ":" + port
			case b.HostPort != "":
				spec = b.HostPort + ":" + port
			}
			args = append(args, "--publish", spec)
		}
	}
	if h.PublishAllPorts {
		args = append(args, "--publish-all")
	}

	// Storage: bind strings as given, --mount mounts, then anonymous/named volumes docker created
	// from the image's VOLUME lines, re-attached by name so their data stays.
	covered := map[string]bool{}
	for _, b := range h.Binds {
		args = append(args, "--volume", b)
		if parts := strings.Split(b, ":"); len(parts) >= 2 {
			covered[parts[1]] = true
		}
	}
	for _, m := range h.Mounts {
		switch m.Type {
		case "bind", "volume":
			spec := "type=" + m.Type + ",target=" + m.Target
			if m.Source != "" {
				spec += ",source=" + m.Source
			}
			if m.ReadOnly {
				spec += ",readonly"
			}
			args = append(args, "--mount", spec)
			covered[m.Target] = true
		case "tmpfs":
			args = append(args, "--mount", "type=tmpfs,target="+m.Target)
			covered[m.Target] = true
		default:
			no("a " + m.Type + " mount")
		}
	}
	for _, m := range c.Mounts {
		if covered[m.Destination] {
			continue
		}
		switch m.Type {
		case "volume":
			spec := m.Name + ":" + m.Destination
			if !m.RW {
				spec += ":ro"
			}
			args = append(args, "--volume", spec)
		case "bind":
			no("a bind mount at " + m.Destination + " that is not in its configuration")
		}
	}
	for _, p := range sortedKeys(h.Tmpfs) {
		spec := p
		if o := h.Tmpfs[p]; o != "" {
			spec += ":" + o
		}
		args = append(args, "--tmpfs", spec)
	}

	// Restart policy, runtime options, limits.
	if rp := h.RestartPolicy.Name; rp != "" && rp != "no" {
		if rp == "on-failure" && h.RestartPolicy.MaximumRetryCount > 0 {
			rp += ":" + strconv.Itoa(h.RestartPolicy.MaximumRetryCount)
		}
		args = append(args, "--restart", rp)
	}
	if h.AutoRemove {
		args = append(args, "--rm")
	}
	if h.Privileged {
		args = append(args, "--privileged")
	}
	if h.ReadonlyRootfs {
		args = append(args, "--read-only")
	}
	if h.Init != nil && *h.Init {
		args = append(args, "--init")
	}
	for _, f := range []struct {
		flag string
		vals []string
	}{{"--cap-add", h.CapAdd}, {"--cap-drop", h.CapDrop}, {"--dns", h.DNS}, {"--dns-option", h.DNSOptions},
		{"--dns-search", h.DNSSearch}, {"--add-host", h.ExtraHosts}, {"--group-add", h.GroupAdd}, {"--security-opt", h.SecurityOpt}} {
		for _, v := range f.vals {
			args = append(args, f.flag, v)
		}
	}
	for _, d := range h.Devices {
		spec := d.PathOnHost + ":" + d.PathInContainer
		if d.CgroupPermissions != "" {
			spec += ":" + d.CgroupPermissions
		}
		args = append(args, "--device", spec)
	}
	for _, k := range sortedKeys(h.Sysctls) {
		args = append(args, "--sysctl", k+"="+h.Sysctls[k])
	}
	for _, u := range h.Ulimits {
		args = append(args, "--ulimit", fmt.Sprintf("%s=%d:%d", u.Name, u.Soft, u.Hard))
	}
	switch h.IpcMode {
	case "host", "shareable":
		args = append(args, "--ipc", h.IpcMode)
	}
	if h.PidMode == "host" {
		args = append(args, "--pid", "host")
	}
	if h.UTSMode == "host" {
		args = append(args, "--uts", "host")
	}
	if h.UsernsMode == "host" {
		args = append(args, "--userns", "host")
	}
	if h.ShmSize != 0 && h.ShmSize != defaultShm {
		args = append(args, "--shm-size", strconv.FormatInt(h.ShmSize, 10))
	}
	if h.Runtime != "" && h.Runtime != "runc" {
		args = append(args, "--runtime", h.Runtime)
	}
	if h.Memory > 0 {
		args = append(args, "--memory", strconv.FormatInt(h.Memory, 10))
	}
	if h.MemoryReservation > 0 {
		args = append(args, "--memory-reservation", strconv.FormatInt(h.MemoryReservation, 10))
	}
	if h.MemorySwap > 0 || h.MemorySwap == -1 && h.Memory > 0 {
		args = append(args, "--memory-swap", strconv.FormatInt(h.MemorySwap, 10))
	}
	if h.NanoCpus > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(float64(h.NanoCpus)/1e9, 'f', -1, 64))
	}
	if h.CPUShares > 0 {
		args = append(args, "--cpu-shares", strconv.FormatInt(h.CPUShares, 10))
	}
	if h.CpusetCpus != "" {
		args = append(args, "--cpuset-cpus", h.CpusetCpus)
	}
	if h.PidsLimit != nil && *h.PidsLimit > 0 {
		args = append(args, "--pids-limit", strconv.FormatInt(*h.PidsLimit, 10))
	}
	if h.LogConfig.Type != "" {
		args = append(args, "--log-driver", h.LogConfig.Type)
		for _, k := range sortedKeys(h.LogConfig.Config) {
			args = append(args, "--log-opt", k+"="+h.LogConfig.Config[k])
		}
	}

	// Networks: the primary through --network (with its aliases and a static address), the others
	// connected before the first start.
	primary := h.NetworkMode
	nets := c.NetworkSettings.Networks
	netNames := sortedKeys(nets)
	switch primary {
	case "", "default":
		primary = "bridge"
	}
	netArgs := func(n string) (flags []string) {
		cfg, ok := nets[n]
		if !ok {
			return nil
		}
		for _, a := range cfg.Aliases {
			if a != name && !strings.HasPrefix(c.ID, a) && a != c.Config.Hostname {
				flags = append(flags, "--network-alias", a)
			}
		}
		if cfg.IPAMConfig != nil {
			if cfg.IPAMConfig.IPv4Address != "" {
				flags = append(flags, "--ip", cfg.IPAMConfig.IPv4Address)
			}
			if cfg.IPAMConfig.IPv6Address != "" {
				flags = append(flags, "--ip6", cfg.IPAMConfig.IPv6Address)
			}
		}
		return flags
	}
	switch primary {
	case "host", "none":
		args = append(args, "--network", primary)
	default:
		if primary != "bridge" {
			args = append(args, "--network", primary)
			args = append(args, netArgs(primary)...)
		} else if fl := netArgs("bridge"); len(fl) > 0 {
			args = append(args, "--network", "bridge")
			args = append(args, fl...)
		}
		for _, n := range netNames {
			if n == primary {
				continue
			}
			conn := []string{"network", "connect"}
			for _, f := range netArgs(n) {
				switch f {
				case "--network-alias":
					f = "--alias"
				}
				conn = append(conn, f)
			}
			plan.Connect = append(plan.Connect, append(conn, n, name))
		}
	}

	// Entrypoint and command, exactly as the process ran: the first entrypoint word through
	// --entrypoint, the rest before the command.
	var tail []string
	if len(c.Config.Entrypoint) > 0 {
		args = append(args, "--entrypoint", c.Config.Entrypoint[0])
		tail = append(tail, c.Config.Entrypoint[1:]...)
	} else {
		args = append(args, "--entrypoint", "")
	}
	tail = append(tail, c.Config.Cmd...)
	args = append(args, img)
	args = append(args, tail...)
	plan.Create = args

	if len(unsupported) > 0 {
		return recreatePlan{}, fmt.Errorf("%s cannot be recreated exactly — it uses %s. Nothing was changed.", name, strings.Join(unsupported, "; "))
	}
	return plan, nil
}

func isHexID(s string) bool {
	if len(s) < 12 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// asideName is where the old container waits while the new one starts.
func asideName(name, id string) string {
	short := id
	if len(short) > 12 {
		short = short[:12]
	}
	n := name + "-old-" + short
	if len(n) > 128 {
		n = n[:128]
	}
	return n
}

func (w *workloads) recreate(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.RecreateParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	out, err := w.quiet(oc, "docker", "inspect", "--type", "container", p.Name)
	if err != nil {
		return nil, 1, fmt.Errorf("container %s not found on this server", p.Name)
	}
	var ins []inspected
	if json.Unmarshal([]byte(out), &ins) != nil || len(ins) != 1 {
		return nil, 1, errors.New("docker inspect returned nothing usable")
	}
	c := ins[0]
	var imgHealth *healthcheck
	if iout, err := w.quiet(oc, "docker", "image", "inspect", "--format", "{{json .Config.Healthcheck}}", c.Image); err == nil {
		_ = json.Unmarshal([]byte(strings.TrimSpace(iout)), &imgHealth)
	}
	plan, err := planRecreate(c, imgHealth)
	if err != nil {
		return nil, 1, err
	}
	oc.Progress(1, 4)
	oc.Info("pulling %s", plan.Image)
	if _, err := w.cmd(oc, nil, "docker", "pull", plan.Image); err != nil {
		return nil, 1, fmt.Errorf("could not pull %s — nothing was changed: %v", plan.Image, err)
	}
	wasRunning := c.State.Running
	aside := asideName(p.Name, c.ID)

	oc.Progress(2, 4)
	if _, err := w.cmd(oc, nil, "docker", "rename", p.Name, aside); err != nil {
		return nil, 1, fmt.Errorf("could not set the old container aside — nothing was changed: %v", err)
	}
	restore := func(cause error) (any, int, error) {
		oc.Info("restoring the old container")
		_, _ = w.cmd(oc, nil, "docker", "rm", "-f", p.Name)
		if _, err := w.cmd(oc, nil, "docker", "rename", aside, p.Name); err != nil {
			return nil, 1, fmt.Errorf("%v — and the old container could not be renamed back: it is %s", cause, aside)
		}
		if wasRunning {
			_, _ = w.cmd(oc, nil, "docker", "start", p.Name)
		}
		return nil, 1, fmt.Errorf("%v — the old container is back as it was", cause)
	}
	if wasRunning {
		if _, err := w.cmd(oc, nil, "docker", "stop", aside); err != nil {
			return restore(fmt.Errorf("could not stop the old container: %v", err))
		}
	}
	oc.Progress(3, 4)
	idOut, err := w.cmd(oc, plan.Env, "docker", plan.Create...)
	if err != nil {
		return restore(fmt.Errorf("could not create the new container: %v", err))
	}
	for _, conn := range plan.Connect {
		if _, err := w.cmd(oc, nil, "docker", conn...); err != nil {
			return restore(fmt.Errorf("could not connect the new container to its networks: %v", err))
		}
	}
	oc.Progress(4, 4)
	if wasRunning {
		if _, err := w.cmd(oc, nil, "docker", "start", p.Name); err != nil {
			return restore(fmt.Errorf("the new container did not start: %v", err))
		}
		w.sleep(3 * time.Second)
		if st, err := w.containerStatus(oc, p.Name); err != nil || st != "running" {
			return restore(fmt.Errorf("the new container stopped right after starting (%s)", st))
		}
	}
	if _, err := w.cmd(oc, nil, "docker", "rm", aside); err != nil {
		oc.Info("note: the old container %s could not be removed: %v", aside, err)
	}
	oc.Info("%s recreated from %s", p.Name, plan.Image)
	return agentproto.RecreateResult{Name: p.Name, Image: plan.Image, ContainerID: clip(strings.TrimSpace(idOut), 64)}, 0, nil
}
