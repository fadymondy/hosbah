package agentproto

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Imported-workload control: operations on workloads Hosbah found running on a server (imported
// apps and data services) rather than ones it built. Two kinds of target:
//
//   - a Proxmox guest (LXC or QEMU VM) on a server that IS the hypervisor, controlled with `pvesh`
//     (and `vzdump` for backups) on that host;
//   - a Docker container on the server, controlled with the docker CLI.
//
// Typed like everything else: fixed params, validated on both sides of the wire, turned into fixed
// argv by the agent (never a shell). Hosbah's own workloads (IsHosbahOwned) are refused by the
// control plane AND by the agent, so a forged or mistaken target can never stop Hosbah itself.
const (
	KindWorkloadPower     = "workload.power"     // start | stop | shutdown | restart a guest or a container
	KindWorkloadLogs      = "workload.logs"      // docker logs of an imported container, bounded, relayed as the op's log
	KindWorkloadStats     = "workload.stats"     // current CPU, memory, disk and uptime of a guest or a container
	KindGuestResize       = "guest.resize"       // cores / memory (pvesh set …/config) and grow the root disk (…/resize)
	KindGuestSnapshot     = "guest.snapshot"     // create | rollback | delete | list Proxmox snapshots
	KindGuestBackup       = "guest.backup"       // vzdump (snapshot mode, zstd) to a backup-capable storage
	KindContainerRecreate = "container.recreate" // pull the image and recreate the container with the same configuration
	KindWorkloadDestroy   = "workload.destroy"   // stop and destroy a guest, or docker rm -f a container
	KindDBProbe           = "db.probe"           // check credentials for an imported database (client in the container, or TCP to a guest)
	// KindContainerInspect (adopt an imported app): a running container's image ref/digest, env,
	// published ports, labels, mounts and restart policy, read with `docker inspect` only — nothing
	// is started, stopped or changed. Env values are never logged (only their names may appear in a
	// progress line); the result itself still carries them, sealed by the caller into the app's env
	// vars before anything else touches it.
	KindContainerInspect = "container.inspect"
)

// Targets.
const (
	TargetGuest     = "guest"
	TargetContainer = "container"
)

// Power actions.
const (
	PowerStart    = "start"
	PowerStop     = "stop"     // a guest: hard stop; a container: docker stop
	PowerShutdown = "shutdown" // a guest: graceful ACPI/init shutdown; a container: docker stop (already graceful)
	PowerRestart  = "restart"  // a guest: reboot; a container: docker restart
)

// Snapshot actions.
const (
	SnapshotList     = "list"
	SnapshotCreate   = "create"
	SnapshotRollback = "rollback"
	SnapshotDelete   = "delete"
)

// Limits.
const (
	MaxWorkloadLogTail = 2000
	MaxResizeCores     = 512
	MaxResizeMemoryMB  = 4 << 20 // 4 TiB
	MaxDiskGrowGB      = 65536
)

var (
	vmidRE          = regexp.MustCompile(`^[1-9][0-9]{2,8}$`)
	containerNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	snapNameRE      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{1,39}$`)
	storageRE       = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)
	sinceRE         = regexp.MustCompile(`^([0-9]{1,7}[smh]|[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+(Z|[+-][0-9]{2}:[0-9]{2}))$`)
)

// IsHosbahOwned reports whether a container, unit or guest name is one of Hosbah's own: app processes
// (hosbah-<app>-<process>-<id>), release and restore-test runs, data services (hosbah-db-*), Caddy,
// frpc, the agent — all "hosbah-…", or "cloudy-…" when they were made before the rename. The importer never imports
// them, and every imported-workload operation refuses them on both sides of the wire.
func IsHosbahOwned(name string) bool {
	n := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "/"))
	return strings.HasPrefix(n, NamePrefix) || strings.HasPrefix(n, LegacyNamePrefix) || n == "hosbah" || n == "cloudy"
}

// ValidVMID: a Proxmox guest id (100 – 999999999).
func ValidVMID(s string) bool { return vmidRE.MatchString(s) }

// ValidGuestType: lxc or qemu.
func ValidGuestType(s string) bool { return s == "lxc" || s == "qemu" }

// ValidContainerName: a Docker container name (never an option: no leading dash).
func ValidContainerName(s string) bool { return containerNameRE.MatchString(s) }

// ValidSnapshotName: a Proxmox snapshot name.
func ValidSnapshotName(s string) bool { return snapNameRE.MatchString(s) && s != "current" }

// ValidStorage: a Proxmox storage id.
func ValidStorage(s string) bool { return storageRE.MatchString(s) }

// WorkloadRef names one workload. Label is the Hosbah record's name, carried so the server's
// operation log says which site or database an operation was for; the agent does not act on it.
type WorkloadRef struct {
	Target    string `json:"target"`               // guest | container
	VMID      string `json:"vmid,omitempty"`       // guest
	GuestType string `json:"guest_type,omitempty"` // guest: lxc | qemu
	Name      string `json:"name,omitempty"`       // container
	Label     string `json:"label,omitempty"`
}

// Validate checks the reference on both sides of the wire.
func (w WorkloadRef) Validate() error {
	if len(w.Label) > 64 || strings.ContainsAny(w.Label, "\r\n") {
		return errors.New("bad label")
	}
	switch w.Target {
	case TargetGuest:
		if !ValidVMID(w.VMID) || !ValidGuestType(w.GuestType) {
			return errors.New("a guest needs a vmid and a guest_type (lxc or qemu)")
		}
		if w.Name != "" && IsHosbahOwned(w.Name) {
			return fmt.Errorf("%s is one of Hosbah's own workloads", w.Name)
		}
	case TargetContainer:
		if !ValidContainerName(w.Name) {
			return fmt.Errorf("bad container name %q", w.Name)
		}
		if IsHosbahOwned(w.Name) {
			return fmt.Errorf("%s is one of Hosbah's own workloads", w.Name)
		}
	default:
		return errors.New("target is guest or container")
	}
	return nil
}

// Guest builds a guest reference.
func Guest(vmid, guestType, label string) WorkloadRef {
	return WorkloadRef{Target: TargetGuest, VMID: vmid, GuestType: guestType, Label: label}
}

// Container builds a container reference.
func Container(name, label string) WorkloadRef {
	return WorkloadRef{Target: TargetContainer, Name: name, Label: label}
}

// ---- workload.power --------------------------------------------------------------------------

// PowerParams: workload.power.
type PowerParams struct {
	WorkloadRef
	Action string `json:"action"`
}

func (p PowerParams) Validate() error {
	if err := p.WorkloadRef.Validate(); err != nil {
		return err
	}
	switch p.Action {
	case PowerStart, PowerStop, PowerShutdown, PowerRestart:
		return nil
	}
	return errors.New("action is start, stop, shutdown or restart")
}

// PowerResult: the status the workload reports afterwards (running, stopped, exited…).
type PowerResult struct {
	Status string `json:"status"`
}

// ---- workload.logs ---------------------------------------------------------------------------

// WorkloadLogsParams: workload.logs — a container's recent lines (then, optionally, following for a
// while), relayed as the operation's log like app.logs.
type WorkloadLogsParams struct {
	Name          string `json:"name"`
	Label         string `json:"label,omitempty"`
	Tail          int    `json:"tail"`
	Since         string `json:"since,omitempty"`          // "15m", "2h", or an RFC 3339 time
	FollowSeconds int    `json:"follow_seconds,omitempty"` // ≤ MaxLogFollowSeconds
}

func (p WorkloadLogsParams) Validate() error {
	if err := Container(p.Name, p.Label).Validate(); err != nil {
		return err
	}
	switch {
	case p.Tail < 0 || p.Tail > MaxWorkloadLogTail:
		return fmt.Errorf("tail is 0–%d", MaxWorkloadLogTail)
	case p.Since != "" && !sinceRE.MatchString(p.Since):
		return errors.New("since is a duration like 15m or 2h, or an RFC 3339 time")
	case p.FollowSeconds < 0 || p.FollowSeconds > MaxLogFollowSeconds:
		return fmt.Errorf("follow_seconds is 0–%d", MaxLogFollowSeconds)
	}
	return nil
}

// ---- workload.stats --------------------------------------------------------------------------

// StatsParams: workload.stats.
type StatsParams struct {
	WorkloadRef
}

func (p StatsParams) Validate() error { return p.WorkloadRef.Validate() }

// WorkloadStats: a reading. Zero means unknown (a stopped workload, or a figure the runtime does
// not report — a QEMU VM's disk use without its guest agent, a container's disk size limit).
type WorkloadStats struct {
	Status         string  `json:"status"`
	CPUPercent     float64 `json:"cpu_percent"`
	CPUs           float64 `json:"cpus,omitempty"`
	MemUsedBytes   uint64  `json:"mem_used_bytes"`
	MemTotalBytes  uint64  `json:"mem_total_bytes"`
	DiskUsedBytes  uint64  `json:"disk_used_bytes"`
	DiskTotalBytes uint64  `json:"disk_total_bytes"`
	UptimeSeconds  uint64  `json:"uptime_seconds"`
}

// ---- guest.resize ----------------------------------------------------------------------------

// ResizeParams: guest.resize. Zero leaves a figure as it is; the disk only grows.
type ResizeParams struct {
	VMID       string `json:"vmid"`
	GuestType  string `json:"guest_type"`
	Label      string `json:"label,omitempty"`
	Cores      int    `json:"cores,omitempty"`
	MemoryMB   int    `json:"memory_mb,omitempty"`
	DiskGrowGB int    `json:"disk_grow_gb,omitempty"`
}

func (p ResizeParams) Validate() error {
	if err := Guest(p.VMID, p.GuestType, p.Label).Validate(); err != nil {
		return err
	}
	switch {
	case p.Cores == 0 && p.MemoryMB == 0 && p.DiskGrowGB == 0:
		return errors.New("give cores, memory_mb or disk_grow_gb")
	case p.Cores < 0 || p.Cores > MaxResizeCores:
		return fmt.Errorf("cores is 1–%d", MaxResizeCores)
	case p.MemoryMB != 0 && (p.MemoryMB < 64 || p.MemoryMB > MaxResizeMemoryMB):
		return fmt.Errorf("memory_mb is 64–%d", MaxResizeMemoryMB)
	case p.DiskGrowGB < 0 || p.DiskGrowGB > MaxDiskGrowGB:
		return fmt.Errorf("disk_grow_gb grows the root disk by 1–%d GB (it never shrinks)", MaxDiskGrowGB)
	}
	return nil
}

// ResizeResult: what the guest has now.
type ResizeResult struct {
	Cores    int    `json:"cores,omitempty"`
	MemoryMB int    `json:"memory_mb,omitempty"`
	Disk     string `json:"disk,omitempty"` // the root disk that grew (rootfs, scsi0…)
	DiskSize string `json:"disk_size,omitempty"`
}

// ---- guest.snapshot --------------------------------------------------------------------------

// SnapshotParams: guest.snapshot.
type SnapshotParams struct {
	VMID        string `json:"vmid"`
	GuestType   string `json:"guest_type"`
	Label       string `json:"label,omitempty"`
	Action      string `json:"action"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

func (p SnapshotParams) Validate() error {
	if err := Guest(p.VMID, p.GuestType, p.Label).Validate(); err != nil {
		return err
	}
	switch p.Action {
	case SnapshotList:
		return nil
	case SnapshotCreate, SnapshotRollback, SnapshotDelete:
		if !ValidSnapshotName(p.Name) {
			return errors.New("a snapshot name is 2–40 letters, digits, - and _, starting with a letter")
		}
	default:
		return errors.New("action is create, rollback, delete or list")
	}
	if len(p.Description) > 200 || strings.ContainsAny(p.Description, "\r\n\x00") {
		return errors.New("a description is one line of up to 200 characters")
	}
	return nil
}

// Snapshot is one Proxmox snapshot.
type Snapshot struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parent      string `json:"parent,omitempty"`
	SnapTime    int64  `json:"snaptime,omitempty"` // unix seconds
	VMState     bool   `json:"vmstate,omitempty"`
}

// SnapshotResult: the guest's snapshots after the action.
type SnapshotResult struct {
	Snapshots []Snapshot `json:"snapshots"`
}

// ---- guest.backup ----------------------------------------------------------------------------

// GuestBackupParams: guest.backup.
type GuestBackupParams struct {
	VMID      string `json:"vmid"`
	GuestType string `json:"guest_type"`
	Label     string `json:"label,omitempty"`
	Storage   string `json:"storage,omitempty"` // "" = the node's first backup-capable storage
}

func (p GuestBackupParams) Validate() error {
	if err := Guest(p.VMID, p.GuestType, p.Label).Validate(); err != nil {
		return err
	}
	if p.Storage != "" && !ValidStorage(p.Storage) {
		return errors.New("bad storage id")
	}
	return nil
}

// GuestBackupResult: the archive.
type GuestBackupResult struct {
	Storage   string `json:"storage"`
	VolID     string `json:"volid"`
	SizeBytes uint64 `json:"size_bytes,omitempty"`
}

// ---- container.recreate ----------------------------------------------------------------------

// RecreateParams: container.recreate.
type RecreateParams struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
}

func (p RecreateParams) Validate() error { return Container(p.Name, p.Label).Validate() }

// RecreateResult: the new container.
type RecreateResult struct {
	Name        string `json:"name"`
	Image       string `json:"image"`
	ContainerID string `json:"container_id,omitempty"`
}

// ---- container.inspect ------------------------------------------------------------------------

// ContainerInspectParams: container.inspect. Read-only — nothing about the container changes.
type ContainerInspectParams struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
}

