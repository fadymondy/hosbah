package agent

import (
	"os"
	"os/exec"
)

// commandContext builds a command for oc's deadline with the non-interactive environment.
func commandContext(oc *OpContext, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(oc.Ctx, name, args...) //#nosec G204 -- fixed argv from the typed handlers
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "LC_ALL=C")
	return cmd
}
