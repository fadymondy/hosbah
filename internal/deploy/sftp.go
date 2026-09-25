package deploy

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"math/big"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// SFTP users: a chrooted, SFTP-only system account on a server (internal-sftp, no shell), so a
// team can hand out upload/download access with any SFTP client without sharing root. See
// internal/agentproto/sftp.go and internal/agent/sftp*.go for the agent-side half of this.

// sftpTimeout bounds waiting for the agent to create, change or remove an sftp user.
var sftpTimeout = 2 * time.Minute

// SFTPUserInput creates or changes an sftp user. Exactly one of PublicKeys or GeneratePassword
// gives it a way in: paste a key (or several), or have Hosbah generate a password shown once.
type SFTPUserInput struct {
	Username         string   // create only
	PublicKeys       []string // OpenSSH public keys, one per authorized_keys line
	GeneratePassword bool     // create only: mint a random password, sealed at rest, returned once
	RootDir          string
	ReadOnly         *bool // default false
}

// CreateSFTPUser adds a chrooted sftp user and applies it on its server. The second return value
// is the generated password in the clear — empty unless in.GeneratePassword was set — the only
// place it is ever visible; the caller must hand it to the operator now and never log it.
func (s *Service) CreateSFTPUser(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, in SFTPUserInput) (db.SftpUser, string, error) {
	if srv.Status != "enrolled" || srv.AgentPublicKey == nil {
		return db.SftpUser{}, "", conflict("that server's agent has not enrolled yet")
	}
	username := strings.TrimSpace(in.Username)
	keys := cleanSSHKeys(in.PublicKeys)
	if !in.GeneratePassword && len(keys) == 0 {
		return db.SftpUser{}, "", invalid("give at least one public key, or have Hosbah generate a password")
	}
	rootDir := strings.TrimSpace(in.RootDir)
	readOnly := boolOr(in.ReadOnly, false)

	row, err := q.CreateSFTPUser(ctx, db.CreateSFTPUserParams{TeamID: teamID, ServerID: srv.ID, Username: username,
		PublicKeys: strings.Join(keys, "\n"), PasswordSealed: nil, RootDir: rootDir, ReadOnly: readOnly,
		Status: "pending", CreatedBy: actor.UserID})
	if err != nil {
		if strings.Contains(err.Error(), "sftp_users_server_username_key") {
			return db.SftpUser{}, "", conflict("this server already has an sftp user named " + username)
		}
		if strings.Contains(err.Error(), "sftp_users_username_check") {
			return db.SftpUser{}, "", invalid("username must be lowercase, start with a letter or underscore, and be 1-32 characters")
		}
		if strings.Contains(err.Error(), "sftp_users_root_dir_check") {
			return db.SftpUser{}, "", invalid("root dir must be an absolute path")
		}
		return db.SftpUser{}, "", err
	}

	var plain string
	if in.GeneratePassword {
		plain, err = generateSFTPPassword()
		if err != nil {
			return db.SftpUser{}, "", err
		}
		dek, err := s.ops.TeamDEK(ctx, q, teamID)
		if err != nil {
			return db.SftpUser{}, "", err
		}
		sealed, err := crypto.Seal(dek, teamID, SFTPPasswordContext(row.ID), []byte(plain))
		if err != nil {
			return db.SftpUser{}, "", err
		}
		row, err = q.UpdateSFTPUser(ctx, db.UpdateSFTPUserParams{ID: row.ID, TeamID: teamID, PublicKeys: row.PublicKeys,
			PasswordSealed: &sealed, RootDir: row.RootDir, ReadOnly: row.ReadOnly, Status: row.Status})
		if err != nil {
			return db.SftpUser{}, "", err
		}
	}

	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "sftp_user.created", "server", srv.ID,
		actor.audit(map[string]any{"username": username, "root_dir": rootDir, "read_only": readOnly})); err != nil {
		return db.SftpUser{}, "", err
	}
	if err := s.enqueueSFTPUser(ctx, q, teamID, actor, srv, row, "create", keys, plain); err != nil {
		return db.SftpUser{}, "", err
	}
	return row, plain, nil
}

// UpdateSFTPUser changes an sftp user's keys, password, root dir or read-only flag and re-applies
// it. The username itself never changes (it is the chroot's home and the system account's name).
func (s *Service) UpdateSFTPUser(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, u db.SftpUser, in SFTPUserInput) (db.SftpUser, string, error) {
	if srv.Status != "enrolled" || srv.AgentPublicKey == nil {
		return db.SftpUser{}, "", conflict("that server's agent has not enrolled yet")
	}
	keys := u.PublicKeys
	if in.PublicKeys != nil {
		keys = strings.Join(cleanSSHKeys(in.PublicKeys), "\n")
	}
	rootDir := orDefault(in.RootDir, u.RootDir)
	readOnly := boolOr(in.ReadOnly, u.ReadOnly)

	sealed := u.PasswordSealed
	var plain string
	if in.GeneratePassword {
		var err error
		plain, err = generateSFTPPassword()
		if err != nil {
			return db.SftpUser{}, "", err
		}
		dek, err := s.ops.TeamDEK(ctx, q, teamID)
		if err != nil {
			return db.SftpUser{}, "", err
		}
		sv, serr := crypto.Seal(dek, teamID, SFTPPasswordContext(u.ID), []byte(plain))
		if serr != nil {
			return db.SftpUser{}, "", serr
		}
		sealed = &sv
	}
	if strings.TrimSpace(keys) == "" && sealed == nil {
		return db.SftpUser{}, "", invalid("an sftp user needs at least one public key or a password")
	}

	row, err := q.UpdateSFTPUser(ctx, db.UpdateSFTPUserParams{ID: u.ID, TeamID: teamID, PublicKeys: keys,
		PasswordSealed: sealed, RootDir: rootDir, ReadOnly: readOnly, Status: "pending"})
	if err != nil {
		if strings.Contains(err.Error(), "sftp_users_root_dir_check") {
			return db.SftpUser{}, "", invalid("root dir must be an absolute path")
		}
		return db.SftpUser{}, "", notFound("sftp user")
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "sftp_user.updated", "server", srv.ID,
		actor.audit(map[string]any{"username": row.Username, "root_dir": rootDir, "read_only": readOnly})); err != nil {
		return db.SftpUser{}, "", err
	}
	splitKeys := []string{}
	if row.PublicKeys != "" {
		splitKeys = strings.Split(row.PublicKeys, "\n")
	}
	if err := s.enqueueSFTPUser(ctx, q, teamID, actor, srv, row, "update", splitKeys, plain); err != nil {
		return db.SftpUser{}, "", err
	}
	return row, plain, nil
}

