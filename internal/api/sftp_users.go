package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// SFTP users: a chrooted, SFTP-only system account on a server, so a team can hand out
// upload/download access with any SFTP client (FileZilla, WinSCP, ...) without sharing root.
// Writes need AbilityManageServers (owner or admin), like connecting a server or the file browser.

// SFTPUserOut is one sftp user, never its password: only a create or a password-rotating update
// returns the plaintext, once, in that call's own response.
type SFTPUserOut struct {
	ID          string    `json:"id"`
	ServerID    string    `json:"server_id"`
	ServerName  string    `json:"server_name,omitempty"`
	Username    string    `json:"username"`
	PublicKeys  []string  `json:"public_keys"`
	HasPassword bool      `json:"has_password"`
	RootDir     string    `json:"root_dir"`
	ReadOnly    bool      `json:"read_only"`
	Status      string    `json:"status" enum:"pending,active,failed,removing"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func splitSSHKeys(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{}
	}
	return strings.Split(s, "\n")
}

func toSFTPUserOut(u db.SftpUser, serverName string) SFTPUserOut {
	out := SFTPUserOut{ID: u.ID, ServerID: u.ServerID, ServerName: serverName, Username: u.Username, PublicKeys: splitSSHKeys(u.PublicKeys),
		HasPassword: u.PasswordSealed != nil, RootDir: u.RootDir, ReadOnly: u.ReadOnly, Status: u.Status, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt}
	if u.Error != nil {
		out.Error = *u.Error
	}
	return out
}

// SFTPUserPath addresses one sftp user of a server.
type SFTPUserPath struct {
	ServerPath
	SFTPUser string `path:"sftp_user" maxLength:"64" doc:"sftp user id or username"`
}

// SFTPUserWrite is what can be given when creating or changing an sftp user.
type SFTPUserWrite struct {
	PublicKeys       []string `json:"public_keys,omitempty" doc:"one or more OpenSSH public keys (ssh-ed25519, ssh-rsa, ...)"`
	GeneratePassword bool     `json:"generate_password,omitempty" doc:"have Hosbah mint a password, sealed at rest, returned once in this response"`
	RootDir          string   `json:"root_dir,omitempty" doc:"absolute path on the server this user is chrooted into"`
	ReadOnly         *bool    `json:"read_only,omitempty"`
}

func (d *deps) registerSFTPUsers(api huma.API) {
	tags := []string{"SFTP users"}
	base := "/api/teams/{team}/servers/{server}/sftp-users"

	type sftpListOut struct {
		Body struct {
			SFTPUsers []SFTPUserOut `json:"sftp_users"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-sftp-users", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "This server's sftp users (owner or admin)",
	}, func(ctx context.Context, in *ServerPath) (*sftpListOut, error) {
		out := &sftpListOut{}
		out.Body.SFTPUsers = []SFTPUserOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			rows, err := q.ListSFTPUsersOfServer(ctx, db.ListSFTPUsersOfServerParams{ServerID: s.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not list sftp users")
			}
			for _, r := range rows {
				out.Body.SFTPUsers = append(out.Body.SFTPUsers, toSFTPUserOut(r, s.Name))
			}
			return nil
		})
		return out, err
	})

	type sftpOneOut struct {
		Body struct {
			SFTPUserOut
			Password string `json:"password,omitempty" doc:"shown once: not stored, not retrievable again"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "create-sftp-user", Method: http.MethodPost, Path: base,
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Add a chrooted sftp-only system user to a server (owner or admin): paste a public key, or ask Hosbah to " +
			"generate a password (returned once, in this response, and never again).",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body struct {
			Username string `json:"username" minLength:"1" maxLength:"32" doc:"lowercase, starts with a letter or underscore"`
			SFTPUserWrite
		}
	}) (*sftpOneOut, error) {
		out := &sftpOneOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			b := in.Body
			u, plain, err := d.deploys().CreateSFTPUser(ctx, q, m.TeamID, webActor(id), s, deploy.SFTPUserInput{Username: b.Username,
				PublicKeys: b.PublicKeys, GeneratePassword: b.GeneratePassword, RootDir: b.RootDir, ReadOnly: b.ReadOnly})
			if err != nil {
				return actionErr(err, "could not add the sftp user")
			}
			out.Body.SFTPUserOut = toSFTPUserOut(u, s.Name)
			out.Body.Password = plain
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "get-sftp-user", Method: http.MethodGet, Path: base + "/{sftp_user}", Tags: tags,
		Summary: "One sftp user (owner or admin)",
	}, func(ctx context.Context, in *SFTPUserPath) (*struct{ Body SFTPUserOut }, error) {
		out := &struct{ Body SFTPUserOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			u, err := deploy.SFTPUserByRef(ctx, q, m.TeamID, s.ID, in.SFTPUser)
			if err != nil {
				return errNotFound
			}
			out.Body = toSFTPUserOut(u, s.Name)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-sftp-user", Method: http.MethodPatch, Path: base + "/{sftp_user}", Tags: tags,
		Summary: "Change an sftp user's keys, password, root dir or read-only flag (owner or admin); applied at once. " +
			"The username itself cannot change.",
	}, func(ctx context.Context, in *struct {
		SFTPUserPath
		Body SFTPUserWrite
	}) (*sftpOneOut, error) {
		out := &sftpOneOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			u, err := deploy.SFTPUserByRef(ctx, q, m.TeamID, s.ID, in.SFTPUser)
			if err != nil {
				return errNotFound
			}
			b := in.Body
			var keys []string
			if b.PublicKeys != nil {
				keys = b.PublicKeys
			}
			nu, plain, err := d.deploys().UpdateSFTPUser(ctx, q, m.TeamID, webActor(id), s, u, deploy.SFTPUserInput{
				PublicKeys: keys, GeneratePassword: b.GeneratePassword, RootDir: b.RootDir, ReadOnly: b.ReadOnly})
			if err != nil {
				return actionErr(err, "could not change the sftp user")
			}
			out.Body.SFTPUserOut = toSFTPUserOut(nu, s.Name)
			out.Body.Password = plain
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-sftp-user", Method: http.MethodDelete, Path: base + "/{sftp_user}",
		DefaultStatus: http.StatusNoContent, Tags: tags, Summary: "Remove an sftp user from its server and Hosbah (owner or admin)",
	}, func(ctx context.Context, in *SFTPUserPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			u, err := deploy.SFTPUserByRef(ctx, q, m.TeamID, s.ID, in.SFTPUser)
			if err != nil {
				return errNotFound
			}
			return actionErr(d.deploys().DeleteSFTPUser(ctx, q, m.TeamID, webActor(id), s, u), "could not remove the sftp user")
		})
		return nil, err
	})
}