func (p ContainerInspectParams) Validate() error { return Container(p.Name, p.Label).Validate() }

// ContainerInspectResult: enough of a running container's configuration to adopt it into a Hosbah
// app without a rebuild. Env holds the container's actual environment values — the agent never logs
// them, and the control plane must seal them with the team DEK before they touch anything else.
type ContainerInspectResult struct {
	Status        string            `json:"status"`
	Image         string            `json:"image"`
	ImageDigest   string            `json:"image_digest,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Ports         []string          `json:"ports,omitempty"` // e.g. "8080/tcp -> 0.0.0.0:3000"
	Labels        map[string]string `json:"labels,omitempty"`
	Mounts        []string          `json:"mounts,omitempty"` // e.g. "/host/path -> /container/path"
	RestartPolicy string            `json:"restart_policy,omitempty"`
}

// ---- workload.destroy ------------------------------------------------------------------------

// DestroyParams: workload.destroy. Purge: a guest is also removed from backup jobs, replication and
// HA (pvesh delete … --purge 1); a container's anonymous volumes go with it (docker rm -v).
type DestroyParams struct {
	WorkloadRef
	Purge bool `json:"purge,omitempty"`
}

func (p DestroyParams) Validate() error { return p.WorkloadRef.Validate() }

// ---- db.probe --------------------------------------------------------------------------------

// DBProbeParams: db.probe. Credentials is a box for DBSecretContext(ServiceID, "root") holding
// {"user", "password", "database"}. A container is checked with the engine's own client inside it;
// a guest by a TCP connection from the hypervisor to Host:Port (the credentials are then only
// stored, not verified — there is no client for them on the hypervisor).
type DBProbeParams struct {
	ServiceID   string `json:"service_id"`
	Label       string `json:"label,omitempty"`
	Kind        string `json:"kind"`
	Target      string `json:"target"` // container | guest
	Container   string `json:"container,omitempty"`
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
	Credentials string `json:"credentials"`
}

func (p DBProbeParams) Validate() error {
	if !ValidID(p.ServiceID) {
		return errors.New("bad service id")
	}
	switch p.Kind {
	case "postgres", "mysql", "redis":
	default:
		return fmt.Errorf("probing %s credentials is not supported", p.Kind)
	}
	switch p.Target {
	case TargetContainer:
		if err := Container(p.Container, p.Label).Validate(); err != nil {
			return err
		}
	case TargetGuest:
		if _, err := ParseUpstream(net.JoinHostPort(p.Host, strconv.Itoa(p.Port))); err != nil || p.Host == "" || p.Port < 1 || p.Port > 65535 {
			return errors.New("a guest probe needs the guest's host and port")
		}
	default:
		return errors.New("target is container or guest")
	}
	if p.Credentials == "" {
		return errors.New("no credentials to probe")
	}
	return nil
}

// DBProbeResult: whether the credentials worked.
type DBProbeResult struct {
	OK       bool   `json:"ok"`
	Verified bool   `json:"verified"` // false: only the TCP port answered (a guest)
	Version  string `json:"version,omitempty"`
	Detail   string `json:"detail,omitempty"`
}
