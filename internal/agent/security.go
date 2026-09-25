package agent

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// passwd.set and deploykey.generate (MH-578 C9 / C11).

// passwdSet resets one unix user's password with chpasswd, fed on stdin so the plaintext never
// appears in argv (visible to any other local user via /proc or ps) or in a shell history. The box
// is opened with this agent's own key and never logged or written to disk.
func passwdSet(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.PasswdSetParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	pt, err := agentproto.OpenBox(oc.priv, agentproto.PasswordContext(oc.ServerID), p.PasswordBox)
	if err != nil {
		return nil, 1, errors.New("the password could not be opened with this server's key")
	}
	defer zero(pt)
	if len(pt) < 12 {
		return nil, 2, errors.New("password is too short")
	}
	cmd := commandContext(oc, "chpasswd")
	cmd.Stdin = bytes.NewReader(append(append([]byte(p.User+":"), pt...), '\n'))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, 1, fmt.Errorf("chpasswd: %w: %s", err, stderr.String())
	}
	oc.Info("root password reset for %s", p.User)
	return agentproto.PasswdSetResult{Changed: true}, 0, nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// deployKeyDir is where a server's outbound git deploy key lives — never inside a team-writable
// app directory, and outside any single unix user's home so it survives that user being removed.
const deployKeyDir = "/etc/hosbah/deploy-key"

var deployKeyDirOverride string // tests only

func deployKeyPaths() (priv, pub, knownHosts string) {
	dir := deployKeyDir
	if deployKeyDirOverride != "" {
		dir = deployKeyDirOverride
	}
	return filepath.Join(dir, "id_ed25519"), filepath.Join(dir, "id_ed25519.pub"), filepath.Join(dir, "known_hosts")
}

// githubKnownHostsLine pins github.com's published ed25519 host key (see
// https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/githubs-ssh-key-fingerprints),
// so a first connection never prompts and never trust-on-first-use's a possibly-spoofed host.
const githubKnownHostsLine = "github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"

// deployKeyGenerate creates (or, idempotently, returns) this server's ed25519 deploy key. The
// private half is written 0600, root-owned, under deployKeyDir and never leaves the server; only
// the public half and its fingerprint go back to the control plane.
func deployKeyGenerate(oc *OpContext) (any, int, error) {
	dir := deployKeyDir
	if deployKeyDirOverride != "" {
		dir = deployKeyDirOverride
	}
	privPath, pubPath, knownHostsPath := deployKeyPaths()
	if err := os.MkdirAll(dir, 0o700); err != nil { //#nosec G301 -- deploy key material is always 700
		return nil, 1, err
	}
	if existing, err := os.ReadFile(pubPath); err == nil { //#nosec G304 -- fixed path under deployKeyDir
		line := string(bytes.TrimSpace(existing))
		if _, _, ferr := agentproto.ParseAuthorizedKey(line); ferr == nil {
			ensureKnownHosts(knownHostsPath)
			return agentproto.DeployKeyGenerateResult{PublicKey: line, Fingerprint: agentproto.Fingerprint(line), Changed: false}, 0, nil
		}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, 1, err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, 1, err
	}
	line := string(bytes.TrimSpace(ssh.MarshalAuthorizedKey(sshPub))) + " hosbah-deploy-key"
	block, err := ssh.MarshalPrivateKey(priv, "hosbah deploy key")
	if err != nil {
		return nil, 1, err
	}
	if err := os.WriteFile(privPath, pem.EncodeToMemory(block), 0o600); err != nil { //#nosec G306 -- private key, 600
		return nil, 1, err
	}
	if err := os.WriteFile(pubPath, []byte(line+"\n"), 0o644); err != nil { //#nosec G306 -- public key, world-readable is fine
		return nil, 1, err
	}
	ensureKnownHosts(knownHostsPath)
	oc.Info("generated a deploy key (%s)", agentproto.Fingerprint(line))
	return agentproto.DeployKeyGenerateResult{PublicKey: line, Fingerprint: agentproto.Fingerprint(line), Changed: true}, 0, nil
}

// ensureKnownHosts writes github.com's pinned host key once, idempotently. Best-effort: a failure
// here does not fail key generation — cloning can still work with StrictHostKeyChecking handled by
// the deploy pipeline itself, and the pinned line is written on the very next call anyway.
func ensureKnownHosts(path string) {
	raw, _ := os.ReadFile(path) //#nosec G304 -- fixed path under deployKeyDir
	if bytes.Contains(raw, []byte(githubKnownHostsLine)) {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //#nosec G302,G304 -- known_hosts is always world-readable
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, githubKnownHostsLine)
}
