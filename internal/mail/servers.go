package mail

import (
	"context"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// MailServer is a team mail server as the API shows it. Never a secret.
type MailServer struct {
	ID        string  `json:"id"`
	ServerID  string  `json:"server_id"`
	Hostname  string  `json:"hostname"`
	AdminPort int64   `json:"admin_port"`
	Status    string  `json:"status" enum:"new,installing,running,failed,removing"`
	Error     *string `json:"error"`
}

func serverView(row db.MailServer) MailServer {
	return MailServer{ID: row.ID, ServerID: row.ServerID, Hostname: row.Hostname, AdminPort: row.AdminPort, Status: row.Status, Error: row.Error}
}

// ListServers is the team's mail servers.
func ListServers(ctx context.Context, q *db.Queries, teamID string) ([]MailServer, error) {
	rows, err := q.ListMailServersOfTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	out := make([]MailServer, 0, len(rows))
	for _, r := range rows {
		out = append(out, serverView(r))
	}
	return out, nil
}

// ServerByRef resolves a mail server by id within the team.
func ServerByRef(ctx context.Context, q *db.Queries, teamID, ref string) (db.MailServer, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.MailServer{}, invalid("a mail server id is required")
	}
	row, err := q.MailServerInTeam(ctx, db.MailServerInTeamParams{ID: ref, TeamID: teamID})
	if err != nil {
		return db.MailServer{}, notFound("mail server")
	}
	return row, nil
}

// InstallInput is what the setup wizard sends.
type InstallInput struct {
	Server   string // server id or name
	Hostname string // the mail server's own HELO/PTR hostname
}

const defaultAdminPort = 8080

// InstallServer inserts the row (status installing) and queues mail.install in the background. Only
// one mail server per team server.
func (s *Service) InstallServer(ctx context.Context, q *db.Queries, teamID string, actor Actor, in InstallInput) (MailServer, error) {
	hostname := strings.ToLower(strings.TrimSpace(in.Hostname))
	if !agentproto.ValidMailHostname(hostname) {
		return MailServer{}, invalid("hostname is not a valid DNS name")
	}
	srv, err := q.ServerInTeamByRef(ctx, db.ServerInTeamByRefParams{TeamID: teamID, Ref: in.Server})
	if err != nil {
		return MailServer{}, notFound("server")
	}
	if _, err := q.MailServerByHostnameInTeam(ctx, db.MailServerByHostnameInTeamParams{TeamID: teamID, Hostname: hostname}); err == nil {
		return MailServer{}, conflict("a mail server named " + hostname + " already exists")
	}
	dek, err := s.teamDEK(ctx, q, teamID)
	if err != nil {
		return MailServer{}, err
	}
	id := newUUID()
	secret := randPassword(28)
	sealed, err := crypto.Seal(dek, teamID, adminSealContext(id), []byte(secret))
	if err != nil {
		return MailServer{}, err
	}
	row, err := q.InsertMailServer(ctx, db.InsertMailServerParams{ID: id, TeamID: teamID, ServerID: srv.ID, Hostname: hostname, Status: "installing", CreatedBy: actor.UserID})
	if err != nil {
		return MailServer{}, err
	}
	// Record the admin secret right away (before the op ever runs) so a resumed install always sends
	// the same credential, never a fresh one, and the container's password never silently changes.
	if err := q.SetMailServerAdminSecret(ctx, db.SetMailServerAdminSecretParams{ID: row.ID, TeamID: teamID, AdminSealed: &sealed}); err != nil {
		return MailServer{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "mailserver.install", "mail_server", row.ID,
		auditExtra(actor, map[string]any{"hostname": hostname, "server": srv.ID})); err != nil {
		return MailServer{}, err
	}
	row.AdminSealed = &sealed
	s.spawn(func(bg context.Context) { s.install(bg, teamID, row, secret) })
	return serverView(row), nil
}

func (s *Service) install(ctx context.Context, teamID string, row db.MailServer, secret string) {
	container := agentproto.MailContainerName(row.ID)
	volume := agentproto.MailVolumeName(row.ID)
	var res agentproto.MailInstallResult
	err := s.withServer(ctx, teamID, row.ServerID, func(agentKey string, _ []byte) (any, string, error) {
		box, err := agentproto.SealBox(agentKey, agentproto.DBSecretContext(row.ID, "admin"), mustJSON(map[string]string{"password": secret}))
		if err != nil {
			return nil, "", err
		}
		p := agentproto.MailInstallParams{
			MailServerID: row.ID, Hostname: row.Hostname, Container: container, Volume: volume,
			Image: agentproto.MailImage, AdminPort: defaultAdminPort, AdminSecret: box,
		}
		return p, opID(row.ID, "install"), nil
	}, agentproto.KindMailInstall, &res)
	_ = s.sys(ctx, teamID, func(q *db.Queries) error {
		if err != nil {
			return q.SetMailServerStatus(ctx, db.SetMailServerStatusParams{ID: row.ID, TeamID: teamID, Status: "failed", Error: strptr(err.Error())})
		}
		c := res.Container
		return q.SetMailServerInstalled(ctx, db.SetMailServerInstalledParams{ID: row.ID, TeamID: teamID, Container: &c, AdminSealed: row.AdminSealed})
	})
}

// RemoveServer removes a mail server and every domain, mailbox and alias hosted on it (best effort
// against the agent; the rows are always removed).
func (s *Service) RemoveServer(ctx context.Context, q *db.Queries, teamID string, actor Actor, row db.MailServer) error {
	if _, err := q.DeleteMailServerInTeam(ctx, db.DeleteMailServerInTeamParams{ID: row.ID, TeamID: teamID}); err != nil {
		return err
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "mailserver.remove", "mail_server", row.ID, auditExtra(actor, nil))
}

// adminSecret opens a mail server's admin password (team-sealed) for the caller side, when a
// resolved server op is built (e.g. resending mail.install after a resumed row).
func (s *Service) adminSecret(ctx context.Context, q *db.Queries, teamID string, row db.MailServer) (string, error) {
	if row.AdminSealed == nil {
		return "", conflict("this mail server has no admin credential yet")
	}
	dek, err := s.teamDEK(ctx, q, teamID)
	if err != nil {
		return "", err
	}
	pt, err := crypto.Open(dek, teamID, adminSealContext(row.ID), *row.AdminSealed)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
