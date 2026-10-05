package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// SSHUserTools: shell logins Hosbah creates on a server for people to reach it over SSH (the SSH
// page). Creating and removing one change who can log in to the server, so both need confirm.
func SSHUserTools() []Tool {
	return []Tool{
		{
			Name: "ssh_users_list", Scope: scopes.Read,
			Description: "Who can reach a server over SSH through Hosbah: its Hosbah-created logins and the library keys installed there.",
			InputSchema: schema([]string{"server"}, map[string]any{"server": str("Server id or name")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				users, err := env.Q.ListServerSSHUsers(ctx, db.ListServerSSHUsersParams{ServerID: srv.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", err
				}
				keys, err := env.Q.ListServerSSHKeyInstalls(ctx, db.ListServerSSHKeyInstallsParams{ServerID: srv.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", err
				}
				var b strings.Builder
				fmt.Fprintf(&b, "SSH users on %s (%d):\n", srv.Name, len(users))
				for _, u := range users {
					fmt.Fprintf(&b, "  %-20s sudo=%-5t %s %s\n", u.Username, u.Sudo, u.Status, u.LastError)
				}
				b.WriteString("Keys:\n")
				for _, k := range keys {
					if k.Status != "removed" {
						fmt.Fprintf(&b, "  %-24s %-8s user=%-12s %s\n", k.KeyName, k.KeyType, k.UnixUser, k.Status)
					}
				}
				return b.String(), nil
			},
		},
		{
			Name: "ssh_users_create", Scope: scopes.Write,
			Description: "Create (or update) a shell login on a server, optionally an admin (sudo), with a public key and/or a " +
				"password. A user that exists and was not created by Hosbah is refused. A generated password is shown once.",
			InputSchema: schema([]string{"server", "username", "confirm"}, map[string]any{
				"server":            str("Server id or name"),
				"username":          str("The unix login: lowercase letters, digits, - and _"),
				"sudo":              map[string]any{"type": "boolean", "description": "Make the login an admin (sudo or wheel group)"},
				"public_key":        str("Optional. An OpenSSH public key line to install for this user; it also joins the team library"),
				"password":          str("Optional. At least 12 characters"),
				"generate_password": map[string]any{"type": "boolean", "description": "Generate a strong password (shown once)"},
				"confirm":           str("The username, exactly"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				user := args.Str("username")
				if err := RequireConfirm(args, user); err != nil {
					return "", err
				}
				if srv.AgentPublicKey == nil {
					return "", errors.New("that server's agent has not enrolled yet")
				}
				p := agentproto.SSHUserSetParams{User: user, Sudo: args.Bool("sudo")}
				if err := p.Validate(); err != nil {
					return "", fmt.Errorf("username: %w", err)
				}
				password := args.Str("password")
				if password == "" && args.Bool("generate_password") {
					if password, err = generateMCPPassword(); err != nil {
						return "", err
					}
				} else if password != "" && len(password) < 12 {
					return "", errors.New("password must be at least 12 characters")
				}
				publicKey := strings.TrimSpace(args.Str("public_key"))
				if publicKey == "" && password == "" {
					return "", errors.New("give the user a public_key, a password, or both")
				}
				var key db.SshKey
				if publicKey != "" {
					keyType, _, perr := agentproto.ParseAuthorizedKey(publicKey)
					if perr != nil {
						return "", fmt.Errorf("public_key: %w", perr)
					}
					fp := agentproto.Fingerprint(publicKey)
					key, err = env.Q.GetTeamSSHKeyByFingerprint(ctx, db.GetTeamSSHKeyByFingerprintParams{TeamID: env.Caller.TeamID, Fingerprint: fp})
					if err != nil {
						if !isNoRowsMCP(err) {
							return "", err
						}
						key, err = env.Q.InsertTeamSSHKey(ctx, db.InsertTeamSSHKeyParams{TeamID: env.Caller.TeamID, Name: user + "@" + srv.Name,
							KeyType: keyType, PublicKey: publicKey, Fingerprint: fp, CreatedBy: env.Caller.UserID})
						if err != nil {
							return "", fmt.Errorf("could not add the key to the library: %w", err)
						}
					}
				}
				if password != "" {
					box, serr := agentproto.SealBox(*srv.AgentPublicKey, agentproto.SSHUserPasswordContext(srv.ID, user), []byte(password))
					if serr != nil {
						return "", errors.New("could not seal the password to the server's key")
					}
					p.PasswordBox = box
				}
				var res agentproto.SSHUserSetResult
				if err := runServiceOp(ctx, env, srv.ID, agentproto.KindSSHUserSet, p, &res); err != nil {
					return "", fmt.Errorf("could not set up the SSH user: %w", err)
				}
				if _, err := env.Q.UpsertServerSSHUser(ctx, db.UpsertServerSSHUserParams{TeamID: env.Caller.TeamID, ServerID: srv.ID,
					Username: user, Sudo: p.Sudo, Status: "ready", CreatedBy: env.Caller.UserID}); err != nil {
					return "", err
				}
				msg := fmt.Sprintf("SSH user %s is ready on %s (sudo: %t).", user, srv.Name, p.Sudo)
				if key.ID != "" {
					kp := agentproto.SSHKeyInstallParams{User: user, PublicKey: key.PublicKey, Fingerprint: key.Fingerprint}
					var kr agentproto.SSHKeyInstallResult
					if err := runServiceOp(ctx, env, srv.ID, agentproto.KindSSHKeyInstall, kp, &kr); err != nil {
						return "", fmt.Errorf("%s The key could not be installed: %w", msg, err)
					}
					if _, err := env.Q.UpsertSSHKeyInstall(ctx, db.UpsertSSHKeyInstallParams{TeamID: env.Caller.TeamID, SshKeyID: key.ID,
						ServerID: srv.ID, UnixUser: user, Status: "installed"}); err != nil {
						return "", err
					}
					msg += fmt.Sprintf(" Key %q installed.", key.Name)
				}
				if password != "" {
					msg += " Password (shown once, not stored): " + password
				}
				return msg, nil
			},
		},
		{
			Name: "ssh_users_remove", Scope: scopes.Write,
			Description: "Remove a Hosbah-created login from a server, with its home directory; its sessions are ended.",
			InputSchema: schema([]string{"server", "username", "confirm"}, map[string]any{
				"server":   str("Server id or name"),
				"username": str("The login to remove"),
				"confirm":  str("The username, exactly"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				user := args.Str("username")
				if err := RequireConfirm(args, user); err != nil {
					return "", err
				}
				if _, err := env.Q.GetServerSSHUser(ctx, db.GetServerSSHUserParams{ServerID: srv.ID, TeamID: env.Caller.TeamID, Username: user}); err != nil {
					return "", fmt.Errorf("no SSH user %q on %s", user, srv.Name)
				}
				var res agentproto.SSHUserRemoveResult
				if err := runServiceOp(ctx, env, srv.ID, agentproto.KindSSHUserRemove, agentproto.SSHUserRemoveParams{User: user}, &res); err != nil {
					return "", fmt.Errorf("could not remove the SSH user: %w", err)
				}
				if _, err := env.Q.DeleteServerSSHUser(ctx, db.DeleteServerSSHUserParams{ServerID: srv.ID, TeamID: env.Caller.TeamID, Username: user}); err != nil {
					return "", err
				}
				return fmt.Sprintf("Removed SSH user %s from %s.", user, srv.Name), nil
			},
		},
	}
}
