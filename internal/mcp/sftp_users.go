package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// SFTPUserTools: chrooted, SFTP-only system accounts on a server (OpenSSH internal-sftp, no
// shell), so a team can hand out upload/download access with any SFTP client without sharing
// root. Every tool needs AbilityManageServers (owner or admin), like the file browser and
// connecting a server; sftp_user_delete is also destructive (confirm = the user's username).
func SFTPUserTools() []Tool {
	serverArg := str("Server name or id")
	userArg := str("sftp user id or username")

	return []Tool{
		{
			Name: "sftp_users_list", Scope: scopes.Read,
			Description: "A server's sftp users: username, root dir, read-only flag and status. Never their password. Owner or admin.",
			InputSchema: schema([]string{"server"}, map[string]any{"server": serverArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				rows, err := env.Q.ListSFTPUsersOfServer(ctx, db.ListSFTPUsersOfServerParams{ServerID: srv.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", errors.New("could not list sftp users")
				}
				if len(rows) == 0 {
					return "No sftp users yet on " + srv.Name + ". sftp_user_create adds one.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d sftp user(s) on %s:\n", len(rows), srv.Name)
				for _, u := range rows {
					keys := 0
					if strings.TrimSpace(u.PublicKeys) != "" {
						keys = len(strings.Split(u.PublicKeys, "\n"))
					}
					pw := "no"
					if u.PasswordSealed != nil {
						pw = "yes"
					}
					mode := "read-write"
					if u.ReadOnly {
						mode = "read-only"
					}
					fmt.Fprintf(&b, "  %-16s root %-24s %-10s keys %d password %-3s status %-9s (id %s)\n", u.Username, u.RootDir, mode, keys, pw, u.Status, u.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "sftp_user_create", Scope: scopes.Write,
			Description: "Add a chrooted sftp-only system user to a server: paste a public key, or ask Hosbah to generate a password " +
				"(returned once, in this response, and never again — Hosbah stores only the sealed form). Owner or admin; audited.",
			InputSchema: schema([]string{"server", "username", "root_dir"}, map[string]any{"server": serverArg,
				"username":          str("Lowercase, starts with a letter or underscore, 1-32 characters"),
				"root_dir":          str("Absolute path on the server this user is chrooted into"),
				"public_key":        str("One OpenSSH public key (ssh-ed25519, ssh-rsa, ...); repeat with public_keys for more than one"),
				"public_keys":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "OpenSSH public keys, one per entry"},
				"generate_password": map[string]any{"type": "boolean", "description": "Have Hosbah mint a password instead of (or as well as) a key"},
				"read_only":         map[string]any{"type": "boolean", "description": "Download only, no upload/delete/rename (default false)"}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				readOnly := args.Bool("read_only")
				u, pw, err := s.CreateSFTPUser(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, deploy.SFTPUserInput{
					Username: args.Str("username"), PublicKeys: sftpKeyArgs(args), GeneratePassword: args.Bool("generate_password"),
					RootDir: args.Str("root_dir"), ReadOnly: &readOnly})
				if err != nil {
					return "", err
				}
				out := fmt.Sprintf("Added sftp user %s on %s, chrooted to %s (id %s).", u.Username, srv.Name, u.RootDir, u.ID)
				if pw != "" {
					out += " Password (shown once): " + pw
				}
				return out, nil
			},
		},
		{
			Name: "sftp_user_update", Scope: scopes.Write,
			Description: "Change an sftp user's keys, password, root dir or read-only flag. The username itself cannot change. Owner or admin; audited.",
			InputSchema: schema([]string{"server", "user"}, map[string]any{"server": serverArg, "user": userArg,
				"public_key":        str("One OpenSSH public key; repeat with public_keys for more than one. Replaces the existing keys."),
				"public_keys":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Replaces the existing keys"},
				"generate_password": map[string]any{"type": "boolean", "description": "Rotate to a new generated password (shown once)"},
				"root_dir":          str("Absolute path (optional; keeps the existing one)"),
				"read_only":         map[string]any{"type": "boolean", "description": "Optional; keeps the existing flag if omitted"}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				u, err := deploy.SFTPUserByRef(ctx, env.Q, env.Caller.TeamID, srv.ID, args.Str("user"))
				if err != nil {
					return "", errors.New("sftp user not found")
				}
				in := deploy.SFTPUserInput{GeneratePassword: args.Bool("generate_password"), RootDir: args.Str("root_dir")}
				if keys := sftpKeyArgs(args); len(keys) > 0 {
					in.PublicKeys = keys
				}
				if _, ok := args["read_only"]; ok {
					ro := args.Bool("read_only")
					in.ReadOnly = &ro
				}
				nu, pw, err := s.UpdateSFTPUser(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, u, in)
				if err != nil {
					return "", err
				}
				out := fmt.Sprintf("Changed sftp user %s on %s.", nu.Username, srv.Name)
				if pw != "" {
					out += " New password (shown once): " + pw
				}
				return out, nil
			},
		},
		{
			Name: "sftp_user_delete", Scope: scopes.Destructive, Destructive: true, ResourceType: "server",
			Description: "Remove an sftp user from its server and Hosbah. confirm must equal the user's username.",
			InputSchema: schema([]string{"server", "user", "confirm"}, map[string]any{"server": serverArg, "user": userArg,
				"confirm": str("The sftp user's username, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				u, err := deploy.SFTPUserByRef(ctx, env.Q, env.Caller.TeamID, srv.ID, args.Str("user"))
				if err != nil {
					return "", errors.New("sftp user not found")
				}
				if err := RequireConfirm(args, u.Username); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				if err := s.DeleteSFTPUser(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, u); err != nil {
					return "", err
				}
				return "Deleted sftp user " + u.Username + " on " + srv.Name + ".", nil
			},
		},
	}
}

// sftpKeyArgs collects public_key (singular, one) and public_keys (array) into one list.
func sftpKeyArgs(args Args) []string {
	var out []string
	if k := args.Str("public_key"); k != "" {
		out = append(out, k)
	}
	if raw, ok := args["public_keys"]; ok {
		if arr, ok := raw.([]any); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}
