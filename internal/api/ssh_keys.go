package api

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Team SSH key library (MH-578 C10): named public keys a team keeps around and can install onto
// (or remove from) any of its servers' authorized_keys, for a chosen unix user, through the typed
// sshkey.install / sshkey.uninstall operations (internal/agentproto/sshkeys.go). Any member reads;
// add/remove the key itself, and install/uninstall on a server, are owner or admin and audited.

// SSHKeyOut is one team SSH key (never the private half — there isn't one to hold).
type SSHKeyOut struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	KeyType     string    `json:"key_type"`
	PublicKey   string    `json:"public_key"`
	Fingerprint string    `json:"fingerprint"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

func toSSHKeyOut(row db.SshKey) SSHKeyOut {
	return SSHKeyOut{ID: row.ID, Name: row.Name, KeyType: row.KeyType, PublicKey: row.PublicKey,
		Fingerprint: row.Fingerprint, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt}
}

// SSHKeyInstallOut is one (server, unix user) install record of a key.
type SSHKeyInstallOut struct {
	ServerID    string     `json:"server_id"`
	UnixUser    string     `json:"unix_user"`
	Status      string     `json:"status"` // "installed" | "removed" | "failed"
	LastError   string     `json:"last_error,omitempty"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
	RemovedAt   *time.Time `json:"removed_at,omitempty"`
}

func toSSHKeyInstallOut(row db.SshKeyInstall) SSHKeyInstallOut {
	return SSHKeyInstallOut{ServerID: row.ServerID, UnixUser: row.UnixUser, Status: row.Status,
		LastError: row.LastError, InstalledAt: row.InstalledAt, RemovedAt: row.RemovedAt}
}

// SSHKeyCreateIn adds a key to the library. name and public_key only — type and fingerprint are
// derived, never trusted from the client.
type SSHKeyCreateIn struct {
	Name      string `json:"name" minLength:"1" maxLength:"80"`
	PublicKey string `json:"public_key" minLength:"1" maxLength:"8192" doc:"One OpenSSH authorized_keys line: \"ssh-ed25519 AAAA... [comment]\" (ed25519, rsa or ecdsa)."`
}

// SSHKeyPath addresses one SSH key of a team.
type SSHKeyPath struct {
	TeamPath
	Key string `path:"key" maxLength:"64"`
}

// SSHKeyInstallIn installs or uninstalls a team key on one server, for one unix user.
type SSHKeyInstallIn struct {
	ServerPath
	Body struct {
		User string `json:"user" minLength:"1" maxLength:"32" doc:"The unix login to edit ~/.ssh/authorized_keys of."`
	}
}

