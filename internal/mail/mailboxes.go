package mail

import (
	"context"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Mailbox is a mailbox as the API shows it. Never carries the password after creation.
type Mailbox struct {
	ID           string  `json:"id"`
	MailDomainID string  `json:"mail_domain_id"`
	LocalPart    string  `json:"local_part"`
	Email        string  `json:"email"`
	QuotaMB      int64   `json:"quota_mb"`
	Status       string  `json:"status" enum:"new,creating,ready,failed,removing"`
	Error        *string `json:"error"`
}

// MailboxCreated is CreateMailbox's answer: the mailbox plus its generated password, shown once.
type MailboxCreated struct {
	Mailbox  Mailbox `json:"mailbox"`
	Password string  `json:"password" doc:"Shown once. Hosbah never stores or shows it again — reset it to get a new one."`
}

func mailboxView(row db.Mailbox, domain string) Mailbox {
	return Mailbox{ID: row.ID, MailDomainID: row.MailDomainID, LocalPart: row.LocalPart, Email: row.LocalPart + "@" + domain, QuotaMB: row.QuotaMb, Status: row.Status, Error: row.Error}
}

// ListMailboxes is a domain's mailboxes.
func ListMailboxes(ctx context.Context, q *db.Queries, teamID string, dom db.MailDomain) ([]Mailbox, error) {
	rows, err := q.ListMailboxesOfDomain(ctx, db.ListMailboxesOfDomainParams{MailDomainID: dom.ID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	out := make([]Mailbox, 0, len(rows))
	for _, r := range rows {
		out = append(out, mailboxView(r, dom.Domain))
	}
	return out, nil
}

// MailboxByRef resolves a mailbox by id or local part within a domain.
func MailboxByRef(ctx context.Context, q *db.Queries, teamID string, dom db.MailDomain, ref string) (db.Mailbox, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Mailbox{}, invalid("a mailbox id or local part is required")
	}
	if agentproto.ValidID(ref) {
		if row, err := q.MailboxInTeam(ctx, db.MailboxInTeamParams{ID: ref, TeamID: teamID}); err == nil && row.MailDomainID == dom.ID {
			return row, nil
		}
	}
	local, _, _ := strings.Cut(strings.ToLower(ref), "@")
	row, err := q.MailboxByLocalPartInTeam(ctx, db.MailboxByLocalPartInTeamParams{TeamID: teamID, MailDomainID: dom.ID, LocalPart: local})
	if err != nil {
		return db.Mailbox{}, notFound("mailbox")
	}
	return row, nil
}

const defaultQuotaMB = 1024

// AddMailbox generates the password (never taken from the caller), inserts the row and queues
// mail.mailbox create. The generated password is returned exactly once.
func (s *Service) AddMailbox(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.MailServer, dom db.MailDomain, localPart string, quotaMB int64) (MailboxCreated, error) {
	localPart = strings.ToLower(strings.TrimSpace(localPart))
	if !agentproto.ValidMailLocalPart(localPart) {
		return MailboxCreated{}, invalid("local part is not valid")
	}
	if dom.Status != "active" {
		return MailboxCreated{}, conflict("the domain is not active yet")
	}
	if quotaMB <= 0 {
		quotaMB = defaultQuotaMB
	}
	if _, err := q.MailboxByLocalPartInTeam(ctx, db.MailboxByLocalPartInTeamParams{TeamID: teamID, MailDomainID: dom.ID, LocalPart: localPart}); err == nil {
		return MailboxCreated{}, conflict("mailbox " + localPart + "@" + dom.Domain + " already exists")
	}
	dek, err := s.teamDEK(ctx, q, teamID)
	if err != nil {
		return MailboxCreated{}, err
	}
	id := newUUID()
	password := randPassword(24)
	sealed, err := crypto.Seal(dek, teamID, mailboxSealContext(id), []byte(password))
	if err != nil {
		return MailboxCreated{}, err
	}
	row, err := q.InsertMailbox(ctx, db.InsertMailboxParams{ID: id, TeamID: teamID, MailDomainID: dom.ID, LocalPart: localPart, QuotaMb: quotaMB, PasswordSealed: sealed, Status: "creating", CreatedBy: actor.UserID})
	if err != nil {
		return MailboxCreated{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "mailbox.add", "mailbox", row.ID, auditExtra(actor, map[string]any{"email": localPart + "@" + dom.Domain})); err != nil {
		return MailboxCreated{}, err
	}
	s.spawn(func(bg context.Context) { s.upsertMailbox(bg, teamID, srv, dom, row, password, "create") })
	return MailboxCreated{Mailbox: mailboxView(row, dom.Domain), Password: password}, nil
}

// SetPassword generates a new password, seals it and queues mail.mailbox update. Returned once.
func (s *Service) SetPassword(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.MailServer, dom db.MailDomain, row db.Mailbox) (string, error) {
	dek, err := s.teamDEK(ctx, q, teamID)
	if err != nil {
		return "", err
	}
	password := randPassword(24)
	sealed, err := crypto.Seal(dek, teamID, mailboxSealContext(row.ID), []byte(password))
	if err != nil {
		return "", err
	}
	if err := q.SetMailboxStatus(ctx, db.SetMailboxStatusParams{ID: row.ID, TeamID: teamID, Status: "creating", Error: nil}); err != nil {
		return "", err
	}
	row.PasswordSealed = sealed
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "mailbox.set_password", "mailbox", row.ID, auditExtra(actor, nil)); err != nil {
		return "", err
	}
	s.spawn(func(bg context.Context) { s.upsertMailbox(bg, teamID, srv, dom, row, password, "update") })
	return password, nil
}

// SetQuota changes a mailbox's quota and queues mail.mailbox update (no password change).
func (s *Service) SetQuota(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.MailServer, dom db.MailDomain, row db.Mailbox, quotaMB int64) error {
	if quotaMB <= 0 {
		return invalid("quota_mb must be positive")
	}
	if err := q.UpdateMailboxQuota(ctx, db.UpdateMailboxQuotaParams{ID: row.ID, TeamID: teamID, QuotaMb: quotaMB}); err != nil {
		return err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "mailbox.set_quota", "mailbox", row.ID, auditExtra(actor, map[string]any{"quota_mb": quotaMB})); err != nil {
		return err
	}
	row.QuotaMb = quotaMB
	s.spawn(func(bg context.Context) { s.upsertMailbox(bg, teamID, srv, dom, row, "", "update") })
	return nil
}

func (s *Service) upsertMailbox(ctx context.Context, teamID string, srv db.MailServer, dom db.MailDomain, row db.Mailbox, password, action string) {
	email := row.LocalPart + "@" + dom.Domain
	err := s.withServer(ctx, teamID, srv.ServerID, func(agentKey string, dek []byte) (any, string, error) {
		adminBox, err := s.reseal(ctx, teamID, dek, agentKey, srv)
		if err != nil {
			return nil, "", err
		}
		p := agentproto.MailboxParams{MailServerID: srv.ID, AdminPort: int(srv.AdminPort), AdminSecret: adminBox, Email: email, QuotaMB: int(row.QuotaMb), Action: action}
		if password != "" {
			box, err := agentproto.SealBox(agentKey, agentproto.DBSecretContext(srv.ID, "mailbox:"+email), mustJSON(map[string]string{"password": password}))
			if err != nil {
				return nil, "", err
			}
			p.PasswordSecret = box
		}
		step := "create"
		if action == "update" {
			step = "update:" + row.PasswordSealed // vary the op id when the credential changes, so a
			// resumed request cannot silently resend a stale one; a quota-only update keeps a stable id.
			if password == "" {
				step = "update"
			}
		}
		return p, opID(row.ID, step), nil
	}, agentproto.KindMailbox, nil)
	status, errStr := "ready", (*string)(nil)
	if err != nil {
		status, errStr = "failed", strptr(err.Error())
	}
	_ = s.sys(ctx, teamID, func(q *db.Queries) error {
		return q.SetMailboxStatus(ctx, db.SetMailboxStatusParams{ID: row.ID, TeamID: teamID, Status: status, Error: errStr})
	})
}

// RemoveMailbox queues mail.mailbox delete and removes the row (best effort against the agent).
func (s *Service) RemoveMailbox(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.MailServer, dom db.MailDomain, row db.Mailbox) error {
	if _, err := q.DeleteMailboxInTeam(ctx, db.DeleteMailboxInTeamParams{ID: row.ID, TeamID: teamID}); err != nil {
		return err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "mailbox.remove", "mailbox", row.ID, auditExtra(actor, nil)); err != nil {
		return err
	}
	email := row.LocalPart + "@" + dom.Domain
	s.spawn(func(bg context.Context) {
		_ = s.withServer(bg, teamID, srv.ServerID, func(agentKey string, dek []byte) (any, string, error) {
			box, err := s.reseal(bg, teamID, dek, agentKey, srv)
			if err != nil {
				return nil, "", err
			}
			p := agentproto.MailboxParams{MailServerID: srv.ID, AdminPort: int(srv.AdminPort), AdminSecret: box, Email: email, Action: "delete"}
			return p, opID(row.ID, "delete"), nil
		}, agentproto.KindMailbox, nil)
	})
	return nil
}
