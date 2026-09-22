// Package provisioning connects the provider drivers (internal/providers) to Hosbah's data: it
// opens sealed credentials, validates them, deletes machines, and runs the control-plane
// operation provider.create — create the machine, wait for an address, deliver the agent (cloud-init
// user-data when the driver can, otherwise an SSH bootstrap with the per-server key), and wait for
// the agent to enrol. Every step is logged to the operation, so the browser shows it live.
package provisioning

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/app"
	"github.com/fadymondy/hosbah/internal/cloudflare"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/npm"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/providers"
	_ "github.com/fadymondy/hosbah/internal/providers/proxmox" // registers "proxmox"
	"github.com/fadymondy/hosbah/internal/providers/sshboot"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// SSHKeyContext / CredentialContext bind sealed values to their row and field.
func SSHKeyContext(serverID string) string { return "server-ssh:" + serverID }
func CredentialContext(id string) string   { return "credential:" + id }

// configKeys are the non-secret settings each provider accepts.
var configKeys = map[string][]string{
	"proxmox": {"api_url", "token_id", "node", "storage", "bridge", "pool", "template_vmid", "lxc_template",
		"snippets_storage", "snippets_dir", "tls_fingerprint", "insecure_tls", "lxc_features"},
	"cloudflare": {"account_id"},
	// frp: the frps the agents dial (server_addr:server_port), the remote-port range Hosbah may allocate,
	// the public IP DNS points at, and the host the edge (NPM) reaches the frps listeners on.
	"frp": {"server_addr", "server_port", "port_from", "port_to", "public_ip", "edge_host"},
	// npm: Nginx Proxy Manager as the public edge; certificate_id is the NPM certificate to use (0: none).
	"npm":       {"api_url", "email", "certificate_id"},
	"tailscale": {"tags"},
}

// CleanConfig trims values, drops empty ones and refuses unknown keys.
func CleanConfig(provider string, in map[string]string) (map[string]string, error) {
	allowed := configKeys[provider]
	if allowed == nil {
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
	out := map[string]string{}
	for k, v := range in {
		if !contains(allowed, k) {
			return nil, fmt.Errorf("unknown setting %q for %s (allowed: %s)", k, provider, strings.Join(allowed, ", "))
		}
		if v = strings.TrimSpace(v); v != "" {
			out[k] = v
		}
	}
	return out, nil
}

// Validate builds the driver from an unsaved credential and asks the provider.
func Validate(ctx context.Context, provider string, cfg map[string]string, secret string) error {
	if provider == "cloudflare" {
		return cloudflare.New(secret).Verify(ctx)
	}
	switch provider {
	case "frp":
		addr := net.JoinHostPort(cfg["server_addr"], orDefault(cfg["server_port"], "7000"))
		conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("the frp server %s is not reachable: %w", addr, err)
		}
		_ = conn.Close()
		if cfg["public_ip"] == "" || net.ParseIP(cfg["public_ip"]) == nil {
			return errors.New("frp: public_ip (the address DNS points at) is required")
		}
		return nil
	case "npm":
		return npm.New(cfg["api_url"], cfg["email"], secret).Login(ctx)
	case "tailscale":
		if !strings.HasPrefix(strings.TrimSpace(secret), "tskey-") {
			return errors.New("a Tailscale auth key starts with tskey-")
		}
		return nil
	}
	p, err := providers.New(provider, providers.Credential{Config: cfg, Secret: secret})
	if err != nil {
		return err
	}
	return p.Validate(ctx)
}

// driverFor opens a team credential (in the caller's transaction) and builds its driver.
func driverFor(ctx context.Context, keys *crypto.Keyring, q *db.Queries, teamID, credID string) (providers.Provider, error) {
	c, err := q.CredentialInTeam(ctx, db.CredentialInTeamParams{ID: credID, TeamID: teamID})
	if err != nil {
		return nil, errors.New("the server's credential no longer exists")
	}
	t, err := q.GetTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	dek, err := keys.UnwrapDEK(teamID, t.DekWrapped)
	if err != nil {
		return nil, err
	}
	secret, err := crypto.Open(dek, teamID, CredentialContext(c.ID), c.SecretSealed)
	if err != nil {
		return nil, errors.New("the credential's secret could not be opened")
	}
	cfg := map[string]string{}
	_ = json.Unmarshal([]byte(c.Config), &cfg)
	return providers.New(c.Provider, providers.Credential{Config: cfg, Secret: string(secret)})
}

