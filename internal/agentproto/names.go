package agentproto

import "strings"

/*
Names of what Hosbah creates on a server, and the names they had before the rename from Cloudy.

Every derived name (containers, volumes, images, labels, Caddy route ids) is emitted with the
hosbah prefix. Servers that ran an agent before the rename still hold objects named cloudy-…,
cloudy/<app> and labelled cloudy.*, and those keep working:

  - validation on both sides of the wire accepts either prefix of a name;
  - the agent looks containers up under both label sets (a drain stops old-labelled ones too);
  - a derived volume, data-service container or install/browser container that already exists
    under its old name keeps that name (ResolveName, in the agent), so no data is orphaned;
    only objects created after the rename get the new name.
*/
const (
	NamePrefix       = "hosbah-" // containers, volumes, units
	LegacyNamePrefix = "cloudy-"

	LabelPrefix       = "hosbah." // docker labels
	LegacyLabelPrefix = "cloudy."

	ImagePrefix       = "hosbah/" // image repositories
	LegacyImagePrefix = "cloudy/"

	// StateDir holds the agent's identity and its small files (frpc.toml, the gh login);
	// WorkDir its checkouts, builds, route table and local backup repositories.
	StateDir       = "/etc/hosbah"
	LegacyStateDir = "/etc/cloudy"
	WorkDir        = "/var/lib/hosbah"
	LegacyWorkDir  = "/var/lib/cloudy"
)

// The agent on a server: its binary and systemd unit (cloudy-agent before the rename). The install
// script writes AgentUnitFile; an agent that still runs as cloudy-agent moves itself over on its
// first start (internal/agent, migrateService).
const (
	AgentUnit           = "hosbah-agent"
	AgentBin            = "/usr/local/bin/hosbah-agent"
	AgentUnitPath       = "/etc/systemd/system/hosbah-agent.service"
	LegacyAgentUnit     = "cloudy-agent"
	LegacyAgentBin      = "/usr/local/bin/cloudy-agent"
	LegacyAgentUnitPath = "/etc/systemd/system/cloudy-agent.service"

	// AgentDistPrefix names the agent builds a control plane serves: hosbah-agent-linux-<arch>.
	// Control planes before the rename served (and their agent.update fetched) cloudy-agent-linux-<arch>.
	AgentDistPrefix       = "hosbah-agent-linux-"
	LegacyAgentDistPrefix = "cloudy-agent-linux-"
)

// AgentUnitFile is hosbah-agent.service. Conflicts= makes the move from the pre-rename unit one
// systemd transaction: starting this unit stops cloudy-agent.service first (After= orders the stop
// before the start), so the two never run at once, and neither depends on the other surviving.
const AgentUnitFile = `[Unit]
Description=Hosbah agent
Documentation=https://hosbah.com
After=network-online.target cloudy-agent.service
Wants=network-online.target
Conflicts=cloudy-agent.service

[Service]
ExecStart=/usr/local/bin/hosbah-agent run
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`

// Docker labels of an app's release containers.
const (
	LabelApp         = LabelPrefix + "app"
	LabelProcess     = LabelPrefix + "process"
	LabelDeployment  = LabelPrefix + "deployment"
	LabelDataService = LabelPrefix + "dataservice"
	LabelKind        = LabelPrefix + "kind"
	LabelRestoreTest = LabelPrefix + "restoretest"
)

// LegacyLabel is a label key as an agent before the rename wrote it ("hosbah.app" → "cloudy.app").
func LegacyLabel(key string) string {
	if k, ok := strings.CutPrefix(key, LabelPrefix); ok {
		return LegacyLabelPrefix + k
	}
	return key
}

// LegacyName is a derived name as it was before the rename ("hosbah-vol-x" → "cloudy-vol-x"). A
// name that is not Hosbah's comes back unchanged.
func LegacyName(name string) string {
	if n, ok := strings.CutPrefix(name, NamePrefix); ok {
		return LegacyNamePrefix + n
	}
	return name
}

// CurrentName is a derived name as it is emitted now ("cloudy-vol-x" → "hosbah-vol-x").
func CurrentName(name string) string {
	if n, ok := strings.CutPrefix(name, LegacyNamePrefix); ok {
		return NamePrefix + n
	}
	return name
}

// NameCandidates: a derived name in either form, the old one first — an object that already exists
// under its old name keeps it.
func NameCandidates(name string) []string {
	cur := CurrentName(name)
	if old := LegacyName(cur); old != cur {
		return []string{old, cur}
	}
	return []string{cur}
}

// hasFamilyPrefix reports whether name starts with prefix in its current or its pre-rename form
// (prefix is given in the current form, e.g. VolumeNamePrefix).
func hasFamilyPrefix(name, prefix string) (rest string, ok bool) {
	if r, ok := strings.CutPrefix(name, prefix); ok {
		return r, true
	}
	return strings.CutPrefix(name, LegacyName(prefix))
}

// sameDerived reports whether name is want (a derived name) in either form.
func sameDerived(name, want string) bool {
	return name == want || name == LegacyName(want)
}

// InWorkDir reports whether path is under the agent's work directory, new or old.
func InWorkDir(path string) bool {
	return strings.HasPrefix(path, WorkDir+"/") || strings.HasPrefix(path, LegacyWorkDir+"/")
}