func (d *deps) registerSSHKeys(api huma.API) {
	tags := []string{"SSH keys"}
	base := "/api/teams/{team}/ssh-keys"

	type sshKeyListOut struct {
		Body struct {
			Keys []SSHKeyOut `json:"keys"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-ssh-keys", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The team's SSH key library. Any member.",
	}, func(ctx context.Context, in *TeamPath) (*sshKeyListOut, error) {
		out := &sshKeyListOut{}
		out.Body.Keys = []SSHKeyOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListTeamSSHKeys(ctx, m.TeamID)
			if err != nil {
				return actionErr(err, "could not list the team's SSH keys")
			}
			for _, r := range rows {
				out.Body.Keys = append(out.Body.Keys, toSSHKeyOut(r))
			}
			return nil
		})
		return out, err
	})

	type sshKeyOneOut struct{ Body SSHKeyOut }
	huma.Register(api, huma.Operation{OperationID: "add-ssh-key", Method: http.MethodPost, Path: base, DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Add a public key to the team's library. The key type (ed25519, rsa or ecdsa) and fingerprint are " +
			"derived from public_key, never trusted from the client. 422: not a well-formed authorized_keys line. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body SSHKeyCreateIn
	}) (*sshKeyOneOut, error) {
		out := &sshKeyOneOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			keyType, _, perr := agentproto.ParseAuthorizedKey(in.Body.PublicKey)
			if perr != nil {
				return huma.Error422UnprocessableEntity("public_key: " + perr.Error())
			}
			fp := agentproto.Fingerprint(in.Body.PublicKey)
			row, err := q.InsertTeamSSHKey(ctx, db.InsertTeamSSHKeyParams{
				TeamID: m.TeamID, Name: in.Body.Name, KeyType: keyType, PublicKey: in.Body.PublicKey,
				Fingerprint: fp, CreatedBy: id.ID,
			})
			if err != nil {
				return actionErr(err, "could not add the SSH key (the name or the key may already be in the library)")
			}
			out.Body = toSSHKeyOut(row)
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "ssh_key.added", "ssh_key", row.ID,
				map[string]any{"name": row.Name, "fingerprint": row.Fingerprint})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-ssh-key", Method: http.MethodDelete, Path: base + "/{key}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Remove a key from the library. This does not touch any server it was already installed to — uninstall it there " +
			"first. Owner or admin. Audited.",
	}, func(ctx context.Context, in *SSHKeyPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			key, err := q.GetTeamSSHKey(ctx, db.GetTeamSSHKeyParams{ID: in.Key, TeamID: m.TeamID})
			if isNoRows(err) {
				return errNotFound
			}
			if err != nil {
				return actionErr(err, "could not read the SSH key")
			}
			n, err := q.DeleteTeamSSHKey(ctx, db.DeleteTeamSSHKeyParams{ID: key.ID, TeamID: m.TeamID})
			if err != nil {
				return actionErr(err, "could not delete the SSH key")
			}
			if n == 0 {
				return errNotFound
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "ssh_key.deleted", "ssh_key", key.ID, map[string]any{"name": key.Name})
		})
		return nil, err
	})

	type sshKeyInstallsOut struct {
		Body struct {
			Installs []SSHKeyInstallOut `json:"installs"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-ssh-key-installs", Method: http.MethodGet, Path: base + "/{key}/installs", Tags: tags,
		Summary: "Every server this key has been installed to or removed from, in this team. Any member.",
	}, func(ctx context.Context, in *SSHKeyPath) (*sshKeyInstallsOut, error) {
		out := &sshKeyInstallsOut{}
		out.Body.Installs = []SSHKeyInstallOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			key, err := q.GetTeamSSHKey(ctx, db.GetTeamSSHKeyParams{ID: in.Key, TeamID: m.TeamID})
			if isNoRows(err) {
				return errNotFound
			}
			if err != nil {
				return actionErr(err, "could not read the SSH key")
			}
			rows, err := q.ListSSHKeyInstalls(ctx, db.ListSSHKeyInstallsParams{SshKeyID: key.ID, TeamID: m.TeamID})
			if err != nil {
				return actionErr(err, "could not list the key's installs")
			}
			for _, r := range rows {
				out.Body.Installs = append(out.Body.Installs, toSSHKeyInstallOut(r))
			}
			return nil
		})
		return out, err
	})

	type sshKeyInstallActionOut struct{ Body SSHKeyInstallOut }
	install := func(uninstall bool, opID, summary string) {
		verb := "install"
		if uninstall {
			verb = "uninstall"
		}
		huma.Register(api, huma.Operation{OperationID: opID, Method: http.MethodPost,
			Path: base + "/{key}/servers/{server}/" + verb, Tags: tags, Summary: summary,
		}, func(ctx context.Context, in *struct {
			SSHKeyPath
			Server string `path:"server" maxLength:"64"`
			Body   struct {
				User string `json:"user" minLength:"1" maxLength:"32" doc:"The unix login to edit ~/.ssh/authorized_keys of."`
			}
		}) (*sshKeyInstallActionOut, error) {
			out := &sshKeyInstallActionOut{}
			err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
				key, err := q.GetTeamSSHKey(ctx, db.GetTeamSSHKeyParams{ID: in.Key, TeamID: m.TeamID})
				if isNoRows(err) {
					return errNotFound
				}
				if err != nil {
					return actionErr(err, "could not read the SSH key")
				}
				srv, err := serverIn(ctx, q, m, in.Server)
				if err != nil {
					return err
				}
				status := "installed"
				var opErr string
				var installedAt, removedAt *time.Time
				now := time.Now()
				if uninstall {
					p := agentproto.SSHKeyUninstallParams{User: in.Body.User, Fingerprint: key.Fingerprint}
					if verr := p.Validate(); verr != nil {
						return huma.Error422UnprocessableEntity(verr.Error())
					}
					var res agentproto.SSHKeyInstallResult
					runErr := d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindSSHKeyUninstall, p, &res)
					if runErr != nil {
						status, opErr = "failed", runErr.Error()
					} else {
						status = "removed"
						removedAt = &now
					}
				} else {
					p := agentproto.SSHKeyInstallParams{User: in.Body.User, PublicKey: key.PublicKey, Fingerprint: key.Fingerprint}
					if verr := p.Validate(); verr != nil {
						return huma.Error422UnprocessableEntity(verr.Error())
					}
					var res agentproto.SSHKeyInstallResult
					runErr := d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindSSHKeyInstall, p, &res)
					if runErr != nil {
						status, opErr = "failed", runErr.Error()
					} else {
						installedAt = &now
					}
				}
				row, err := q.UpsertSSHKeyInstall(ctx, db.UpsertSSHKeyInstallParams{
					TeamID: m.TeamID, SshKeyID: key.ID, ServerID: srv.ID, UnixUser: in.Body.User,
					Status: status, LastError: opErr, InstalledAt: installedAt, RemovedAt: removedAt,
				})
				if err != nil {
					return actionErr(err, "could not record the key install")
				}
				out.Body = toSSHKeyInstallOut(row)
				if status == "failed" {
					return huma.Error409Conflict("could not " + verb + " the key: " + opErr)
				}
				return tenancy.Record(ctx, q, m.TeamID, id.ID, "ssh_key."+verb, "server", srv.ID,
					map[string]any{"key": key.ID, "name": key.Name, "user": in.Body.User})
			})
			return out, err
		})
	}
	install(false, "install-ssh-key", "Install a team key onto one server's chosen unix user, idempotently (marker-comment based — "+
		"never touches a line it did not add itself, including Hosbah's own enrollment key). Owner or admin. Audited.")
	install(true, "uninstall-ssh-key", "Remove a team key from one server's chosen unix user, if it was installed there by Hosbah "+
		"(the same marker comment). Owner or admin. Audited.")
}