// DeleteMachine destroys a provider-created server's machine.
func DeleteMachine(ctx context.Context, keys *crypto.Keyring, q *db.Queries, s db.Server) error {
	if s.CredentialID == nil || s.ProviderRef == nil {
		return nil
	}
	p, err := driverFor(ctx, keys, q, s.TeamID, *s.CredentialID)
	if err != nil {
		return err
	}
	dctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	return p.Delete(dctx, *s.ProviderRef)
}

// Timings (variables so tests can shorten them).
var (
	PollEvery   = 5 * time.Second
	AddressWait = 10 * time.Minute
	EnrolWait   = 10 * time.Minute
	SSHWait     = 5 * time.Minute
)

// Runner is the provider.create control-plane operation.
func Runner(a *app.App, svc *ops.Service) ops.ControlPlaneRunner {
	return func(ctx context.Context, op db.Operation, logf func(stream, line string)) (any, error) {
		res, err := provision(ctx, a, svc, op, logf)
		if err != nil {
			msg := err.Error()
			_ = system(ctx, a, func(q *db.Queries) error {
				return q.SetServerStatus(ctx, db.SetServerStatusParams{ID: op.ServerID, Status: "failed", Error: &msg})
			})
			svc.Broker().Publish(ops.ServerTopic(op.ServerID))
		}
		return res, err
	}
}

func system(ctx context.Context, a *app.App, fn func(q *db.Queries) error) error {
	return tenancy.Tx(ctx, a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error { return fn(q) })
}

