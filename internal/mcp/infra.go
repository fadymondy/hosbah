package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/cloudy/internal/crypto"
	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/ops"
	"github.com/fadymondy/cloudy/internal/provisioning"
	"github.com/fadymondy/cloudy/internal/scopes"
	"github.com/fadymondy/cloudy/internal/tenancy"
)

// InfraTools: adding servers (install command, SSH connect, Proxmox) and provider credentials
// (Proxmox, Cloudflare) — the same functions the REST API uses.
func InfraTools() []Tool {
	return []Tool{
		{
			Name: "add_server", Scope: scopes.Write,
			Description: "Add a server. method=install returns a one-time install command to run on it. method=connect logs in over SSH " +
				"(root or a passwordless-sudo user, password or private key), detects plain Linux / Docker / Proxmox VE, installs Cloudy's key " +
				"and the agent, and imports what already runs; the login is used once and forgotten. method=proxmox creates a VM or LXC " +
				"with one of the team's Proxmox credentials. Follow progress with get_server / list_operations.",
			InputSchema: schema([]string{"name", "method"}, map[string]any{
				"name":             str("Letters, digits, dot, dash, underscore; unique in the team"),
				"method":           map[string]any{"type": "string", "enum": []string{"install", "connect", "proxmox"}},
				"host":             str("connect: IP address or hostname"),
				"port":             map[string]any{"type": "integer", "description": "connect: SSH port (default 22)"},
				"user":             str("connect: root, or a user with passwordless sudo (default root)"),
				"password":         str("connect: the password (or give private_key)"),
				"private_key":      str("connect: an OpenSSH/PEM private key without a passphrase"),
				"register_proxmox": map[string]any{"type": "boolean", "default": true, "description": "connect: if the host is a Proxmox VE node, also save it as a Proxmox credential"},
				"credential":       str("proxmox: credential id or name"),
				"kind":             map[string]any{"type": "string", "enum": []string{"vm", "lxc"}},
				"size":             map[string]any{"type": "string", "enum": []string{"small", "medium", "large"}},
				"ipconfig":         str("proxmox: static address for networks without DHCP, e.g. ip=10.10.10.121/24,gw=10.10.10.1"),
				"nameserver":       str("proxmox LXC: resolver for a static address, e.g. 1.1.1.1"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				svc, err := opsOf(env)
				if err != nil {
					return "", err
				}
				in := provisioning.AddInput{Name: args.Str("name"), Provider: "byo"}
				switch args.Str("method") {
				case "install":
				case "connect":
					user := args.Str("user")
					if user == "" {
						user = "root"
					}
					pw, _ := args["password"].(string) // not trimmed: a password may start or end with a space
					c := &provisioning.ConnectInput{Host: args.Str("host"), Port: args.Int("port", 22, 1, 65535), User: user,
						Auth: "password", Password: pw}
					if k, _ := args["private_key"].(string); strings.TrimSpace(k) != "" {
						c.Auth, c.PrivateKey, c.Password = "key", k, ""
					}
					if v, ok := args["register_proxmox"].(bool); ok {
						c.RegisterProxmox = &v
					}
					in.Connect = c
				case "proxmox":
					cred, err := findCredential(ctx, env, args.Str("credential"))
					if err != nil {
						return "", err
					}
					in.Provider, in.CredentialID, in.Kind, in.Size = "proxmox", cred.ID, args.Str("kind"), args.Str("size")
					in.IPConfig, in.Nameserver = args.Str("ipconfig"), args.Str("nameserver")
				default:
					return "", errors.New("method must be install, connect or proxmox")
				}
				s, inst, err := provisioning.AddServer(ctx, svc, env.Q, env.Caller.TeamID, actor(env), in)
				if err != nil {
					return "", err
				}
				switch {
				case inst != nil:
					return fmt.Sprintf("Added %s (id %s). Run this on the server as a sudo user — it works once and expires %s:\n  %s\nAlready root:\n  %s\n",
						s.Name, s.ID, inst.ExpiresAt.UTC().Format(time.RFC3339), inst.Command, inst.CommandRoot), nil
				case in.Connect != nil:
					return fmt.Sprintf("Added %s (id %s): connecting to %s over SSH now. Watch get_server %s until it is enrolled.\n", s.Name, s.ID, in.Connect.Host, s.Name), nil
				default:
					return fmt.Sprintf("Added %s (id %s): creating a %s on Proxmox now. Watch get_server %s.\n", s.Name, s.ID, in.Kind, s.Name), nil
				}
			},
		},
		{
			Name: "list_credentials", Scope: scopes.Read,
			Description: "The team's provider credentials (Proxmox, Cloudflare): name, provider, non-secret settings, last validated. Never the secret.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				rows, err := env.Q.ListCredentialsOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list credentials")
				}
				if len(rows) == 0 {
					return "No credentials yet.", nil
				}
				var b strings.Builder
				for _, c := range rows {
					validated := "never"
					if c.LastValidatedAt != nil {
						validated = c.LastValidatedAt.UTC().Format(time.RFC3339)
					}
					fmt.Fprintf(&b, "  %-10s %-24s validated %s  config %s  (id %s)\n", c.Provider, c.Name, validated, c.Config, c.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "add_credential", Scope: scopes.Write,
			Description: "Save a provider credential, sealed with the team key and checked against the provider first. " +
				"cloudflare: secret = an API token with Zone:Read + DNS:Edit — custom domains in its zones then get DNS records and verification automatically. " +
				"proxmox: secret = the API token secret; config = api_url, token_id, node, storage (+ bridge, pool, template_vmid, lxc_template, tls_fingerprint…).",
			InputSchema: schema([]string{"provider", "name", "secret"}, map[string]any{
				"provider": map[string]any{"type": "string", "enum": []string{"cloudflare", "proxmox"}},
				"name":     str("A name unique in the team"),
				"secret":   str("The API token (secret)"),
				"config":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				svc, err := opsOf(env)
				if err != nil {
					return "", err
				}
				raw := map[string]string{}
				if m, ok := args["config"].(map[string]any); ok {
					for k, v := range m {
						raw[k] = fmt.Sprint(v)
					}
				}
				provider, name, secret := args.Str("provider"), args.Str("name"), args.Str("secret")
				cfg, err := provisioning.CleanConfig(provider, raw)
				if err != nil {
					return "", err
				}
				if name == "" || secret == "" {
					return "", errors.New("name and secret are required")
				}
				vctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				err = provisioning.Validate(vctx, provider, cfg, secret)
				cancel()
				if err != nil {
					return "", fmt.Errorf("the provider refused this credential, nothing was saved: %w", err)
				}
				dek, err := svc.TeamDEK(ctx, env.Q, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("team key unavailable")
				}
				id := newCredentialID()
				sealed, err := crypto.Seal(dek, env.Caller.TeamID, provisioning.CredentialContext(id), []byte(secret))
				if err != nil {
					return "", errors.New("could not seal the secret")
				}
				cfgJSON, _ := json.Marshal(cfg)
				now := time.Now().UTC()
				row, err := env.Q.InsertCredential(ctx, db.InsertCredentialParams{ID: id, TeamID: env.Caller.TeamID, Provider: provider, Name: name,
					Config: string(cfgJSON), SecretSealed: sealed, CreatedBy: actor(env), LastValidatedAt: &now})
				if err != nil {
					if strings.Contains(err.Error(), "credentials_team_name_key") {
						return "", errors.New("a credential with that name already exists in this team")
					}
					return "", errors.New("could not save the credential")
				}
				if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "credential.created", "credential", row.ID,
					auditChanges(env, map[string]any{"name": name, "provider": provider, "validated": true})); err != nil {
					return "", err
				}
				return fmt.Sprintf("Saved %s credential %s (id %s), validated just now.\n", provider, name, row.ID), nil
			},
		},
	}
}

func opsOf(env *Env) (*ops.Service, error) {
	if env.App != nil {
		if s, ok := env.App.Ops.(*ops.Service); ok && s != nil {
			return s, nil
		}
	}
	return nil, errors.New("the operations service is not running")
}

func findCredential(ctx context.Context, env *Env, ref string) (db.Credential, error) {
	rows, err := env.Q.ListCredentialsOfTeam(ctx, env.Caller.TeamID)
	if err != nil {
		return db.Credential{}, errors.New("could not list credentials")
	}
	for _, c := range rows {
		if c.ID == ref || strings.EqualFold(c.Name, ref) {
			return c, nil
		}
	}
	return db.Credential{}, errors.New("credential not found")
}

func newCredentialID() string { return provisioning.NewID() }