// DeleteSFTPUser removes the system account on its server and forgets it. RootDir is never touched.
func (s *Service) DeleteSFTPUser(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, u db.SftpUser) error {
	if srv.AgentPublicKey != nil {
		raw, _ := json.Marshal(agentproto.SFTPUserParams{ID: u.ID, Action: "delete", Username: u.Username})
		if _, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: srv.ID, Kind: agentproto.KindSFTPUser, Params: raw, CreatedBy: actor.UserID}); err != nil {
			return err
		}
	}
	if n, err := q.DeleteSFTPUserInTeam(ctx, db.DeleteSFTPUserInTeamParams{ID: u.ID, TeamID: teamID}); err != nil || n == 0 {
		return notFound("sftp user")
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "sftp_user.deleted", "server", srv.ID,
		actor.audit(map[string]any{"username": u.Username}))
}

// SFTPUserByRef resolves one of a server's sftp users by id or username.
func SFTPUserByRef(ctx context.Context, q *db.Queries, teamID, serverID, ref string) (db.SftpUser, error) {
	u, err := q.SFTPUserInTeamByRef(ctx, db.SFTPUserInTeamByRefParams{TeamID: teamID, ServerID: serverID, Ref: strings.TrimSpace(ref)})
	if err != nil {
		return db.SftpUser{}, notFound("sftp user")
	}
	return u, nil
}

// SFTPPasswordContext binds the at-rest seal (the team DEK) to the sftp user row whose password
// it holds — distinct from agentproto.SFTPSecretContext, which binds the box re-sealed to the
// target agent's own key inside the signed operation.
func SFTPPasswordContext(id string) string { return "sftp-password:" + id }

// enqueueSFTPUser builds sftp.user's params (re-sealing a fresh password to the server's agent
// key, if there is one) and enqueues it, then settles the row's status in the background.
func (s *Service) enqueueSFTPUser(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, u db.SftpUser, action string, keys []string, plain string) error {
	var box string
	if plain != "" {
		b, err := agentproto.SealBox(*srv.AgentPublicKey, agentproto.SFTPSecretContext(u.ID), mustJSON(map[string]string{"password": plain}))
		if err != nil {
			return err
		}
		box = b
	}
	params := agentproto.SFTPUserParams{ID: u.ID, Action: action, Username: u.Username, PublicKeys: keys,
		PasswordSecret: box, RootDir: u.RootDir, ReadOnly: u.ReadOnly}
	if err := params.Validate(); err != nil {
		return err
	}
	raw, _ := json.Marshal(params)
	op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: srv.ID, Kind: agentproto.KindSFTPUser, Params: raw, CreatedBy: actor.UserID})
	if err != nil {
		return err
	}
	s.settleSFTPUser(teamID, u.ID, op.ID, srv.ID)
	return nil
}

// settleSFTPUser waits for the agent in the background and records the outcome.
func (s *Service) settleSFTPUser(teamID, userID, opID, serverID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), sftpTimeout)
		defer cancel()
		s.ops.Wake()
		done, err := s.ops.Await(ctx, opID, serverID, nil)
		status, msg := "active", (*string)(nil)
		switch {
		case err != nil:
			status, msg = "failed", strptr(err.Error())
		case done.Status != "ok":
			status, msg = "failed", strptr(orDefault(derefStr(done.Error), done.Status))
		}
		_ = tenancy.Tx(context.WithoutCancel(ctx), s.a.SQLDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
			return q.SetSFTPUserState(ctx, db.SetSFTPUserStateParams{ID: userID, TeamID: teamID, Status: status, Error: msg})
		})
	}()
}

// cleanSSHKeys trims blank lines out of a pasted key list (one or more keys, possibly one per
// textarea line).
func cleanSSHKeys(in []string) []string {
	out := make([]string, 0, len(in))
	for _, k := range in {
		k = strings.TrimSpace(k)
		if k != "" {
			out = append(out, k)
		}
	}
	return out
}

// generateSFTPPassword mints a 24-character password from an unambiguous alphabet — shown to the
// operator exactly once; only its sealed form is ever stored.
func generateSFTPPassword() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	out := make([]byte, 24)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out), nil
}
