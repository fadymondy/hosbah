package agentproto

import (
	"errors"
	"fmt"
	"regexp"
)

// service.list / service.action (MH-576 D3): systemd units of type .service on the host. Read-only
// listing needs no params beyond an optional filter; actions are start/stop/restart/reload of one
// unit. The agent's own unit (hosbah-agent.service, and the legacy cloudy-agent.service it hands
// over from) can never be targeted here — stopping or restarting itself through its own operation
// queue is a deadlock (and a way to end a session with no way back without a human at the console).

// ServiceListParams: service.list.
type ServiceListParams struct {
	Query string `json:"query,omitempty" doc:"Case-insensitive substring match on the unit name."`
}

// ServiceUnit is one systemd service unit as `systemctl` reports it.
type ServiceUnit struct {
	Name        string `json:"name"`   // e.g. "caddy.service"
	Description string `json:"description,omitempty"`
	LoadState   string `json:"load_state"`   // loaded, not-found, ...
	ActiveState string `json:"active_state"` // active, inactive, failed, activating, ...
	SubState    string `json:"sub_state"`    // running, dead, exited, ...
	Enabled     bool   `json:"enabled"`
}

// ServiceListResult: service.list.
type ServiceListResult struct {
	Units []ServiceUnit `json:"units"`
}

// unitNameRE: a strict systemd unit name — letters, digits and -_.\@: plus the .service suffix.
// Never a shell metacharacter, never a path.
var unitNameRE = regexp.MustCompile(`^[A-Za-z0-9_.\-@:]{1,255}\.service$`)

// ProtectedUnits are refused for service.action on both sides: the agent's own unit under either
// name (see CLAUDE.md: on first start after an update it hands over from cloudy-agent.service).
var ProtectedUnits = []string{"hosbah-agent.service", "cloudy-agent.service"}

// ValidUnitName reports whether name is a strict, safe systemd .service unit name.
func ValidUnitName(name string) bool { return unitNameRE.MatchString(name) }

// ProtectedUnit reports whether name is a unit service.action must always refuse.
func ProtectedUnit(name string) bool {
	for _, p := range ProtectedUnits {
		if name == p {
			return true
		}
	}
	return false
}

// Validate checks service.list's params.
func (p ServiceListParams) Validate() error {
	if len(p.Query) > 200 {
		return errors.New("query is too long")
	}
	return nil
}

// ServiceActionParams: service.action.
type ServiceActionParams struct {
	Unit   string `json:"unit"`
	Action string `json:"action"` // start | stop | restart | reload
}

// ServiceActionResult: service.action.
type ServiceActionResult struct {
	Unit        string `json:"unit"`
	ActiveState string `json:"active_state"`
	SubState    string `json:"sub_state"`
}

// Validate checks service.action's params, on both sides of the wire.
func (p ServiceActionParams) Validate() error {
	if !ValidUnitName(p.Unit) {
		return errors.New("not a valid systemd .service unit name")
	}
	if ProtectedUnit(p.Unit) {
		return fmt.Errorf("%s manages the agent itself and cannot be controlled here", p.Unit)
	}
	switch p.Action {
	case "start", "stop", "restart", "reload":
	default:
		return errors.New("action must be start, stop, restart or reload")
	}
	return nil
}
