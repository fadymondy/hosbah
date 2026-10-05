package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// SSH access to one server (the SSH page of a server and of a server user's panel): the shell logins
// Hosbah created there (typed sshuser.set / sshuser.remove), and the library keys installed there
// (sshkey.install, see ssh_keys.go). Any member reads; changing either is owner or admin and audited.
// A password is sealed to the server's agent key and shown once, never stored.

// SSHUserOut is one shell login Hosbah created on a server.
type SSHUserOut struct {
	Username  string    `json:"username"`
	Sudo      bool      `json:"sudo"`
	Status    string    `json:"status"` // "ready" | "failed"
	LastError string    `json:"last_error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toSSHUserOut(r db.ServerSshUser) SSHUserOut {
	return SSHUserOut{Username: r.Username, Sudo: r.Sudo, Status: r.Status, LastError: r.LastError, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// ServerSSHKeyOut is one library key's install on this server, for one unix user.
type ServerSSHKeyOut struct {
	KeyID       string     `json:"key_id"`
	Name        string     `json:"name"`
	KeyType     string     `json:"key_type"`
	Fingerprint string     `json:"fingerprint"`
	UnixUser    string     `json:"unix_user"`
	Status      string     `json:"status"` // "installed" | "removed" | "failed"
	LastError   string     `json:"last_error,omitempty"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
}

// SSHUserPath addresses one SSH login on a server.
type SSHUserPath struct {
	TeamPath
	Server  string `path:"server" maxLength:"64"`
	SSHUser string `path:"ssh_user" maxLength:"32"`
}

// SSHUserPasswordIn chooses a login's password: none, a generated one, or a given one (min 12).
type SSHUserPasswordIn struct {
	Password         string `json:"password,omitempty" maxLength:"128" doc:"A password of at least 12 characters. Leave empty for key-only access, or set generate_password."`
	GeneratePassword bool   `json:"generate_password,omitempty" doc:"Have Hosbah generate a strong password, shown once in the response."`
}

func (p SSHUserPasswordIn) pick() (string, error) {
	if p.Password != "" {
		if len(p.Password) < 12 {
			return "", huma.Error422UnprocessableEntity("password: at least 12 characters")
		}
		return p.Password, nil
	}
	if !p.GeneratePassword {
		return "", nil
	}
	pw, err := generatePassword()
	if err != nil {
		return "", huma.Error500InternalServerError("could not generate a password")
	}
	return pw, nil
}

type sshUserOneOut struct {
	Body struct {
		User     SSHUserOut `json:"user"`
		Password string     `json:"password,omitempty" doc:"The password just set, shown this once. Hosbah does not keep it."`
	}
}

// sshUserSet runs sshuser.set on the server and records the outcome. A failed op is recorded too
// (status failed) and returned as a 409 with the agent's reason.
func (d *deps) sshUserSet(ctx context.Context, q *db.Queries, m *tenancy.Membership, id *auth.Identity, srv db.Server, username string, sudo bool, password string) (db.ServerSshUser, error) {
	p := agentproto.SSHUserSetParams{User: username, Sudo: sudo}
	if password != "" {
		box, err := agentproto.SealBox(*srv.AgentPublicKey, agentproto.SSHUserPasswordContext(srv.ID, username), []byte(password))
		if err != nil {
			return db.ServerSshUser{}, huma.Error500InternalServerError("could not seal the password to the server's key")
		}
		p.PasswordBox = box
	}
	if err := p.Validate(); err != nil {
		return db.ServerSshUser{}, huma.Error422UnprocessableEntity("username: " + err.Error())
	}
	var res agentproto.SSHUserSetResult
	status, lastErr := "ready", ""
	if runErr := d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindSSHUserSet, p, &res); runErr != nil {
		status, lastErr = "failed", runErr.Error()
	}
	row, err := q.UpsertServerSSHUser(ctx, db.UpsertServerSSHUserParams{TeamID: m.TeamID, ServerID: srv.ID, Username: username,
		Sudo: sudo, Status: status, LastError: lastErr, CreatedBy: id.ID})
	if err != nil {
		return db.ServerSshUser{}, actionErr(err, "could not record the SSH user")
	}
	if status == "failed" {
		return row, huma.Error409Conflict("could not set up the SSH user: " + lastErr)
	}
	return row, nil
}

