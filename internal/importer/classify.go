// Package importer turns the workloads an agent discovers on a server (containers, systemd units,
// Proxmox guests — agentproto.Service in its system.info facts) into Hosbah records: apps with
// source "imported" (sites) and data services with source "imported" (databases).
//
// Importing never acts on a workload: it only records it. Control (power, stats, logs, snapshots,
// resize, backup, recreate, destroy, domains, database credentials) happens only when a member asks,
// through typed agent operations (internal/deploy/workloads.go); git-only actions (deploy, rollback,
// env, build settings, deploy hook) are refused with 409 imported_workload. Sync is idempotent; a workload that is no longer discovered is marked missing, never deleted; a
// record the user deleted is tombstoned in import_ignores and stays gone.
package importer

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// Targets of an imported item.
const (
	TargetApp         = "app"
	TargetDataService = "data_service"
)

// Imported states.
const (
	StateRunning = "running"
	StateStopped = "stopped"
	StateMissing = "missing"
)

// Origin is what discovery reported about a workload (apps.origin / data_services.origin).
type Origin struct {
	Image     string   `json:"image,omitempty" doc:"Container image (containers)"`
	Ports     []string `json:"ports" doc:"Published ports as discovery reported them"`
	Address   string   `json:"address,omitempty" doc:"Guest address (Proxmox guests)"`
	GuestType string   `json:"guest_type,omitempty" enum:"lxc,qemu" doc:"Proxmox guest type"`
	VMID      string   `json:"vmid,omitempty" doc:"Proxmox guest id"`
	CPUs      int      `json:"cpus,omitempty"`
	MemBytes  uint64   `json:"mem_bytes,omitempty"`
	DiskBytes uint64   `json:"disk_bytes,omitempty"`
	Status    string   `json:"status" doc:"The raw status discovery reported (running, exited, active, stopped…)"`
}

// Item is one discovered workload, classified.
type Item struct {
	Target     string // TargetApp | TargetDataService
	OriginKind string // container | systemd | guest
	OriginKey  string // container name, unit name, or guest:<vmid>
	Name       string // the workload's own name (made valid and unique at insert)
	State      string // running | stopped
	Origin     Origin
	// Data services only.
	Kind    string // postgres | mysql | redis | mongodb | minio
	Version string
	Host    string // bind address of the published port ("" = every interface), or the guest address
	Port    int    // first published host port (0 = none)
}

// Classify maps discovered services to imported apps and data services. Pure: no I/O.
//   - Hosbah's own workloads (hosbah-* / pre-rename cloudy-* containers, units and guests) are skipped.
//   - A container whose image is a database engine is a data service; every other container is an app.
//   - A Proxmox guest whose name has a database token (pg, postgres, mysql, maria, redis, mongo,
//     minio…) is a data service; every other guest is an app.
//   - A systemd unit is an app only if it publishes ports and is not a base-system daemon.
func Classify(services []agentproto.Service) []Item {
	var out []Item
	seen := map[string]bool{}
	for _, s := range services {
		it, ok := classifyOne(s)
		if !ok || seen[it.OriginKey] {
			continue
		}
		seen[it.OriginKey] = true
		out = append(out, it)
	}
	return out
}

func classifyOne(s agentproto.Service) (Item, bool) {
	name := strings.TrimSpace(strings.TrimPrefix(s.Name, "/"))
	it := Item{OriginKind: s.Kind, Name: name, State: stateOf(s.Status),
		Origin: Origin{Image: s.Image, Ports: nonNil(s.Ports), Status: s.Status}}
	switch s.Kind {
	case "container":
		if name == "" || IsHosbahOwned(name) {
			return Item{}, false
		}
		it.OriginKey = name
		it.Target = TargetApp
		if kind := DBKindOfImage(s.Image); kind != "" {
			it.Target, it.Kind, it.Version = TargetDataService, kind, ImageTag(s.Image)
			it.Host, it.Port = FirstPublished(s.Ports)
		}
		return it, true
	case "guest":
		// Hosbah's own guests (its control plane, its demo servers) are not sites either.
		if s.ID == "" || IsHosbahOwned(name) {
			return Item{}, false
		}
		if name == "" {
			name = s.Type + "-" + s.ID
			it.Name = name
		}
		it.OriginKey = "guest:" + s.ID
		it.Origin.Image = ""
		it.Origin.Address, it.Origin.GuestType, it.Origin.VMID = s.Address, s.Type, s.ID
		it.Origin.CPUs, it.Origin.MemBytes, it.Origin.DiskBytes = s.CPUs, s.MemBytes, s.DiskBytes
		it.Target = TargetApp
		if kind := DBKindOfGuestName(name); kind != "" {
			it.Target, it.Kind, it.Version, it.Host = TargetDataService, kind, "unknown", s.Address
		}
		return it, true
	case "systemd":
		unit := strings.TrimSuffix(name, ".service")
		if unit == "" || IsHosbahOwned(unit) || IsBaseDaemon(unit) || len(s.Ports) == 0 {
			return Item{}, false
		}
		it.Name, it.OriginKey, it.Target = unit, unit, TargetApp
		return it, true
	}
	return Item{}, false
}

