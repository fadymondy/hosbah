package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/platform"
	"github.com/fadymondy/hosbah/internal/providers/sshboot"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Adding a server is one function shared by the REST API and the MCP tools, so both offer the same
// three ways in: an install command (byo), SSH connect (byo + Connect), or a new Proxmox guest.

// AddInput says how to add a server.
type AddInput struct {
	Name         string
	Provider     string // byo | proxmox
	CredentialID string // proxmox
	Kind         string // proxmox: vm | lxc
	Size         string // proxmox: small | medium | large
	// IPConfig, proxmox: a static address for networks without DHCP, e.g. "ip=10.10.10.121/24,gw=10.10.10.1"
	// (qemu ipconfig0 syntax; the same keys set an LXC's net0). Empty: DHCP.
	IPConfig string
	// Nameserver, proxmox LXC: the resolver for a static address (e.g. 1.1.1.1). Empty: the node's.
	Nameserver string
	Connect    *ConnectInput
}

// ConnectInput is how to log in to a server the user already has.
type ConnectInput struct {
	Host            string
	Port            int
	User            string
	Auth            string // password | key
	Password        string
	PrivateKey      string
	RegisterProxmox *bool // default true
}

// UserError is a refusal the caller should show as is. Conflict distinguishes 409 from 422.
type UserError struct {
	Msg      string
	Conflict bool
}

func (e *UserError) Error() string { return e.Msg }

func refuse(msg string) error { return &UserError{Msg: msg} }

// AddServer inserts the server in the caller's team transaction and queues what brings it up. For
// a plain byo server it returns the one-time install command.
func AddServer(ctx context.Context, svc *ops.Service, q *db.Queries, teamID, actorID string, in AddInput) (db.Server, *ops.Install, error) {
	// The platform's max_servers_per_team (a *platform.Error, 403) — checked first, for every way in.
	if err := platform.CheckServerLimit(ctx, q, teamID); err != nil {
		return db.Server{}, nil, err
	}
	if in.Connect != nil && in.Provider != "byo" {
		return db.Server{}, nil, refuse("connect over SSH is for a server you already have (provider byo)")
	}
	serverID := newID()
	kind, status := "byo", "pending"
	var credID, spec *string
	switch {
	case in.Provider == "proxmox":
		cred, err := q.CredentialInTeam(ctx, db.CredentialInTeamParams{ID: in.CredentialID, TeamID: teamID})
		if err != nil || cred.Provider != "proxmox" {
			return db.Server{}, nil, refuse("choose one of this team's Proxmox credentials")
		}
		if in.Kind != "vm" && in.Kind != "lxc" {
			return db.Server{}, nil, refuse("choose a VM or an LXC")
		}
		size := in.Size
		if size == "" {
			size = "small"
		}
		settings := map[string]string{"kind": in.Kind, "size": size}
		if ip := strings.TrimSpace(in.IPConfig); ip != "" {
			if strings.ContainsAny(ip, " \t\n") || !strings.Contains(ip, "ip=") {
				return db.Server{}, nil, refuse("ipconfig looks like ip=10.10.10.121/24,gw=10.10.10.1 (or leave it empty for DHCP)")
			}
			settings["ipconfig"] = ip
		}
		if ns := strings.TrimSpace(in.Nameserver); ns != "" {
			if net.ParseIP(ns) == nil {
				return db.Server{}, nil, refuse("nameserver must be an IP address, e.g. 1.1.1.1")
			}
			settings["nameserver"] = ns
		}
		raw, _ := json.Marshal(settings)
		s := string(raw)
		kind, status, credID, spec = in.Kind, "provisioning", &cred.ID, &s
	case in.Provider != "byo":
		return db.Server{}, nil, refuse("provider must be byo or proxmox")
	case in.Connect != nil:
		cs, err := sealConnect(ctx, svc, q, teamID, serverID, in.Connect)
		if err != nil {
			return db.Server{}, nil, err
		}
		status, spec = "provisioning", &cs
	}
	row, err := q.InsertServer(ctx, db.InsertServerParams{ID: serverID, TeamID: teamID, Name: in.Name, Provider: in.Provider,
		Kind: kind, Status: status, CredentialID: credID, Spec: spec, CreatedBy: actorID})
	if err != nil {
		if strings.Contains(err.Error(), "servers_team_name_key") {
			return db.Server{}, nil, &UserError{Msg: "a server with that name already exists in this team", Conflict: true}
		}
		return db.Server{}, nil, fmt.Errorf("could not add the server: %w", err)
	}

	var install *ops.Install
	enqueue := func(k string) error {
		_, _, err := svc.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: serverID, Kind: k,
			Params: json.RawMessage(`{}`), CreatedBy: actorID, Executor: ops.ExecutorControlPlane})
		return err
	}
	switch {
	case in.Connect != nil:
		if err := enqueue(ops.KindServerConnect); err != nil {
			return db.Server{}, nil, errors.New("could not queue the connection")
		}
	case in.Provider == "byo":
		inst, err := svc.NewEnrollment(ctx, q, teamID, serverID, actorID)
		if err != nil {
			return db.Server{}, nil, errors.New("could not create the install command")
		}
		install = &inst
	default:
		// The per-server bootstrap key: its public half goes on the guest (cloud-init sshkeys /
		// LXC ssh-public-keys); the private half is sealed with the team key.
		pub, privPEM, err := sshboot.GenerateKey()
		if err != nil {
			return db.Server{}, nil, errors.New("could not create the bootstrap key")
		}
		dek, err := svc.TeamDEK(ctx, q, teamID)
		if err != nil {
			return db.Server{}, nil, errors.New("team key unavailable")
		}
		sealed, err := crypto.Seal(dek, teamID, SSHKeyContext(serverID), privPEM)
		if err != nil {
			return db.Server{}, nil, errors.New("could not seal the bootstrap key")
		}
		if err := q.SetServerSSHKey(ctx, db.SetServerSSHKeyParams{ID: serverID, TeamID: teamID, SshPublicKey: &pub, SshPrivateKeySealed: &sealed}); err != nil {
			return db.Server{}, nil, errors.New("could not store the bootstrap key")
		}
		if err := enqueue(ops.KindProviderCreate); err != nil {
			return db.Server{}, nil, errors.New("could not queue provisioning")
		}
	}
	via := in.Provider
	if in.Connect != nil {
		via = "ssh"
	}
	if err := tenancy.Record(ctx, q, teamID, actorID, "server.created", "server", serverID,
		map[string]any{"name": in.Name, "provider": in.Provider, "kind": kind, "via": via}); err != nil {
		return db.Server{}, nil, errors.New("audit failed")
	}
	return row, install, nil
}

