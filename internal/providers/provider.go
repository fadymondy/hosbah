// Package providers defines the driver contract through which Cloudy obtains servers from an
// infrastructure provider (Proxmox VE, clouds). Docker is the runtime on every server; a driver's
// only job is to create, inspect, power and destroy the machine itself, and to get the Cloudy agent
// onto it — either by cloud-init user-data (see AgentUserData) or, where the provider cannot
// deliver user-data, by the SSH bootstrap in package sshboot.
//
// Drivers register a Factory under their key in an init function; the control plane constructs one
// per stored credential with New. The credential is bound at construction, so every method works
// against that one account.
package providers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Caps reports which optional operations a driver supports.
type Caps struct{ Create, Power, Snapshot, Firewall, PrivateNet, Volumes, RDNS bool }

// Credential is what a team stores for one provider account. Config holds non-secret settings;
// Secret holds the one secret value (an API token secret, for example). Drivers must never log
// Secret or include it in errors.
type Credential struct {
	Config map[string]string
	Secret string
}

// Region is a placement target: a cloud region, or a Proxmox node.
type Region struct{ ID, Name, Status string }

// Size is a machine shape.
type Size struct {
	ID, Name string
	CPUs     int
	MemoryMB int
	DiskGB   int
}

// Image is something a machine can be created from. Kind is "vm" or "lxc".
type Image struct{ ID, Name, Kind string }

// Machine kinds.
const (
	KindVM  = "vm"
	KindLXC = "lxc"
)

// CreateSpec describes a machine to create.
type CreateSpec struct {
	Name, Region, Size, Image string
	// Kind is "vm" or "lxc"; drivers without containers ignore it.
	Kind string
	// SSHKeyIDs are ids returned by EnsureSSHKey, for providers with a key registry.
	SSHKeyIDs []string
	// SSHPublicKeys are authorized_keys lines put on the guest directly, for providers without a
	// key registry (Proxmox).
	SSHPublicKeys []string
	// UserData is cloud-init user-data that installs and enrols the agent.
	UserData string
	Tags     []string
	// Options are provider-specific settings. Proxmox understands "ipconfig0" ("ip=dhcp" by
	// default, or "ip=10.10.10.50/24,gw=10.10.10.1"), "nameserver" (LXC), "cores", "memory_mb" and "disk_gb".
	Options map[string]string
}

// Machine statuses.
const (
	StatusRunning  = "running"
	StatusStopped  = "stopped"
	StatusCreating = "creating"
	StatusUnknown  = "unknown"
)

// Machine is the observed state of a created machine. Status is one of the Status* constants.
type Machine struct{ ID, Name, Status, IPv4, IPv6, PrivateIP string }

// PowerAction is a power operation for Provider.Power.
type PowerAction string

// Power actions.
const (
	PowerStart    PowerAction = "start"
	PowerStop     PowerAction = "stop"
	PowerShutdown PowerAction = "shutdown"
	PowerReboot   PowerAction = "reboot"
)

// ErrUnsupported is returned by operations a provider does not implement.
var ErrUnsupported = errors.New("providers: not supported by this provider")

// Provider is one provider account's driver.
type Provider interface {
	Key() string
	Capabilities() Caps
	// Validate checks that the bound credential works and can see what it needs.
	Validate(ctx context.Context) error
	Regions(ctx context.Context) ([]Region, error)
	Sizes(ctx context.Context, region string) ([]Size, error)
	Images(ctx context.Context, region string) ([]Image, error)
	// EnsureSSHKey registers pub with the provider (if it has a key registry) and returns its id.
	EnsureSSHKey(ctx context.Context, pub string) (string, error)
	Create(ctx context.Context, s CreateSpec) (providerID string, err error)
	Get(ctx context.Context, id string) (Machine, error)
	// Delete destroys the machine. Deleting a machine that no longer exists succeeds.
	Delete(ctx context.Context, id string) error
	Power(ctx context.Context, id string, a PowerAction) error
}

// UserDataDelivery is implemented by drivers that can deliver CreateSpec.UserData only for some
// kinds. When DeliversUserData reports false, the control plane bootstraps the agent over SSH with
// the per-server key instead. Drivers that do not implement it are assumed to always deliver.
type UserDataDelivery interface{ DeliversUserData(kind string) bool }

// Factory constructs a driver bound to one credential.
type Factory func(cred Credential) (Provider, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register makes a driver available under key. It panics on a duplicate or nil factory, since both
// are programming errors caught at start-up.
func Register(key string, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if f == nil {
		panic("providers: Register with nil factory for " + key)
	}
	if _, dup := registry[key]; dup {
		panic("providers: Register called twice for " + key)
	}
	registry[key] = f
}

// New constructs the driver registered under key, bound to cred.
func New(key string, cred Credential) (Provider, error) {
	registryMu.RLock()
	f, ok := registry[key]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("providers: unknown provider %q", key)
	}
	return f(cred)
}

// Keys lists the registered provider keys, sorted.
func Keys() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	keys := make([]string, 0, len(registry))
	for k := range registry {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
