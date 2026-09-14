package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/cloudy/internal/crypto"
	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/provisioning"
	"github.com/fadymondy/cloudy/internal/tenancy"
)

// CredentialOut is a provider credential without its secret. The secret is sealed with the team's
// key and only ever returned by the audited reveal endpoint.
type CredentialOut struct {
	ID              string            `json:"id"`
	Provider        string            `json:"provider" enum:"proxmox"`
	Name            string            `json:"name"`
	Config          map[string]string `json:"config" doc:"Non-secret settings (API URL, token id, node, storage…)."`
	LastValidatedAt *time.Time        `json:"last_validated_at"`
	CreatedAt       time.Time         `json:"created_at"`
}

func toCredentialOut(c db.Credential) CredentialOut {
	cfg := map[string]string{}
	_ = json.Unmarshal([]byte(c.Config), &cfg)
	return CredentialOut{ID: c.ID, Provider: c.Provider, Name: c.Name, Config: cfg, LastValidatedAt: c.LastValidatedAt, CreatedAt: c.CreatedAt}
}

// CredentialPath addresses one credential of a team.
type CredentialPath struct {
	TeamPath
	Credential string `path:"credential" maxLength:"64"`
}

func (d *deps) registerCredentials(api huma.API) {
	type credsOut struct {
		Body struct {
			Credentials []CredentialOut `json:"credentials"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-credentials", Method: http.MethodGet, Path: "/api/teams/{team}/credentials",
		Summary: "The team's provider credentials, never their secrets (owner or admin)", Tags: []string{"Credentials"},
	}, func(ctx context.Context, in *TeamPath) (*credsOut, error) {
		out := &credsOut{}
		out.Body.Credentials = []CredentialOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListCredentialsOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list credentials")
			}
			for _, r := range rows {
				out.Body.Credentials = append(out.Body.Credentials, toCredentialOut(r))
			}
			return nil
		})
		return out, err
	})

	type credOut struct{ Body CredentialOut }
	huma.Register(api, huma.Operation{
		OperationID: "create-credential", Method: http.MethodPost, Path: "/api/teams/{team}/credentials",
		DefaultStatus: http.StatusCreated, Tags: []string{"Credentials"},
		Summary: "Add a provider credential (owner or admin). The secret is sealed with the team key; " +
			"by default the credential is checked against the provider first.",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body struct {
			Provider string            `json:"provider" enum:"proxmox"`
			Name     string            `json:"name" minLength:"1" maxLength:"80"`
			Config   map[string]string `json:"config" doc:"proxmox: api_url, token_id, node, storage, bridge, pool, template_vmid, lxc_template, snippets_storage, snippets_dir, tls_fingerprint, insecure_tls"`
			Secret   string            `json:"secret" minLength:"1" maxLength:"4096" doc:"proxmox: the API token secret."`
			// Not `validate` with default:"true": huma re-applies a bool default over an explicit false.
			SkipValidation bool `json:"skip_validation,omitempty" doc:"Save without checking the credential against the provider first."`
		}
	}) (*credOut, error) {
		out := &credOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			b := in.Body
			cfg, err := provisioning.CleanConfig(b.Provider, b.Config)
			if err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			var validated *time.Time
			if !b.SkipValidation {
				// The control plane calls the URL given here: only owners and admins may, and the
				// provider driver refuses anything but https.
				vctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				err := provisioning.Validate(vctx, b.Provider, cfg, b.Secret)
				cancel()
				if err != nil {
					return huma.Error422UnprocessableEntity("the provider refused this credential: " + err.Error())
				}
				validated = ptr(time.Now().UTC())
			}
			credID := newUUID()
			dek, err := d.ops.TeamDEK(ctx, q, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("team key unavailable")
			}
			sealed, err := crypto.Seal(dek, m.TeamID, provisioning.CredentialContext(credID), []byte(b.Secret))
			if err != nil {
				return huma.Error500InternalServerError("could not seal the secret")
			}
			raw, _ := json.Marshal(cfg)
			row, err := q.InsertCredential(ctx, db.InsertCredentialParams{ID: credID, TeamID: m.TeamID, Provider: b.Provider,
				Name: strings.TrimSpace(b.Name), Config: string(raw), SecretSealed: sealed, CreatedBy: id.ID, LastValidatedAt: validated})
			if err != nil {
				if strings.Contains(err.Error(), "credentials_team_name_key") {
					return huma.Error409Conflict("a credential with that name already exists in this team")
				}
				return huma.Error500InternalServerError("could not save the credential")
			}
			if err := tenancy.Record(ctx, q, m.TeamID, id.ID, "credential.created", "credential", row.ID,
				map[string]any{"name": row.Name, "provider": row.Provider, "validated": validated != nil}); err != nil {
				return huma.Error500InternalServerError("audit failed")
			}
			out.Body = toCredentialOut(row)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-credential", Method: http.MethodDelete, Path: "/api/teams/{team}/credentials/{credential}",
		DefaultStatus: http.StatusNoContent, Tags: []string{"Credentials"},
		Summary: "Delete a credential (owner or admin). Servers created with it are kept, but Cloudy can no longer manage their machines.",
	}, func(ctx context.Context, in *CredentialPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			c, err := q.CredentialInTeam(ctx, db.CredentialInTeamParams{ID: in.Credential, TeamID: m.TeamID})
			if err != nil {
				return errNotFound
			}
			if n, err := q.DeleteCredentialInTeam(ctx, db.DeleteCredentialInTeamParams{ID: c.ID, TeamID: m.TeamID}); err != nil || n == 0 {
				return errNotFound
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "credential.deleted", "credential", c.ID, map[string]any{"name": c.Name})
		})
		return nil, err
	})

	type revealOut struct {
		Body struct {
			Secret string `json:"secret"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "reveal-credential", Method: http.MethodPost, Path: "/api/teams/{team}/credentials/{credential}/reveal",
		Tags:    []string{"Credentials"},
		Summary: "Reveal a credential's secret (owner or admin). Every reveal is written to the audit log.",
	}, func(ctx context.Context, in *CredentialPath) (*revealOut, error) {
		out := &revealOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityRevealSecrets, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			c, err := q.CredentialInTeam(ctx, db.CredentialInTeamParams{ID: in.Credential, TeamID: m.TeamID})
			if err != nil {
				return errNotFound
			}
			dek, err := d.ops.TeamDEK(ctx, q, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("team key unavailable")
			}
			secret, err := crypto.Open(dek, m.TeamID, provisioning.CredentialContext(c.ID), c.SecretSealed)
			if err != nil {
				return huma.Error500InternalServerError("the secret could not be opened")
			}
			// Same transaction: if the audit entry cannot be written, the secret is not returned.
			if err := tenancy.Record(ctx, q, m.TeamID, id.ID, "credential.revealed", "credential", c.ID, map[string]any{"name": c.Name}); err != nil {
				return huma.Error500InternalServerError("audit failed")
			}
			out.Body.Secret = string(secret)
			return nil
		})
		return out, err
	})
}