// sealConnect validates the login and seals it with the team key for the connect operation only.
func sealConnect(ctx context.Context, svc *ops.Service, q *db.Queries, teamID, serverID string, c *ConnectInput) (string, error) {
	host := strings.TrimSpace(c.Host)
	if host == "" || strings.ContainsAny(host, " /@'\"`$;&|\\") {
		return "", refuse("the host must be an IP address or a hostname")
	}
	if c.Port < 0 || c.Port > 65535 {
		return "", refuse("the SSH port must be 1–65535")
	}
	user := strings.TrimSpace(c.User)
	if user == "" || strings.ContainsAny(user, " /@'\"`$;&|\\") {
		return "", refuse("give the user to log in as (root, or a user with passwordless sudo)")
	}
	var secret string
	switch c.Auth {
	case "password":
		secret = c.Password
	case "key":
		secret = strings.TrimSpace(c.PrivateKey) + "\n"
		if _, err := ssh.ParsePrivateKey([]byte(secret)); err != nil {
			return "", refuse("the private key could not be read (passphrase-protected keys are not supported)")
		}
	default:
		return "", refuse("auth must be password or key")
	}
	if strings.TrimSpace(secret) == "" {
		return "", refuse("give the password or the private key")
	}
	dek, err := svc.TeamDEK(ctx, q, teamID)
	if err != nil {
		return "", errors.New("team key unavailable")
	}
	sealed, err := crypto.Seal(dek, teamID, ConnectSecretContext(serverID), []byte(secret))
	if err != nil {
		return "", errors.New("could not seal the login")
	}
	register := c.RegisterProxmox == nil || *c.RegisterProxmox
	raw, _ := json.Marshal(map[string]ConnectSpec{"connect": {Host: host, Port: c.Port, User: user,
		Auth: c.Auth, RegisterProxmox: register, SecretSealed: sealed}})
	return string(raw), nil
}