func enrolledServer(ctx context.Context, q *db.Queries, m *tenancy.Membership, server string) (db.Server, error) {
	srv, err := serverIn(ctx, q, m, server)
	if err != nil {
		return srv, err
	}
	if srv.AgentPublicKey == nil {
		return srv, huma.Error409Conflict("that server's agent has not enrolled yet")
	}
	return srv, nil
}

func (d *deps) registerSSHUsers(api huma.API) {
	tags := []string{"SSH access"}
	base := "/api/teams/{team}/servers/{server}"

	type sshAccessOut struct {
		Body struct {
			Users []SSHUserOut       `json:"users"`
			Keys  []ServerSSHKeyOut `json:"keys"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "get-server-ssh-access", Method: http.MethodGet, Path: base + "/ssh-access", Tags: tags,
		Summary: "Who can reach this server over SSH through Hosbah: the shell logins it created and the library keys installed " +
			"here (with their unix user). Any member.",
	}, func(ctx context.Context, in *ServerPath) (*sshAccessOut, error) {
		out := &sshAccessOut{}
		out.Body.Users, out.Body.Keys = []SSHUserOut{}, []ServerSSHKeyOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			users, err := q.ListServerSSHUsers(ctx, db.ListServerSSHUsersParams{ServerID: srv.ID, TeamID: m.TeamID})
			if err != nil {
				return actionErr(err, "could not list the SSH users")
			}
			for _, u := range users {
				out.Body.Users = append(out.Body.Users, toSSHUserOut(u))
			}
			keys, err := q.ListServerSSHKeyInstalls(ctx, db.ListServerSSHKeyInstallsParams{ServerID: srv.ID, TeamID: m.TeamID})
			if err != nil {
				return actionErr(err, "could not list the SSH keys")
			}
			for _, k := range keys {
				if k.Status == "removed" {
					continue
				}
				out.Body.Keys = append(out.Body.Keys, ServerSSHKeyOut{KeyID: k.SshKeyID, Name: k.KeyName, KeyType: k.KeyType, Fingerprint: k.Fingerprint,
					UnixUser: k.UnixUser, Status: k.Status, LastError: k.LastError, InstalledAt: k.InstalledAt})
			}
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "create-server-ssh-user", Method: http.MethodPost, Path: base + "/ssh-users",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Create a shell login (home directory, bash) on the server, optionally an admin (sudo), with a password and/or " +
			"a public key. The key also joins the team's SSH key library. A user that exists and was not created by Hosbah is " +
			"refused. Owner or admin. Audited (never with the password).",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body struct {
			Username string `json:"username" minLength:"1" maxLength:"32" pattern:"^[a-z_][a-z0-9_-]*$"`
			Sudo     bool   `json:"sudo,omitempty"`
			SSHUserPasswordIn
			PublicKey string `json:"public_key,omitempty" maxLength:"8192" doc:"An OpenSSH public key line to install for this user."`
			KeyName   string `json:"key_name,omitempty" maxLength:"80" doc:"The key's name in the library (default: username@server)."`
		}
	}) (*sshUserOneOut, error) {
		out := &sshUserOneOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := enrolledServer(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if !agentproto.ValidSSHUser(in.Body.Username) {
				return huma.Error422UnprocessableEntity("username: that name is reserved on servers; pick another")
			}
			if in.Body.PublicKey == "" && in.Body.Password == "" && !in.Body.GeneratePassword {
				return huma.Error422UnprocessableEntity("give the user a public key, a password, or both")
			}
			var key db.SshKey
			if in.Body.PublicKey != "" {
				name := in.Body.KeyName
				if name == "" {
					name = in.Body.Username + "@" + srv.Name
				}
				if key, err = libraryKey(ctx, q, m, id, name, in.Body.PublicKey); err != nil {
					return err
				}
			}
			password, err := in.Body.pick()
			if err != nil {
				return err
			}
			row, err := d.sshUserSet(ctx, q, m, id, srv, in.Body.Username, in.Body.Sudo, password)
			out.Body.User = toSSHUserOut(row)
			if err != nil {
				return err
			}
			if key.ID != "" {
				if err := d.installKey(ctx, q, m, srv, key, in.Body.Username); err != nil {
					return err
				}
			}
			out.Body.Password = password
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "server.ssh_user_created", "server", srv.ID,
				map[string]any{"user": in.Body.Username, "sudo": in.Body.Sudo, "key": key.ID, "password": password != ""})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-server-ssh-user", Method: http.MethodPatch, Path: base + "/ssh-users/{ssh_user}", Tags: tags,
		Summary: "Change a Hosbah-created login: admin (sudo) or not, and optionally a new password (given or generated, shown " +
			"once). Owner or admin. Audited (never with the password).",
	}, func(ctx context.Context, in *struct {
		SSHUserPath
		Body struct {
			Sudo bool `json:"sudo"`
			SSHUserPasswordIn
		}
	}) (*sshUserOneOut, error) {
		out := &sshUserOneOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := enrolledServer(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if _, err := q.GetServerSSHUser(ctx, db.GetServerSSHUserParams{ServerID: srv.ID, TeamID: m.TeamID, Username: in.SSHUser}); err != nil {
				return errNotFound
			}
			password, err := in.Body.pick()
			if err != nil {
				return err
			}
			row, err := d.sshUserSet(ctx, q, m, id, srv, in.SSHUser, in.Body.Sudo, password)
			out.Body.User = toSSHUserOut(row)
			if err != nil {
				return err
			}
			out.Body.Password = password
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "server.ssh_user_updated", "server", srv.ID,
				map[string]any{"user": in.SSHUser, "sudo": in.Body.Sudo, "password": password != ""})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-server-ssh-user", Method: http.MethodDelete, Path: base + "/ssh-users/{ssh_user}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Remove a Hosbah-created login from the server, with its home directory; its sessions are ended. Owner or admin. Audited.",
	}, func(ctx context.Context, in *SSHUserPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if _, err := q.GetServerSSHUser(ctx, db.GetServerSSHUserParams{ServerID: srv.ID, TeamID: m.TeamID, Username: in.SSHUser}); err != nil {
				return errNotFound
			}
			p := agentproto.SSHUserRemoveParams{User: in.SSHUser}
			if err := p.Validate(); err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			var res agentproto.SSHUserRemoveResult
			if runErr := d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindSSHUserRemove, p, &res); runErr != nil {
				return huma.Error409Conflict("could not remove the SSH user: " + runErr.Error())
			}
			if _, err := q.DeleteServerSSHUser(ctx, db.DeleteServerSSHUserParams{ServerID: srv.ID, TeamID: m.TeamID, Username: in.SSHUser}); err != nil {
				return actionErr(err, "could not delete the SSH user")
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "server.ssh_user_removed", "server", srv.ID, map[string]any{"user": in.SSHUser})
		})
		return nil, err
	})

	type sshKeyAddOut struct{ Body ServerSSHKeyOut }
	huma.Register(api, huma.Operation{OperationID: "add-server-ssh-key", Method: http.MethodPost, Path: base + "/ssh-keys",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Give a public key SSH access to this server as a unix user (root or a Hosbah-created login): the key joins " +
			"the team's library (or is matched by fingerprint) and is installed. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body struct {
			Name      string `json:"name,omitempty" maxLength:"80"`
			PublicKey string `json:"public_key" minLength:"1" maxLength:"8192"`
			User      string `json:"user" minLength:"1" maxLength:"32" doc:"root or a login from this server's SSH users."`
		}
	}) (*sshKeyAddOut, error) {
		out := &sshKeyAddOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := enrolledServer(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if in.Body.User != "root" {
				if _, err := q.GetServerSSHUser(ctx, db.GetServerSSHUserParams{ServerID: srv.ID, TeamID: m.TeamID, Username: in.Body.User}); err != nil {
					return huma.Error422UnprocessableEntity("user: root or one of this server's SSH users")
				}
			}
			name := strings.TrimSpace(in.Body.Name)
			if name == "" {
				name = in.Body.User + "@" + srv.Name
			}
			key, err := libraryKey(ctx, q, m, id, name, in.Body.PublicKey)
			if err != nil {
				return err
			}
			if err := d.installKey(ctx, q, m, srv, key, in.Body.User); err != nil {
				return err
			}
			now := time.Now()
			out.Body = ServerSSHKeyOut{KeyID: key.ID, Name: key.Name, KeyType: key.KeyType, Fingerprint: key.Fingerprint,
				UnixUser: in.Body.User, Status: "installed", InstalledAt: &now}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "ssh_key.install", "server", srv.ID,
				map[string]any{"key": key.ID, "name": key.Name, "user": in.Body.User})
		})
		return out, err
	})
}

// libraryKey returns the team's library key with publicKey's fingerprint, adding it under name if
// the library does not have it yet.
func libraryKey(ctx context.Context, q *db.Queries, m *tenancy.Membership, id *auth.Identity, name, publicKey string) (db.SshKey, error) {
	keyType, _, perr := agentproto.ParseAuthorizedKey(publicKey)
	if perr != nil {
		return db.SshKey{}, huma.Error422UnprocessableEntity("public_key: " + perr.Error())
	}
	fp := agentproto.Fingerprint(publicKey)
	if k, err := q.GetTeamSSHKeyByFingerprint(ctx, db.GetTeamSSHKeyByFingerprintParams{TeamID: m.TeamID, Fingerprint: fp}); err == nil {
		return k, nil
	} else if !isNoRows(err) {
		return db.SshKey{}, actionErr(err, "could not read the SSH key library")
	}
	k, err := q.InsertTeamSSHKey(ctx, db.InsertTeamSSHKeyParams{TeamID: m.TeamID, Name: name, KeyType: keyType,
		PublicKey: strings.TrimSpace(publicKey), Fingerprint: fp, CreatedBy: id.ID})
	if err != nil {
		return db.SshKey{}, actionErr(err, "could not add the SSH key (a key with that name may already be in the library)")
	}
	return k, tenancy.Record(ctx, q, m.TeamID, id.ID, "ssh_key.added", "ssh_key", k.ID, map[string]any{"name": k.Name, "fingerprint": k.Fingerprint})
}

// installKey runs sshkey.install for user and records it in ssh_key_installs.
func (d *deps) installKey(ctx context.Context, q *db.Queries, m *tenancy.Membership, srv db.Server, key db.SshKey, user string) error {
	p := agentproto.SSHKeyInstallParams{User: user, PublicKey: key.PublicKey, Fingerprint: key.Fingerprint}
	if err := p.Validate(); err != nil {
		return huma.Error422UnprocessableEntity(err.Error())
	}
	var res agentproto.SSHKeyInstallResult
	status, opErr := "installed", ""
	var installedAt *time.Time
	if runErr := d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindSSHKeyInstall, p, &res); runErr != nil {
		status, opErr = "failed", runErr.Error()
	} else {
		now := time.Now()
		installedAt = &now
	}
	if _, err := q.UpsertSSHKeyInstall(ctx, db.UpsertSSHKeyInstallParams{TeamID: m.TeamID, SshKeyID: key.ID, ServerID: srv.ID,
		UnixUser: user, Status: status, LastError: opErr, InstalledAt: installedAt}); err != nil {
		return actionErr(err, "could not record the key install")
	}
	if status == "failed" {
		return huma.Error409Conflict("could not install the key: " + opErr)
	}
	return nil
}