// IsHosbahOwned: Hosbah's own containers and units — app processes (hosbah-<app>-<process>-<id>),
// release and restore-test runs, data services (hosbah-db-*), Caddy (hosbah-caddy), frpc
// (hosbah-frpc), the agent — all start with "hosbah-", or "cloudy-" when made before the rename. The same
// check the agent applies to every imported-workload operation (agentproto.IsHosbahOwned).
func IsHosbahOwned(name string) bool { return agentproto.IsHosbahOwned(name) }

// baseDaemons are units of the operating system or of the host's own plumbing, never a site.
var baseDaemons = []string{"sshd", "ssh", "cron", "crond", "dbus", "chrony", "chronyd", "rsyslog", "syslog", "snapd",
	"getty", "networkd", "resolved", "polkit", "udisks", "udisks2", "unattended-upgrades", "docker", "containerd",
	"corosync", "rrdcached", "postfix", "exim4", "ntp", "ntpd", "networking", "network-manager", "networkmanager",
	"accounts-daemon", "atd", "irqbalance", "multipathd", "iscsid", "open-iscsi", "lvm2", "zfs", "smartd", "ksmtuned",
	"apparmor", "auditd", "firewalld", "ufw", "fail2ban", "ceph", "watchdog-mux", "cloud-init", "qemu-guest-agent"}

// baseDaemonPrefixes cover families (systemd-*, pve*, qemu*, lxc*, spice*…).
var baseDaemonPrefixes = []string{"systemd-", "pve", "qemu", "lxc", "spice", "getty@", "user@", "dbus-", "snap.", "ceph-",
	"proxmox-", "cloud-", "polkit", "udisks", "networkd", "resolved"}