func provision(ctx context.Context, a *app.App, svc *ops.Service, op db.Operation, logf func(stream, line string)) (map[string]string, error) {
	info := func(format string, args ...any) { logf("info", fmt.Sprintf(format, args...)) }

	var (
		srv     db.Server
		p       providers.Provider
		privPEM []byte
	)
	err := system(ctx, a, func(q *db.Queries) error {
		var err error
		if srv, err = q.ServerByID(ctx, op.ServerID); err != nil {
			return err
		}
		if srv.CredentialID == nil || srv.SshPrivateKeySealed == nil || srv.SshPublicKey == nil {
			return errors.New("the server has no credential or bootstrap key")
		}
		if p, err = driverFor(ctx, a.Keys, q, srv.TeamID, *srv.CredentialID); err != nil {
			return err
		}
		t, err := q.GetTeam(ctx, srv.TeamID)
		if err != nil {
			return err
		}
		dek, err := a.Keys.UnwrapDEK(srv.TeamID, t.DekWrapped)
		if err != nil {
			return err
		}
		privPEM, err = crypto.Open(dek, srv.TeamID, SSHKeyContext(srv.ID), *srv.SshPrivateKeySealed)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("prepare: %w", err)
	}
	var spec struct {
		Kind, Size string
		IPConfig   string `json:"ipconfig"`
		Nameserver string `json:"nameserver"`
	}
	if srv.Spec != nil {
		_ = json.Unmarshal([]byte(*srv.Spec), &spec)
	}
	if spec.Kind == "" {
		spec.Kind = srv.Kind
	}

	delivers := true
	if d, ok := p.(providers.UserDataDelivery); ok {
		delivers = d.DeliversUserData(spec.Kind)
	}
	newInstall := func() (ops.Install, error) {
		var inst ops.Install
		err := inTeam(ctx, a, srv.TeamID, func(q *db.Queries) error {
			var err error
			inst, err = svc.NewEnrollment(ctx, q, srv.TeamID, srv.ID, "system:provisioning")
			return err
		})
		return inst, err
	}

	cs := providers.CreateSpec{Name: srv.Name, Kind: spec.Kind, Size: spec.Size, SSHPublicKeys: []string{*srv.SshPublicKey}, Tags: []string{"hosbah"}}
	cs.Options = map[string]string{}
	if spec.IPConfig != "" {
		cs.Options["ipconfig0"] = spec.IPConfig
	}
	if spec.Nameserver != "" {
		cs.Options["nameserver"] = spec.Nameserver
	}
	if delivers {
		inst, err := newInstall()
		if err != nil {
			return nil, err
		}
		cs.UserData = providers.AgentUserData(inst.Command, cs.SSHPublicKeys)
		info("the agent will be installed by cloud-init user-data on first boot")
	} else {
		info("this %s cannot take cloud-init user-data here: the agent will be installed over SSH with the server's bootstrap key", spec.Kind)
	}

	info("creating a %s (%s) on %s", spec.Kind, orDefault(spec.Size, "small"), p.Key())
	ref, err := p.Create(ctx, cs)
	if ref != "" {
		_ = system(ctx, a, func(q *db.Queries) error {
			return q.SetServerProvisioned(ctx, db.SetServerProvisionedParams{ProviderRef: &ref, ID: srv.ID})
		})
	}
	if err != nil {
		return nil, fmt.Errorf("create: %w", err)
	}
	info("created %s", ref)

	info("waiting for the machine to run and get an address")
	var m providers.Machine
	wctx, cancel := context.WithTimeout(ctx, AddressWait)
	defer cancel()
	for {
		m, err = p.Get(wctx, ref)
		if err == nil && m.Status == providers.StatusRunning && (m.IPv4 != "" || m.PrivateIP != "") {
			break
		}
		select {
		case <-wctx.Done():
			return nil, errors.New("the machine did not report an address in time")
		case <-time.After(PollEvery):
		}
	}
	ip := m.IPv4
	if ip == "" {
		ip = m.PrivateIP
	}
	_ = system(ctx, a, func(q *db.Queries) error {
		return q.SetServerProvisioned(ctx, db.SetServerProvisionedParams{IpAddress: &ip, ID: srv.ID})
	})
	info("running at %s", ip)

	if !delivers {
		inst, err := newInstall()
		if err != nil {
			return nil, err
		}
		user, cmd := providers.GuestUser, inst.Command
		if spec.Kind == providers.KindLXC {
			user, cmd = "root", inst.CommandRoot // LXC keys go to root
		}
		info("connecting over SSH as %s to install the agent", user)
		sctx, scancel := context.WithTimeout(ctx, SSHWait)
		err = sshboot.Run(sctx, sshboot.Config{
			Addr: net.JoinHostPort(ip, "22"), User: user, PrivateKeyPEM: privPEM,
			HostKey: sshboot.HostKeyPolicy{Observed: func(fp string) { info("host key %s (trusted on first use)", fp) }},
		}, cmd, &lineLogger{log: func(l string) { logf("stdout", l) }})
		scancel()
		if err != nil {
			return nil, fmt.Errorf("bootstrap over SSH: %w", err)
		}
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
			info("the agent enrolled")
			return map[string]string{"provider_ref": ref, "ip": ip}, nil
		}
		select {
		case <-ectx.Done():
			return nil, errors.New("the agent did not enrol in time")
		case <-time.After(PollEvery):
		}
	}
}

type lineLogger struct {
	log func(string)
	buf bytes.Buffer
}

func (w *lineLogger) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			w.buf.Reset()
			w.buf.WriteString(line)
			return len(p), nil
		}
		w.log(strings.TrimRight(line, "\r\n"))
	}
}

func contains(xs []string, v string) bool {
	i := sort.SearchStrings(append([]string(nil), xs...), v)
	_ = i
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// inTeam runs fn in the server's team scope. Enrolment tokens are team rows: minting one inserts
// and reads it back (RETURNING), and RLS lets system scope insert but not read agent_enrollments.
func inTeam(ctx context.Context, a *app.App, teamID string, fn func(q *db.Queries) error) error {
	return tenancy.Tx(ctx, a.SQLDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error { return fn(q) })
}
