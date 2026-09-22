package provisioning

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/app"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/providers/sshboot"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Connecting a server the user already has: they give an address and a login (root or a sudo user,
// with a password or a private key); Hosbah logs in once, looks at what the host is (plain Linux,
// Docker, Proxmox VE), installs its own per-server key and the agent, and waits for the agent to
// enrol. The agent's facts then carry the platform and the services already running (import).
//
// The user's password or key is sealed with the team key only for the life of the operation, and
// forgotten when it ends, whatever the outcome. From then on Hosbah uses its own key.

// ConnectSpec is servers.spec.connect.
type ConnectSpec struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
	Auth string `json:"auth"` // password | key
	// RegisterProxmox: when the host turns out to be a Proxmox VE node, also create an API token
	// on it and save it as a team credential, so Hosbah can create VMs/LXCs there.
	RegisterProxmox bool   `json:"register_proxmox"`
	SecretSealed    string `json:"secret_sealed,omitempty"`
}

// ConnectSecretContext binds the sealed login to its server.
func ConnectSecretContext(serverID string) string { return "server-connect:" + serverID }

// ConnectTimeout bounds the login itself (a wrong password fails fast; this is for unreachable hosts).
var ConnectTimeout = 2 * time.Minute

// ConnectRunner is the server.connect control-plane operation.
func ConnectRunner(a *app.App, svc *ops.Service) ops.ControlPlaneRunner {
	return func(ctx context.Context, op db.Operation, logf func(stream, line string)) (any, error) {
		res, err := connect(ctx, a, svc, op, logf)
		// Forget the login even if the operation was cancelled or timed out.
		fctx := context.WithoutCancel(ctx)
		_ = system(fctx, a, func(q *db.Queries) error { return q.ForgetServerConnectSecret(fctx, op.ServerID) })
		if err != nil {
			msg := err.Error()
			_ = system(fctx, a, func(q *db.Queries) error {
				return q.SetServerStatus(fctx, db.SetServerStatusParams{ID: op.ServerID, Status: "failed", Error: &msg})
			})
		}
		svc.Broker().Publish(ops.ServerTopic(op.ServerID))
		return res, err
	}
}

// probe is what one shell round-trip learns about the host.
type probe struct {
	uid              int
	osID, osVersion  string
	docker, proxmox  bool
	pveVersion, node string
	sudo             bool
}

const probeScript = `. /etc/os-release 2>/dev/null; echo "os=$ID"; echo "ver=$VERSION_ID"; echo "uid=$(id -u)"
command -v docker >/dev/null 2>&1 && echo docker=1
command -v pveversion >/dev/null 2>&1 && echo "pve=$(pveversion | cut -d/ -f2)" && echo "node=$(hostname)"
sudo -n true >/dev/null 2>&1 && echo sudo=1; true`

func parseProbe(out string) probe {
	var p probe
	p.uid = -1
	for _, l := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(l), "=")
		switch k {
		case "os":
			p.osID = v
		case "ver":
			p.osVersion = v
		case "uid":
			if n, err := strconv.Atoi(v); err == nil {
				p.uid = n
			}
		case "docker":
			p.docker = true
		case "pve":
			p.proxmox, p.pveVersion = true, v
		case "node":
			p.node = v
		case "sudo":
			p.sudo = true
		}
	}
	return p
}