// IsBaseDaemon reports whether a systemd unit is part of the base system.
func IsBaseDaemon(unit string) bool {
	u := strings.ToLower(strings.TrimSuffix(unit, ".service"))
	for _, d := range baseDaemons {
		if u == d {
			return true
		}
	}
	for _, p := range baseDaemonPrefixes {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

// imageTools are companion images named after an engine that are not the engine (web UIs,
// exporters, CLIs): mongo-express, redis-commander, postgres-exporter, redisinsight…
var imageTools = regexp.MustCompile(`(express|commander|exporter|insight|admin|backup|ui|web|operator|proxy|cli|client)`)

// DBKindOfImage maps a container image to a data-service kind, or "" when it is not a database
// engine. The repository's last path segment is what counts (bitnami/postgresql, postgis/postgis).
func DBKindOfImage(image string) string {
	repo := imageRepo(image)
	if repo == "" {
		return ""
	}
	base := repo
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if imageTools.MatchString(base) {
		return ""
	}
	switch {
	case strings.HasPrefix(base, "postgres"), strings.HasPrefix(base, "postgis"), strings.HasPrefix(base, "timescale"),
		strings.Contains(repo, "timescale/"), strings.Contains(repo, "postgis/"):
		return "postgres"
	case strings.HasPrefix(base, "mysql"), strings.HasPrefix(base, "mariadb"):
		return "mysql"
	case strings.HasPrefix(base, "redis"), strings.HasPrefix(base, "valkey"):
		return "redis"
	case strings.HasPrefix(base, "mongo"):
		return "mongodb"
	case base == "minio":
		return "minio"
	}
	return ""
}

// guestDBTokens are whole name tokens that mark a Proxmox guest as a database.
var guestDBTokens = map[string]string{
	"pg": "postgres", "postgres": "postgres", "postgresql": "postgres", "psql": "postgres", "pgsql": "postgres",
	"postgis": "postgres", "timescale": "postgres", "timescaledb": "postgres",
	"mysql": "mysql", "maria": "mysql", "mariadb": "mysql",
	"redis": "redis", "valkey": "redis",
	"mongo": "mongodb", "mongodb": "mongodb",
	"minio": "minio",
}

var tokenSplit = regexp.MustCompile(`[^a-z0-9]+`)

// DBKindOfGuestName maps a guest name to a data-service kind when one of its tokens (split on
// anything but letters and digits) names an engine: "pg-main", "db.mysql", "redis01" → no (redis01
// is one token), "redis-01" → redis.
func DBKindOfGuestName(name string) string {
	for _, t := range tokenSplit.Split(strings.ToLower(name), -1) {
		if k, ok := guestDBTokens[t]; ok {
			return k
		}
	}
	return ""
}

// imageRepo is the image without its tag and digest, lowercased.
func imageRepo(image string) string {
	img := strings.ToLower(strings.TrimSpace(image))
	img, _, _ = strings.Cut(img, "@")
	if i := strings.LastIndex(img, ":"); i > strings.LastIndex(img, "/") {
		img = img[:i]
	}
	return img
}

// ImageTag is the image's tag ("latest" when it has none).
func ImageTag(image string) string {
	img := strings.TrimSpace(image)
	img, _, _ = strings.Cut(img, "@")
	if i := strings.LastIndex(img, ":"); i > strings.LastIndex(img, "/") && i+1 < len(img) {
		return img[i+1:]
	}
	return "latest"
}

// Published is one host-published port: its bind address ("" = every interface) and host port.
type Published struct {
	Host string
	Port int
}

// PublishedPorts is every host-published port in Docker's Ports column, in order, without repeats
// (Docker lists 0.0.0.0 and :: separately for one binding).
func PublishedPorts(ports []string) []Published {
	var out []Published
	seen := map[Published]bool{}
	for _, p := range ports {
		for _, part := range strings.Split(p, ",") {
			h, n := FirstPublished([]string{part})
			if n == 0 {
				continue
			}
			pp := Published{Host: h, Port: n}
			if !seen[pp] {
				seen[pp] = true
				out = append(out, pp)
			}
		}
	}
	return out
}

// FirstPublished is the first host-published port in Docker's Ports column
// ("0.0.0.0:5432->5432/tcp", "127.0.0.1:6379->6379/tcp", ":::8080->80/tcp"). host is the bind
// address when it is a specific one, "" for every interface.
func FirstPublished(ports []string) (host string, port int) {
	for _, p := range ports {
		for _, part := range strings.Split(p, ",") {
			left, _, ok := strings.Cut(strings.TrimSpace(part), "->")
			if !ok {
				continue
			}
			i := strings.LastIndex(left, ":")
			if i < 0 {
				continue
			}
			n, err := strconv.Atoi(left[i+1:])
			if err != nil || n < 1 || n > 65535 {
				// a range ("8000-8010") publishes its first port
				a, _, _ := strings.Cut(left[i+1:], "-")
				if n, err = strconv.Atoi(a); err != nil || n < 1 || n > 65535 {
					continue
				}
			}
			h := strings.Trim(left[:i], "[]")
			if h == "0.0.0.0" || h == "::" || h == ":" || h == "" {
				h = ""
			}
			return h, n
		}
	}
	return "", 0
}

func stateOf(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch {
	case s == "running", s == "active", s == "restarting", s == "activating", s == "reloading", strings.HasPrefix(s, "up"):
		return StateRunning
	}
	return StateStopped
}

func nonNil(p []string) []string {
	if p == nil {
		return []string{}
	}
	return p
}

var nameJunk = regexp.MustCompile(`[^a-z0-9]+`)

// maxName is the longest app / data-service name (apps_name_check, data_services_name_check).
const maxName = 40

// Slug makes a name valid for ^[a-z0-9][a-z0-9-]{0,39}$; fallback when nothing usable is left.
func Slug(name, fallback string) string {
	s := strings.Trim(nameJunk.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > maxName {
		s = strings.Trim(s[:maxName], "-")
	}
	if s == "" {
		s = fallback
	}
	return s
}

// UniqueName is Slug(name) made unique against used with a numeric suffix (-2, -3…); it records
// the result in used.
func UniqueName(name, fallback string, used map[string]bool) string {
	base := Slug(name, fallback)
	cand := base
	for n := 2; used[cand]; n++ {
		suf := "-" + strconv.Itoa(n)
		b := base
		if len(b)+len(suf) > maxName {
			b = strings.TrimRight(b[:maxName-len(suf)], "-")
		}
		cand = b + suf
	}
	used[cand] = true
	return cand
}
