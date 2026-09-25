package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// SSHKeyTools are MH-578 C10: the team's SSH key library and installing/removing a key on a
// server's chosen unix user. Adding a key is write; installing or removing it on a server, and
// deleting it from the library, are destructive (they change a server's authorized_keys, or drop
// something other tools track installs against) and need confirm.
func SSHKeyTools() []Tool {
	return []Tool{
		{
			Name: "ssh_keys_list", Scope: scopes.Read,
			Description: "The team's SSH key library.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				rows, err := env.Q.ListTeamSSHKeys(ctx, env.Caller.TeamID)
				if err != nil {
					return "", err
				}
				if len(rows) == 0 {
					return "No SSH keys in the library yet.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d key(s):\n", len(rows))
				for _, r := range rows {
					fmt.Fprintf(&b, "  %-24s %-8s %s\n", r.Name, r.KeyType, r.Fingerprint)
				}
				return b.String(), nil
			},
		},
		{
			Name: "ssh_keys_add", Scope: scopes.Write,
			Description: "Add a public key to the team's library. The key type and fingerprint are derived from public_key.",
			InputSchema: schema([]string{"name", "public_key"}, map[string]any{
				"name":       str("A name for this key, unique in the team"),
				"public_key": str("One OpenSSH authorized_keys line: \"ssh-ed25519 AAAA... [comment]\" (ed25519, rsa or ecdsa)"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				publicKey := args.Str("public_key")
				keyType, _, err := agentproto.ParseAuthorizedKey(publicKey)
				if err != nil {
					return "", fmt.Errorf("public_key: %w", err)
				}
				fp := agentproto.Fingerprint(publicKey)
				row, err := env.Q.InsertTeamSSHKey(ctx, db.InsertTeamSSHKeyParams{
					TeamID: env.Caller.TeamID, Name: args.Str("name"), KeyType: keyType, PublicKey: publicKey,
					Fingerprint: fp, CreatedBy: env.Caller.UserID,
				})
				if err != nil {
					return "", fmt.Errorf("could not add the key (the name or the key may already be in the library): %w", err)
				}
				return fmt.Sprintf("Added %q (%s, %s).", row.Name, row.KeyType, row.Fingerprint), nil
			},
		},
		{
			Name: "ssh_keys_remove", Scope: scopes.Destructive, Destructive: true, ResourceType: "ssh_key",
			Description: "Remove a key from the library. This does not touch any server it was already installed to. confirm must equal the key's name.",
			InputSchema: schema([]string{"key", "confirm"}, map[string]any{
				"key":     str("Key name or id"),
				"confirm": str("The key's name, exactly"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				key, err := findSSHKey(ctx, env, args.Str("key"))
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, key.Name); err != nil {
					return "", err
				}
				n, err := env.Q.DeleteTeamSSHKey(ctx, db.DeleteTeamSSHKeyParams{ID: key.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", err
				}
				if n == 0 {
					return "", errors.New("SSH key not found")
				}
				return fmt.Sprintf("Removed %q.", key.Name), nil
			},
		},
		{
			Name: "ssh_keys_install", Scope: scopes.Destructive, Destructive: true, ResourceType: "ssh_key",
			Description: "Install a team key onto one server's chosen unix user, idempotently (marker-comment based — never touches a " +
				"line it did not add itself, including Hosbah's own enrollment key). confirm must equal the server's id or name.",
			InputSchema: schema([]string{"key", "server", "user", "confirm"}, map[string]any{
				"key":     str("Key name or id"),
				"server":  str("Server id or name"),
				"user":    str("The unix login to edit ~/.ssh/authorized_keys of"),
				"confirm": str("The server id or name, exactly"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) { return sshKeyInstallRun(ctx, env, args, false) },
		},
		{
			Name: "ssh_keys_uninstall", Scope: scopes.Destructive, Destructive: true, ResourceType: "ssh_key",
			Description: "Remove a team key from one server's chosen unix user, if it was installed there by Hosbah (the same marker " +
				"comment). confirm must equal the server's id or name.",
			InputSchema: schema([]string{"key", "server", "user", "confirm"}, map[string]any{
				"key":     str("Key name or id"),
				"server":  str("Server id or name"),
				"user":    str("The unix login to edit ~/.ssh/authorized_keys of"),
				"confirm": str("The server id or name, exactly"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) { return sshKeyInstallRun(ctx, env, args, true) },
		},
	}
}

func sshKeyInstallRun(ctx context.Context, env *Env, args Args, uninstall bool) (string, error) {
	if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
		return "", err
	}
	key, err := findSSHKey(ctx, env, args.Str("key"))
	if err != nil {
		return "", err
	}
	srv, err := findServer(ctx, env, args)
	if err != nil {
		return "", err
	}
	if err := RequireConfirm(args, args.Str("server")); err != nil {
		return "", err
	}
	user := args.Str("user")
	verb := "install"
	status := "installed"
	var installedAt, removedAt *time.Time
	now := time.Now()
	var runErr error
	if uninstall {
		verb, status = "uninstall", "removed"
		p := agentproto.SSHKeyUninstallParams{User: user, Fingerprint: key.Fingerprint}
		if verr := p.Validate(); verr != nil {
			return "", verr
		}
		var res agentproto.SSHKeyInstallResult
		runErr = runServiceOp(ctx, env, srv.ID, agentproto.KindSSHKeyUninstall, p, &res)
		if runErr == nil {
			removedAt = &now
		}
	} else {
		p := agentproto.SSHKeyInstallParams{User: user, PublicKey: key.PublicKey, Fingerprint: key.Fingerprint}
		if verr := p.Validate(); verr != nil {
			return "", verr
		}
		var res agentproto.SSHKeyInstallResult
		runErr = runServiceOp(ctx, env, srv.ID, agentproto.KindSSHKeyInstall, p, &res)
		if runErr == nil {
			installedAt = &now
		}
	}
	opErr := ""
	if runErr != nil {
		status, opErr = "failed", runErr.Error()
	}
	if _, err := env.Q.UpsertSSHKeyInstall(ctx, db.UpsertSSHKeyInstallParams{
		TeamID: env.Caller.TeamID, SshKeyID: key.ID, ServerID: srv.ID, UnixUser: user,
		Status: status, LastError: opErr, InstalledAt: installedAt, RemovedAt: removedAt,
	}); err != nil {
		return "", err
	}
	if runErr != nil {
		return "", fmt.Errorf("could not %s the key: %w", verb, runErr)
	}
	action := "Installed"
	if uninstall {
		action = "Uninstalled"
	}
	return fmt.Sprintf("%s %q on %s for %s.", action, key.Name, srv.Name, user), nil
}

func findSSHKey(ctx context.Context, env *Env, ref string) (db.SshKey, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.SshKey{}, errors.New("key is required")
	}
	if row, err := env.Q.GetTeamSSHKey(ctx, db.GetTeamSSHKeyParams{ID: ref, TeamID: env.Caller.TeamID}); err == nil {
		return row, nil
	}
	rows, err := env.Q.ListTeamSSHKeys(ctx, env.Caller.TeamID)
	if err != nil {
		return db.SshKey{}, err
	}
	for _, r := range rows {
		if strings.EqualFold(r.Name, ref) {
			return r, nil
		}
	}
	return db.SshKey{}, errors.New("SSH key not found")
}
