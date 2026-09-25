package agent

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// sshkey.install / sshkey.uninstall (MH-578 C10): idempotently edit one unix user's
// ~/.ssh/authorized_keys. Every line this feature adds is preceded by its own marker comment
// (agentproto.SSHKeyMarker), so uninstall only ever removes the exact marker+key pair it finds —
// never a line with no marker, which is how Hosbah's own enrollment key (installed by
// internal/provisioning/connect.go with no marker at all) and anything a human put there by hand
// stay untouched.

// upsertAuthorizedKey adds marker+key to content, right after any existing occurrence is removed
// first (so a re-install with a changed comment/options still ends up as one clean copy). Returns
// the new content and whether it actually changed anything.
func upsertAuthorizedKey(content, marker, key string) (string, bool) {
	lines := akSplitLines(content)
	out, _ := removeMarkedBlock(lines, marker)
	out = append(out, marker, key)
	next := akJoinLines(out)
	return next, next != content
}

// removeAuthorizedKey removes the marker+key block for marker, if present. Returns the new content
// and whether anything changed.
func removeAuthorizedKey(content, marker string) (string, bool) {
	lines := akSplitLines(content)
	out, had := removeMarkedBlock(lines, marker)
	if !had {
		return content, false
	}
	return akJoinLines(out), true
}

// removeMarkedBlock drops marker and the single non-empty line right after it (the key line this
// feature always writes together), wherever marker occurs. Any other line — including another
// tool's key, or a comment with no marker — passes through untouched.
func removeMarkedBlock(lines []string, marker string) ([]string, bool) {
	out := make([]string, 0, len(lines))
	found := false
	for i := 0; i < len(lines); i++ {
		if lines[i] == marker {
			found = true
			i++ // also skip the key line right after the marker, if there is one
			continue
		}
		out = append(out, lines[i])
	}
	return out, found
}

func akSplitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func akJoinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// sshUserHomeDir resolves user's home and uid/gid; overridable in tests.
var sshUserLookup = func(name string) (home string, uid, gid int, err error) {
	u, err := user.Lookup(name)
	if err != nil {
		return "", 0, 0, err
	}
	uid, err = strconv.Atoi(u.Uid)
	if err != nil {
		return "", 0, 0, err
	}
	gid, err = strconv.Atoi(u.Gid)
	if err != nil {
		return "", 0, 0, err
	}
	return u.HomeDir, uid, gid, nil
}

func chownIfRoot(path string, uid, gid int) {
	if os.Geteuid() == 0 {
		_ = os.Chown(path, uid, gid) //#nosec G302 -- best effort; ownership matters, the error does not stop the install
	}
}

func editAuthorizedKeys(oc *OpContext, username string, edit func(content string) (string, bool)) (bool, error) {
	home, uid, gid, err := sshUserLookup(username)
	if err != nil {
		return false, fmt.Errorf("no such user %q: %w", username, err)
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil { //#nosec G301 -- .ssh is always 700
		return false, err
	}
	chownIfRoot(sshDir, uid, gid)
	path := filepath.Join(sshDir, "authorized_keys")
	raw, err := os.ReadFile(path) //#nosec G304 -- path is home dir of a validated unix user, joined with a fixed name
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if len(raw) > agentproto.MaxAuthorizedKeysBytes {
		return false, fmt.Errorf("authorized_keys is too large to edit (%d bytes)", len(raw))
	}
	next, changed := edit(string(raw))
	if !changed {
		return false, nil
	}
	tmp := path + ".hosbah-tmp"
	if err := os.WriteFile(tmp, []byte(next), 0o600); err != nil { //#nosec G306 -- authorized_keys is always 600
		return false, err
	}
	chownIfRoot(tmp, uid, gid)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	oc.Info("authorized_keys of %s updated (%s)", username, path)
	return true, nil
}

func sshkeyInstall(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.SSHKeyInstallParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	marker := agentproto.SSHKeyMarker(p.Fingerprint)
	key := strings.TrimSpace(p.PublicKey)
	changed, err := editAuthorizedKeys(oc, p.User, func(content string) (string, bool) {
		return upsertAuthorizedKey(content, marker, key)
	})
	if err != nil {
		return nil, 1, err
	}
	return agentproto.SSHKeyInstallResult{Changed: changed}, 0, nil
}

func sshkeyUninstall(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.SSHKeyUninstallParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	marker := agentproto.SSHKeyMarker(p.Fingerprint)
	changed, err := editAuthorizedKeys(oc, p.User, func(content string) (string, bool) {
		return removeAuthorizedKey(content, marker)
	})
	if err != nil {
		return nil, 1, err
	}
	return agentproto.SSHKeyInstallResult{Changed: changed}, 0, nil
}