func connect(ctx context.Context, a *app.App, svc *ops.Service, op db.Operation, logf func(stream, line string)) (map[string]any, error) {
	info := func(format string, args ...any) { logf("info", fmt.Sprintf(format, args...)) }

	var (
		srv    db.Server
		spec   struct{ Connect ConnectSpec }
		secret []byte
		dek    []byte
	)
	err := system(ctx, a, func(q *db.Queries) error {
		var err error
		if srv, err = q.ServerByID(ctx, op.ServerID); err != nil {
			return err
		}
		if srv.Spec == nil || json.Unmarshal([]byte(*srv.Spec), &spec) != nil || spec.Connect.Host == "" {
			return errors.New("the server has no connection details")
		}
		if spec.Connect.SecretSealed == "" {
			return errors.New("the login was already used and forgotten — connect the server again")
		}
		t, err := q.GetTeam(ctx, srv.TeamID)
		if err != nil {
			return err
		}
		if dek, err = a.Keys.UnwrapDEK(srv.TeamID, t.DekWrapped); err != nil {
			return err
		}
		secret, err = crypto.Open(dek, srv.TeamID, ConnectSecretContext(srv.ID), spec.Connect.SecretSealed)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("prepare: %w", err)
	}
	c := spec.Connect
	if c.Port == 0 {
		c.Port = 22
	}
	cfg := sshboot.Config{
		Addr: net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), User: c.User, FailFastAuth: true,
		HostKey: sshboot.HostKeyPolicy{Observed: func(fp string) { info("host key %s (trusted on first use)", fp) }},
	}
	if c.Auth == "key" {
		cfg.PrivateKeyPEM = secret
	} else {
		cfg.Password = string(secret)
	}

	info("connecting to %s as %s (%s)", cfg.Addr, c.User, c.Auth)
	cctx, cancel := context.WithTimeout(ctx, ConnectTimeout)
	client, err := sshboot.Connect(cctx, cfg)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer client.Close()

	out, err := client.Output(ctx, probeScript)
	if err != nil {
		return nil, fmt.Errorf("probe: %w", err)
	}
	p := parseProbe(out)
	platform := "linux"
	switch {
	case p.proxmox:
		platform = "proxmox"
	case p.docker:
		platform = "docker"
	}
	info("found %s %s — platform: %s", orDefault(p.osID, "unknown OS"), p.osVersion, platform)
	if p.proxmox {
		info("Proxmox VE %s on node %s", p.pveVersion, p.node)
	}
	sudo := ""
	switch {
	case p.uid == 0:
	case p.sudo:
		sudo = "sudo -n "
	default:
		return nil, fmt.Errorf("%s is not root and cannot sudo without a password — use root, or allow passwordless sudo for this user", c.User)
	}

	// Hosbah's own key: installed for root, so the user's login is never needed again.
	pub, privPEM, err := sshboot.GenerateKey()
	if err != nil {
		return nil, err
	}
	sealedKey, err := crypto.Seal(dek, srv.TeamID, SSHKeyContext(srv.ID), privPEM)
	if err != nil {
		return nil, err
	}
	addKey := fmt.Sprintf(`%ssh -c 'umask 077; mkdir -p /root/.ssh && touch /root/.ssh/authorized_keys && grep -qxF "%s" /root/.ssh/authorized_keys || echo "%s" >> /root/.ssh/authorized_keys'`, sudo, pub, pub)
	if err := client.Stream(ctx, addKey, &lineLogger{log: func(l string) { logf("stdout", l) }}); err != nil {
		return nil, fmt.Errorf("install Hosbah's key: %w", err)
	}
	if err := system(ctx, a, func(q *db.Queries) error {
		return q.SetServerSSHKey(ctx, db.SetServerSSHKeyParams{ID: srv.ID, TeamID: srv.TeamID, SshPublicKey: &pub, SshPrivateKeySealed: &sealedKey})
	}); err != nil {
		return nil, fmt.Errorf("store Hosbah's key: %w", err)
	}
	info("installed Hosbah's own SSH key for root")

	result := map[string]any{"platform": platform, "os": p.osID, "os_version": p.osVersion}
	if p.proxmox && c.RegisterProxmox {
		credID, err := registerProxmox(ctx, a, client, srv, op, c.Host, p, sudo, dek, info)
		if err != nil {
			// The server is still worth connecting; the credential can be added by hand.
			logf("stderr", "could not register this node as a Proxmox provider: "+err.Error())
		} else {
			result["credential_id"] = credID
		}
	}

	var inst ops.Install
	if err := inTeam(ctx, a, srv.TeamID, func(q *db.Queries) error {
		var err error
		inst, err = svc.NewEnrollment(ctx, q, srv.TeamID, srv.ID, "system:connect")
		return err
	}); err != nil {
		return nil, err
	}
	cmd := inst.CommandRoot
	if sudo != "" {
		cmd = "curl -fsSL " + inst.URL + " | sudo -n sh"
	}
	info("installing the agent")
	if err := client.Stream(ctx, cmd, &lineLogger{log: func(l string) { logf("stdout", l) }}); err != nil {
		return nil, fmt.Errorf("install the agent: %w", err)
	}

	info("waiting for the agent to enrol")
	ectx, ecancel := context.WithTimeout(ctx, EnrolWait)
	defer ecancel()
	for {
		var status string
		_ = system(ectx, a, func(q *db.Queries) error {
			s, err := q.ServerByID(ectx, srv.ID)
			status = s.Status
			return err
		})
		if status == "enrolled" {
			info("the agent enrolled — services on the host are listed on the server page")
			return result, nil
		}
		select {
		case <-ectx.Done():
			return nil, errors.New("the agent did not enrol in time")
		case <-time.After(PollEvery):
		}
	}
}

