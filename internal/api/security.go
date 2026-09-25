package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Server security (MH-578 C9 root-password reset, C11 outbound deploy key). Both are owner/admin
// only and audited — never with the password, never with the private half of a key.

// RootPasswordResetIn optionally supplies a caller-chosen password; otherwise the control plane
// generates one.
type RootPasswordResetIn struct {
	ServerPath
	Body struct {
		Password string `json:"password,omitempty" minLength:"12" maxLength:"128" doc:"Optional. At least 12 characters. Omit to have Hosbah generate a strong one."`
	}
}

// RootPasswordResetOut carries the new password exactly once. Nothing about it is stored or
// logged — the caller must copy it now.
type RootPasswordResetOut struct {
	Body struct {
		User     string `json:"user"`
		Password string `json:"password" doc:"Shown once. Hosbah does not keep it — if it is lost, reset again."`
	}
}

// generatePassword returns a strong random password: 24 characters from a set wide enough to be
// unambiguous to read out loud while still mixing cases, digits and symbols.
func generatePassword() (string, error) {
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

func (d *deps) registerServerSecurity(api huma.API) {
	tags := []string{"Server security"}
	base := "/api/teams/{team}/servers/{server}"

	huma.Register(api, huma.Operation{OperationID: "reset-server-root-password", Method: http.MethodPost, Path: base + "/root-password/reset", Tags: tags,
		Summary: "Reset the server's root password with chpasswd. Hosbah generates a strong password unless one is given (min 12 " +
			"characters); either way, it is sealed to the server's own key end to end and shown in this response exactly once — " +
			"Hosbah never stores or logs it. Owner or admin. Audited (never with the password).",
	}, func(ctx context.Context, in *RootPasswordResetIn) (*RootPasswordResetOut, error) {
		out := &RootPasswordResetOut{}
		out.Body.User = "root"
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if srv.AgentPublicKey == nil {
				return huma.Error409Conflict("that server's agent has not enrolled yet")
			}
			password := in.Body.Password
			if password == "" {
				p, gerr := generatePassword()
				if gerr != nil {
					return huma.Error500InternalServerError("could not generate a password")
				}
				password = p
			}
			box, serr := agentproto.SealBox(*srv.AgentPublicKey, agentproto.PasswordContext(srv.ID), []byte(password))
			if serr != nil {
				return huma.Error500InternalServerError("could not seal the password to the server's key")
			}
			p := agentproto.PasswdSetParams{User: "root", PasswordBox: box}
			var res agentproto.PasswdSetResult
			if runErr := d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindPasswdSet, p, &res); runErr != nil {
				return actionErr(runErr, "could not reset the root password")
			}
			out.Body.Password = password
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "server.root_password_reset", "server", srv.ID, map[string]any{"user": "root"})
		})
		return out, err
	})

	type deployKeyOut struct {
		Body struct {
			PublicKey   string     `json:"public_key,omitempty"`
			Fingerprint string     `json:"fingerprint,omitempty"`
			CreatedAt   *time.Time `json:"created_at,omitempty"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "get-server-deploy-key", Method: http.MethodGet, Path: base + "/deploy-key", Tags: tags,
		Summary: "The server's outbound git deploy key, if one has been generated. Public half only. Any member.",
	}, func(ctx context.Context, in *ServerPath) (*deployKeyOut, error) {
		out := &deployKeyOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			row, err := q.GetServerDeployKey(ctx, db.GetServerDeployKeyParams{ServerID: srv.ID, TeamID: m.TeamID})
			if isNoRows(err) {
				return nil
			}
			if err != nil {
				return actionErr(err, "could not read the deploy key")
			}
			out.Body.PublicKey, out.Body.Fingerprint, out.Body.CreatedAt = row.PublicKey, row.Fingerprint, &row.CreatedAt
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "generate-server-deploy-key", Method: http.MethodPost, Path: base + "/deploy-key/generate", Tags: tags,
		Summary: "Generate (or, idempotently, return) the server's outbound git deploy key: an ed25519 keypair created on the " +
			"server itself, with a known_hosts entry for github.com. The private half never leaves the server. Owner or admin. Audited.",
	}, func(ctx context.Context, in *ServerPath) (*deployKeyOut, error) {
		out := &deployKeyOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if srv.AgentPublicKey == nil {
				return huma.Error409Conflict("that server's agent has not enrolled yet")
			}
			var res agentproto.DeployKeyGenerateResult
			if runErr := d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindDeployKeyGenerate, agentproto.DeployKeyGenerateParams{}, &res); runErr != nil {
				return actionErr(runErr, "could not generate the deploy key")
			}
			row, err := q.UpsertServerDeployKey(ctx, db.UpsertServerDeployKeyParams{
				TeamID: m.TeamID, ServerID: srv.ID, PublicKey: res.PublicKey, Fingerprint: res.Fingerprint,
			})
			if err != nil {
				return actionErr(err, "could not save the deploy key")
			}
			out.Body.PublicKey, out.Body.Fingerprint, out.Body.CreatedAt = row.PublicKey, row.Fingerprint, &row.CreatedAt
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "server.deploy_key_generated", "server", srv.ID,
				map[string]any{"fingerprint": row.Fingerprint})
		})
		return out, err
	})

	type githubAddIn struct {
		ServerPath
		Body struct {
			Repo  string `json:"repo" minLength:"3" maxLength:"200" doc:"\"owner/name\" of a repository the team's GitHub App installation can reach."`
			Title string `json:"title,omitempty" maxLength:"80" doc:"Optional. Defaults to the server's name."`
		}
	}
	type githubAddOut struct {
		Body struct {
			KeyID int64 `json:"key_id"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "add-server-deploy-key-to-github", Method: http.MethodPost, Path: base + "/deploy-key/github-add", Tags: tags,
		Summary: "Add the server's deploy key (generating one first if needed) as a read-only GitHub deploy key of repo, through " +
			"the team's connected GitHub App installation. 409: no installation of the team covers that repository, or the app " +
			"is not configured — copy the public key and add it by hand instead. Owner or admin. Audited.",
	}, func(ctx context.Context, in *githubAddIn) (*githubAddOut, error) {
		out := &githubAddOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			row, gerr := q.GetServerDeployKey(ctx, db.GetServerDeployKeyParams{ServerID: srv.ID, TeamID: m.TeamID})
			if isNoRows(gerr) {
				if srv.AgentPublicKey == nil {
					return huma.Error409Conflict("that server's agent has not enrolled yet")
				}
				var res agentproto.DeployKeyGenerateResult
				if runErr := d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindDeployKeyGenerate, agentproto.DeployKeyGenerateParams{}, &res); runErr != nil {
					return actionErr(runErr, "could not generate the deploy key")
				}
				row, gerr = q.UpsertServerDeployKey(ctx, db.UpsertServerDeployKeyParams{
					TeamID: m.TeamID, ServerID: srv.ID, PublicKey: res.PublicKey, Fingerprint: res.Fingerprint,
				})
			}
			if gerr != nil {
				return actionErr(gerr, "could not read the deploy key")
			}
			svc := d.deploys()
			if svc == nil {
				return huma.Error409Conflict("GitHub App not configured")
			}
			title := in.Body.Title
			if title == "" {
				title = srv.Name
			}
			keyID, aerr := svc.AddDeployKeyToRepo(ctx, m.TeamID, in.Body.Repo, title, row.PublicKey)
			if aerr != nil {
				return huma.Error409Conflict(aerr.Error())
			}
			out.Body.KeyID = keyID
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "server.deploy_key_added_to_github", "server", srv.ID,
				map[string]any{"repo": in.Body.Repo})
		})
		return out, err
	})
}
