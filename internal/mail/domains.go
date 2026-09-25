package mail

import (
	"context"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Domain is a mail domain as the API shows it.
type Domain struct {
	ID            string  `json:"id"`
	MailServerID  string  `json:"mail_server_id"`
	Domain        string  `json:"domain"`
	DKIMSelector  *string `json:"dkim_selector"`
	DKIMPublicKey *string `json:"dkim_public_key"`
	Status        string  `json:"status" enum:"new,creating,active,failed,removing"`
	Error         *string `json:"error"`
}

func domainView(row db.MailDomain) Domain {
	return Domain{ID: row.ID, MailServerID: row.MailServerID, Domain: row.Domain, DKIMSelector: row.DkimSelector, DKIMPublicKey: row.DkimPublicKey, Status: row.Status, Error: row.Error}
}

// ListDomains is the team's mail domains, optionally filtered to one server.
func ListDomains(ctx context.Context, q *db.Queries, teamID string, mailServerID string) ([]Domain, error) {
	var rows []db.MailDomain
	var err error
	if mailServerID != "" {
		rows, err = q.ListMailDomainsOfServer(ctx, db.ListMailDomainsOfServerParams{MailServerID: mailServerID, TeamID: teamID})
	} else {
		rows, err = q.ListMailDomainsOfTeam(ctx, teamID)
	}
	if err != nil {
		return nil, err
	}
	out := make([]Domain, 0, len(rows))
	for _, r := range rows {
		out = append(out, domainView(r))
	}
	return out, nil
}

// DomainByRef resolves a mail domain by id or its domain name within the team.
func DomainByRef(ctx context.Context, q *db.Queries, teamID, ref string) (db.MailDomain, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.MailDomain{}, invalid("a domain id or name is required")
	}
	if agentproto.ValidID(ref) {
		if row, err := q.MailDomainInTeam(ctx, db.MailDomainInTeamParams{ID: ref, TeamID: teamID}); err == nil {
			return row, nil
		}
	}
	row, err := q.MailDomainByNameInTeam(ctx, db.MailDomainByNameInTeamParams{TeamID: teamID, Domain: strings.ToLower(ref)})
	if err != nil {
		return db.MailDomain{}, notFound("mail domain")
	}
	return row, nil
}

// AddDomain inserts the row (status creating) and queues mail.domain create in the background.
func (s *Service) AddDomain(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.MailServer, domain string) (Domain, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if !agentproto.ValidMailDomain(domain) {
		return Domain{}, invalid("domain is not a valid DNS name")
	}
	if srv.Status != "running" {
		return Domain{}, conflict("the mail server is not running yet")
	}
	if _, err := q.MailDomainByNameInTeam(ctx, db.MailDomainByNameInTeamParams{TeamID: teamID, Domain: domain}); err == nil {
		return Domain{}, conflict("domain " + domain + " already exists")
	}
	row, err := q.InsertMailDomain(ctx, db.InsertMailDomainParams{ID: newUUID(), TeamID: teamID, MailServerID: srv.ID, Domain: domain, Status: "creating", CreatedBy: actor.UserID})
	if err != nil {
		return Domain{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "maildomain.add", "mail_domain", row.ID, auditExtra(actor, map[string]any{"domain": domain})); err != nil {
		return Domain{}, err
	}
	s.spawn(func(bg context.Context) { s.createDomain(bg, teamID, srv, row) })
	return domainView(row), nil
}

func (s *Service) createDomain(ctx context.Context, teamID string, srv db.MailServer, row db.MailDomain) {
	var res agentproto.MailDomainResult
	err := s.withServer(ctx, teamID, srv.ServerID, func(agentKey string, dek []byte) (any, string, error) {
		box, err := s.reseal(ctx, teamID, dek, agentKey, srv)
		if err != nil {
			return nil, "", err
		}
		p := agentproto.MailDomainParams{MailServerID: srv.ID, AdminPort: int(srv.AdminPort), AdminSecret: box, Domain: row.Domain, Action: "create"}
		return p, opID(row.ID, "create"), nil
	}, agentproto.KindMailDomain, &res)
	_ = s.sys(ctx, teamID, func(q *db.Queries) error {
		if err != nil {
			return q.SetMailDomainStatus(ctx, db.SetMailDomainStatusParams{ID: row.ID, TeamID: teamID, Status: "failed", Error: strptr(err.Error())})
		}
		return q.SetMailDomainDKIM(ctx, db.SetMailDomainDKIMParams{ID: row.ID, TeamID: teamID, DkimSelector: strptr(res.DKIMSelector), DkimPublicKey: strptr(res.DKIMPublicKey)})
	})
}

// RemoveDomain queues mail.domain delete and removes the row once the agent (best effort) answers;
// the row is always removed so a dead server can never block cleanup.
func (s *Service) RemoveDomain(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.MailServer, row db.MailDomain) error {
	if _, err := q.DeleteMailDomainInTeam(ctx, db.DeleteMailDomainInTeamParams{ID: row.ID, TeamID: teamID}); err != nil {
		return err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "maildomain.remove", "mail_domain", row.ID, auditExtra(actor, nil)); err != nil {
		return err
	}
	s.spawn(func(bg context.Context) {
		_ = s.withServer(bg, teamID, srv.ServerID, func(agentKey string, dek []byte) (any, string, error) {
			box, err := s.reseal(bg, teamID, dek, agentKey, srv)
			if err != nil {
				return nil, "", err
			}
			p := agentproto.MailDomainParams{MailServerID: srv.ID, AdminPort: int(srv.AdminPort), AdminSecret: box, Domain: row.Domain, Action: "delete"}
			return p, opID(row.ID, "delete"), nil
		}, agentproto.KindMailDomain, nil)
	})
	return nil
}

// reseal opens the mail server's admin credential (team-sealed) and re-seals it to the target
// agent's own key, the way every other agent-op secret travels.
func (s *Service) reseal(ctx context.Context, teamID string, dek []byte, agentKey string, srv db.MailServer) (string, error) {
	var pt string
	err := s.sys(ctx, teamID, func(q *db.Queries) error {
		row, err := q.MailServerInTeam(ctx, db.MailServerInTeamParams{ID: srv.ID, TeamID: teamID})
		if err != nil {
			return err
		}
		p, err := s.adminSecret(ctx, q, teamID, row)
		pt = p
		return err
	})
	if err != nil {
		return "", err
	}
	return agentproto.SealBox(agentKey, agentproto.DBSecretContext(srv.ID, "admin"), mustJSON(map[string]string{"password": pt}))
}
