package mcp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/deploy"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// SecurityTools are MH-578 C9 (reset a server's root password) and C11 (a server's outbound git
// deploy key, and adding it to a GitHub repository through the team's App installation). Owner or
// admin only, like ssh_keys_install. Resetting the password and adding a key to GitHub are
// destructive: both change who or what can get in. Generating and reading the deploy key are not —
// it is idempotent, and nothing is destroyed by making or showing it.
func SecurityTools() []Tool {
	return []Tool{
		{
			Name: "security_reset_root_password", Scope: scopes.Destructive, Destructive: true, ResourceType: "server",
			Description: "Reset a server's root password with chpasswd. Hosbah generates a strong password unless one is given " +
				"(min 12 characters); it is sealed to the server's own key end to end and returned in this result exactly once — " +
				"Hosbah never stores or logs it. confirm must equal the server's id or name.",
			InputSchema: schema([]string{"server", "confirm"}, map[string]any{
				"server":   str("Server id or name"),
				"password": str("Optional. At least 12 characters. Omit to have Hosbah generate a strong one."),
				"confirm":  str("The server id or name, exactly"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, args.Str("server")); err != nil {
					return "", err
				}
				if srv.AgentPublicKey == nil {
					return "", errors.New("that server's agent has not enrolled yet")
				}
				password := args.Str("password")
				if password == "" {
					p, gerr := generateMCPPassword()
					if gerr != nil {
						return "", gerr
					}
					password = p
				} else if len(password) < 12 {
					return "", errors.New("password must be at least 12 characters")
				}
				box, serr := agentproto.SealBox(*srv.AgentPublicKey, agentproto.PasswordContext(srv.ID), []byte(password))
				if serr != nil {
					return "", errors.New("could not seal the password to the server's key")
				}
				p := agentproto.PasswdSetParams{User: "root", PasswordBox: box}
				var res agentproto.PasswdSetResult
				if err := runServiceOp(ctx, env, srv.ID, agentproto.KindPasswdSet, p, &res); err != nil {
					return "", err
				}
				return fmt.Sprintf("Root password reset on %s. New password (shown once, not stored): %s", srv.Name, password), nil
			},
		},
		{
			Name: "server_deploy_key_get", Scope: scopes.Read,
			Description: "The server's outbound git deploy key, if one has been generated. Public half only.",
			InputSchema: schema([]string{"server"}, map[string]any{"server": str("Server id or name")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				row, err := env.Q.GetServerDeployKey(ctx, db.GetServerDeployKeyParams{ServerID: srv.ID, TeamID: env.Caller.TeamID})
				if isNoRowsMCP(err) {
					return "No deploy key generated yet for " + srv.Name + ".", nil
				}
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("%s (%s):\n%s", srv.Name, row.Fingerprint, row.PublicKey), nil
			},
		},
		{
			Name: "server_deploy_key_generate", Scope: scopes.Write,
			Description: "Generate (or, idempotently, return) the server's outbound git deploy key: an ed25519 keypair created on " +
				"the server itself, with a known_hosts entry for github.com. The private half never leaves the server.",
			InputSchema: schema([]string{"server"}, map[string]any{"server": str("Server id or name")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				srv, res, err := generateDeployKey(ctx, env, args)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Deploy key for %s (%s):\n%s", srv.Name, res.Fingerprint, res.PublicKey), nil
			},
		},
		{
			Name: "server_deploy_key_add_to_github", Scope: scopes.Destructive, Destructive: true, ResourceType: "server",
			Description: "Add the server's deploy key (generating one first if needed) as a read-only GitHub deploy key of repo, " +
				"through the team's connected GitHub App installation. confirm must equal the server's id or name.",
			InputSchema: schema([]string{"server", "repo", "confirm"}, map[string]any{
				"server":  str("Server id or name"),
				"repo":    str("\"owner/name\" of a repository the team's GitHub App installation can reach"),
				"title":   str("Optional. Defaults to the server's name."),
				"confirm": str("The server id or name, exactly"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, args.Str("server")); err != nil {
					return "", err
				}
				row, err := env.Q.GetServerDeployKey(ctx, db.GetServerDeployKeyParams{ServerID: srv.ID, TeamID: env.Caller.TeamID})
				if isNoRowsMCP(err) {
					_, res, gerr := generateDeployKey(ctx, env, args)
					if gerr != nil {
						return "", gerr
					}
					row = db.ServerDeployKey{PublicKey: res.PublicKey, Fingerprint: res.Fingerprint}
				} else if err != nil {
					return "", err
				}
				dep, ok := env.App.Deploy.(*deploy.Service)
				if !ok || dep == nil {
					return "", errors.New("GitHub App not configured")
				}
				title := args.Str("title")
				if title == "" {
					title = srv.Name
				}
				keyID, aerr := dep.AddDeployKeyToRepo(ctx, env.Caller.TeamID, args.Str("repo"), title, row.PublicKey)
				if aerr != nil {
					return "", aerr
				}
				return fmt.Sprintf("Added %s's deploy key to %s (GitHub key id %d).", srv.Name, args.Str("repo"), keyID), nil
			},
		},
	}
}

func generateDeployKey(ctx context.Context, env *Env, args Args) (db.Server, agentproto.DeployKeyGenerateResult, error) {
	srv, err := findServer(ctx, env, args)
	if err != nil {
		return db.Server{}, agentproto.DeployKeyGenerateResult{}, err
	}
	if srv.AgentPublicKey == nil {
		return db.Server{}, agentproto.DeployKeyGenerateResult{}, errors.New("that server's agent has not enrolled yet")
	}
	var res agentproto.DeployKeyGenerateResult
	if err := runServiceOp(ctx, env, srv.ID, agentproto.KindDeployKeyGenerate, agentproto.DeployKeyGenerateParams{}, &res); err != nil {
		return db.Server{}, agentproto.DeployKeyGenerateResult{}, err
	}
	if _, err := env.Q.UpsertServerDeployKey(ctx, db.UpsertServerDeployKeyParams{
		TeamID: env.Caller.TeamID, ServerID: srv.ID, PublicKey: res.PublicKey, Fingerprint: res.Fingerprint,
	}); err != nil {
		return db.Server{}, agentproto.DeployKeyGenerateResult{}, err
	}
	return srv, res, nil
}

func isNoRowsMCP(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func generateMCPPassword() (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!@#%^*-_=+"
	const length = 24
	out := make([]byte, length)
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}