// registerProxmox creates hosbah@pve with a privilege-separated-off API token and saves it as a
// team credential. The token secret goes straight from the node's output into a sealed column.
func registerProxmox(ctx context.Context, a *app.App, client *sshboot.Client, srv db.Server, op db.Operation,
	host string, p probe, sudo string, dek []byte, info func(string, ...any)) (string, error) {
	tokenName := "hosbah-" + srv.ID[:8]
	script := sudo + `sh -c 'pveum user add hosbah@pve --comment Hosbah >/dev/null 2>&1; ` +
		`pveum acl modify / --users hosbah@pve --roles PVEAdmin >/dev/null && ` +
		`pveum user token remove hosbah@pve ` + tokenName + ` >/dev/null 2>&1; ` +
		`pveum user token add hosbah@pve ` + tokenName + ` --privsep 0 --output-format json && echo && ` +
		`openssl x509 -in /etc/pve/local/pve-ssl.pem -noout -fingerprint -sha256 && ` +
		`pvesm status --content images 2>/dev/null | awk "NR>1 {print \"storage=\" \$1}"'`
	out, err := client.Output(ctx, script)
	if err != nil {
		return "", err
	}
	var tok struct {
		Value string `json:"value"`
	}
	cfg := map[string]string{
		"api_url":  "https://" + net.JoinHostPort(host, "8006"),
		"token_id": "hosbah@pve!" + tokenName,
		"node":     p.node,
		"bridge":   "vmbr0",
	}
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "{"):
			_ = json.Unmarshal([]byte(l), &tok)
		case strings.Contains(l, "Fingerprint="):
			_, fp, _ := strings.Cut(l, "Fingerprint=")
			cfg["tls_fingerprint"] = fp
		case strings.HasPrefix(l, "storage=") && (cfg["storage"] == "" || strings.HasSuffix(l, "=local-lvm")):
			cfg["storage"] = strings.TrimPrefix(l, "storage=")
		}
	}
	if tok.Value == "" {
		return "", errors.New("the node did not return a token")
	}
	if cfg["storage"] == "" {
		cfg["storage"] = "local"
	}
	vctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	err = Validate(vctx, "proxmox", cfg, tok.Value)
	cancel()
	if err != nil {
		return "", fmt.Errorf("the new token did not validate: %w", err)
	}
	credID := newID()
	sealed, err := crypto.Seal(dek, srv.TeamID, CredentialContext(credID), []byte(tok.Value))
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(cfg)
	now := time.Now().UTC()
	// The operation's creator (an owner or admin, who may manage servers) owns the credential.
	err = tenancy.Tx(ctx, a.SQLDB, tenancy.Scope{UserID: op.CreatedBy, TeamID: srv.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		if _, err := q.InsertCredential(ctx, db.InsertCredentialParams{ID: credID, TeamID: srv.TeamID, Provider: "proxmox",
			Name: "proxmox-" + orDefault(p.node, srv.Name), Config: string(raw), SecretSealed: sealed, CreatedBy: op.CreatedBy, LastValidatedAt: &now}); err != nil {
			return err
		}
		return tenancy.Record(ctx, q, srv.TeamID, op.CreatedBy, "credential.created", "credential", credID,
			map[string]any{"name": "proxmox-" + p.node, "provider": "proxmox", "validated": true, "via": "server.connect"})
	})
	if err != nil {
		return "", err
	}
	info("registered node %s as a Proxmox provider (token %s) — new VMs and LXCs can be created there", p.node, cfg["token_id"])
	return credID, nil
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// NewID is a random UUIDv4 (row ids created outside the database default).
func NewID() string { return newID() }
