package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// dbTarget is where an imported service's engine client should run for db.create, db.drop, db.user,
// db.query, db.insights and db.cancel_query: a Docker container directly on this server (VMID "",
// Container the name — a Hosbah-managed service, or an imported container), or a Proxmox LXC guest
// reached with `pct exec` on this node — either the engine installed natively there (Container "") or,
// if the guest itself runs it in Docker, nested as `pct exec <vmid> -- docker exec <container> …`
// (Container set). A QEMU guest never reaches here: agentproto.Validate refuses guest_type "qemu" on
// both sides before any operation is even built — there is no client to run inside one without its
// guest agent (a possible future addition).
type dbTarget struct {
	VMID      string
	Container string
}

func dbTargetOf(vmid, container string) dbTarget { return dbTarget{VMID: vmid, Container: container} }

// isNativeGuest: the engine is installed directly in the guest, not run through Docker there.
func (t dbTarget) isNativeGuest() bool { return t.VMID != "" && t.Container == "" }

// checkDBTarget refuses a pct target unless this agent is on a Proxmox node (pct exists) and the
// guest exists here — before any argv is ever built around it.
func checkDBTarget(oc *OpContext, t dbTarget) error {
	if t.VMID == "" {
		return nil
	}
	if _, err := exec.LookPath("pct"); err != nil {
		return errors.New("this server has no pct — it is not a Proxmox node, so it cannot reach a guest's database")
	}
	out, err := exec.CommandContext(oc.Ctx, "pct", "status", t.VMID).CombinedOutput() //#nosec G204 -- vmid is validated (ValidVMID)
	if err != nil {
		return fmt.Errorf("guest %s was not found on this node (%s)", t.VMID, strings.TrimSpace(firstLine(string(out))))
	}
	return nil
}

// dbTargetArgv builds the fixed argv that runs args inside t, with env (only ever meaningful for a
// docker exec — pct exec has no such flag) as -e flags on the innermost docker exec.
func dbTargetArgv(env []string, t dbTarget, args []string) (bin string, full []string) {
	switch {
	case t.VMID == "":
		full = append([]string{"exec", "-i"}, envArgs(mapEnv(env))...)
		full = append(full, t.Container)
		full = append(full, args...)
		return "docker", full
	case t.Container != "":
		inner := append([]string{"exec", "-i"}, envArgs(mapEnv(env))...)
		inner = append(inner, t.Container)
		inner = append(inner, args...)
		return "pct", append([]string{"exec", t.VMID, "--", "docker"}, inner...)
	default:
		return "pct", append([]string{"exec", t.VMID, "--"}, args...)
	}
}

// execInTarget runs args against t, feeding stdin and env like execIn, and returns the combined
// output (stdout+stderr merged), like execIn's CombinedOutput.
func execInTarget(oc *OpContext, env []string, stdin string, t dbTarget, args ...string) (string, error) {
	if err := checkDBTarget(oc, t); err != nil {
		return "", err
	}
	bin, full := dbTargetArgv(env, t, args)
	cmd := exec.CommandContext(oc.Ctx, bin, full...) //#nosec G204 -- fixed argv; names/ids are validated identifiers
	if bin == "docker" {
		cmd.Env = append(dockerBaseEnv(), env...)
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// execInTargetTimeout is execInTarget with stdout and stderr kept apart and a hard deadline of its
// own — execInTimeout's target-aware twin, used by the table browser/SQL console and insights.
func execInTargetTimeout(oc *OpContext, env []string, stdin string, timeout time.Duration, t dbTarget, args ...string) (stdout, stderr string, err error) {
	if err := checkDBTarget(oc, t); err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(oc.Ctx, timeout)
	defer cancel()
	bin, full := dbTargetArgv(env, t, args)
	cmd := exec.CommandContext(ctx, bin, full...) //#nosec G204 -- fixed argv; names/ids are validated identifiers
	if bin == "docker" {
		cmd.Env = append(dockerBaseEnv(), env...)
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return out.String(), "the statement timed out", ctx.Err()
	}
	return out.String(), errb.String(), err
}

// clientLogin returns the login flags and the env carrying the password for one client invocation
// against t. A container (or an engine that itself runs in Docker inside a guest) uses the credential
// db.probe already checked, its password on env (never an argv) — exactly as for a Hosbah-managed
// service. A native guest engine ignores the stored credential and connects as the matching OS user
// instead: Postgres by peer auth, MySQL by its default auth_socket as root (pct exec already runs as
// root on the node, and inside the guest by default) — pct exec has no flag to carry an env var, so a
// native command must need none.
func clientLogin(kind string, t dbTarget, root map[string]string, database string) (login, env []string) {
	if t.isNativeGuest() {
		switch kind {
		case "postgres":
			if database == "" {
				database = "postgres"
			}
			return []string{"-U", "postgres", "-d", database}, nil
		case "mysql":
			return []string{"-u", "root"}, nil
		}
		return nil, nil
	}
	switch kind {
	case "postgres":
		return pgLogin(root, database), []string{"PGPASSWORD=" + root["password"]}
	case "mysql":
		return mysqlLogin(root), []string{"MYSQL_PWD=" + root["password"]}
	}
	return nil, nil
}

// clientArgv wraps a client's own argv (bin plus its flags) for t. A native guest Postgres must run
// the client process itself as the postgres OS user for peer auth to apply (it checks the process's
// real uid, not just -U), so it is prefixed with runuser.
func clientArgv(kind string, t dbTarget, args []string) []string {
	if t.isNativeGuest() && kind == "postgres" {
		return append([]string{"runuser", "-u", "postgres", "--"}, args...)
	}
	return args
}

// dbTargetRunning reports whether the service is up: a container is checked with docker inspect
// (containerRunning); a native guest with `pct status` (already done by checkDBTarget, so here it is
// just "does pct exec succeed"); an engine nested in a guest's Docker is checked with docker inspect
// run through pct exec.
func dbTargetRunning(oc *OpContext, t dbTarget) bool {
	if t.VMID == "" {
		return containerRunning(oc, t.Container)
	}
	if checkDBTarget(oc, t) != nil {
		return false
	}
	if t.Container == "" {
		return true // pct status already confirmed the guest is up; the engine's own client call will fail clearly if it is not
	}
	out, err := exec.CommandContext(oc.Ctx, "pct", "exec", t.VMID, "--", "docker", "inspect", "-f", "{{.State.Running}}", t.Container).CombinedOutput() //#nosec G204 -- vmid and container are validated identifiers
	return err == nil && strings.TrimSpace(string(out)) == "true"
}
