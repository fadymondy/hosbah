package agentproto

import (
	"errors"
	"fmt"
)

// Resource limits (MH-574 E13): memory and CPU caps carried on app.start / app.rollback (StartParams)
// and db.provision (DBProvisionParams), and re-applied live to running containers with app.limits /
// db.limits — a `docker update`, no restart. Nil/zero means "no limit" (Docker's own default): the
// docker run/update argv simply omits --memory/--cpus.
const (
	MinMemoryMB, MaxMemoryMB = 32, 1024 * 1024 // 32 MiB .. 1 TiB
	MinCPUs, MaxCPUs         = 0.05, 256.0

	KindAppLimits = "app.limits" // live docker update of one or more of an app's containers, no redeploy
	KindDBLimits  = "db.limits"  // live docker update of a data service's container
)

// ResourceLimits: MemoryMB 0 = unlimited, CPUs 0 = unlimited. Both bounded when set.
type ResourceLimits struct {
	MemoryMB int     `json:"memory_mb,omitempty"`
	CPUs     float64 `json:"cpus,omitempty"`
}

// Validate: both fields are optional, but a non-zero one must be in range.
func (r ResourceLimits) Validate() error {
	if r.MemoryMB != 0 && (r.MemoryMB < MinMemoryMB || r.MemoryMB > MaxMemoryMB) {
		return fmt.Errorf("memory_mb is 0 (unlimited) or %d to %d", MinMemoryMB, MaxMemoryMB)
	}
	if r.CPUs != 0 && (r.CPUs < MinCPUs || r.CPUs > MaxCPUs) {
		return fmt.Errorf("cpus is 0 (unlimited) or %g to %g", MinCPUs, MaxCPUs)
	}
	return nil
}

// Empty reports no limit is set at all (docker run gets neither flag).
func (r ResourceLimits) Empty() bool { return r.MemoryMB == 0 && r.CPUs == 0 }

// DockerArgs is the --memory/--memory-swap/--cpus flags for `docker run` or `docker update`.
// --memory-swap equals --memory: no extra swap beyond the memory limit.
func (r ResourceLimits) DockerArgs() []string {
	var args []string
	if r.MemoryMB != 0 {
		mb := fmt.Sprintf("%dm", r.MemoryMB)
		args = append(args, "--memory", mb, "--memory-swap", mb)
	}
	if r.CPUs != 0 {
		args = append(args, "--cpus", trimFloat(r.CPUs))
	}
	return args
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.4f", f)
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

// AppLimitsParams: app.limits — a live `docker update` of the named containers (this app's live
// release only; the agent never guesses a container by anything but the exact name it is given).
type AppLimitsParams struct {
	AppID      string         `json:"app_id"`
	Containers []string       `json:"containers"`
	Resources  ResourceLimits `json:"resources"`
}

func (p AppLimitsParams) Validate() error {
	if !ValidID(p.AppID) {
		return errors.New("bad app id")
	}
	if len(p.Containers) == 0 || len(p.Containers) > 32 {
		return errors.New("app.limits needs 1 to 32 container names")
	}
	for _, c := range p.Containers {
		if c == "" || len(c) > 255 {
			return errors.New("bad container name")
		}
	}
	return p.Resources.Validate()
}

type AppLimitsResult struct {
	Applied []string `json:"applied"`
}

// DBLimitsParams: db.limits — a live `docker update` of a data service's container.
type DBLimitsParams struct {
	ServiceID string         `json:"service_id"`
	Container string         `json:"container"`
	Resources ResourceLimits `json:"resources"`
}

func (p DBLimitsParams) Validate() error {
	if !ValidID(p.ServiceID) {
		return errors.New("bad service id")
	}
	if p.Container == "" || len(p.Container) > 255 {
		return errors.New("bad container name")
	}
	return p.Resources.Validate()
}

type DBLimitsResult struct {
	Applied bool `json:"applied"`
}
