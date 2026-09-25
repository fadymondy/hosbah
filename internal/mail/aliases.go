package mail

import (
	"context"
	"net/mail"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Alias is a mail alias or forward as the API shows it.
type Alias struct {
	ID           string   `json:"id"`
	MailDomainID string   `json:"mail_domain_id"`
	LocalPart    string   `json:"local_part"`
	Email        string   `json:"email"`
	Kind         string   `json:"kind" enum:"alias,forward"`
	Destinations []string `json:"destinations"`
	Status       string   `json:"status" enum:"new,creating,ready,failed,removing"`
	Error        *string  `json:"error"`
}

func aliasView(row db.MailAlias, domain string) Alias {
	return Alias{
		ID: row.ID, MailDomainID: row.MailDomainID, LocalPart: row.LocalPart, Email: row.LocalPart + "@" + domain,
		Kind: row.Kind, Destinations: parseDestinations(row.Destinations), Status: row.Status, Error: row.Error,
	}
}

// ListAliases is a domain's aliases and forwards.
func ListAliases(ctx context.Context, q *db.Queries, teamID string, dom db.MailDomain) ([]Alias, error) {
	rows, err := q.ListMailAliasesOfDomain(ctx, db.ListMailAliasesOfDomainParams{MailDomainID: dom.ID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	out := make([]Alias, 0, len(rows))
	for _, r := range rows {
		out = append(out, aliasView(r, dom.Domain))
	}
	return out, nil
}

// AliasByRef resolves an alias by id within the team.
func AliasByRef(ctx context.Context, q *db.Queries, teamID, ref string) (db.MailAlias, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.MailAlias{}, invalid("an alias id is required")
	}
	row, err := q.MailAliasInTeam(ctx, db.MailAliasInTeamParams{ID: ref, TeamID: teamID})
	if err != nil {
		return db.MailAlias{}, notFound("mail alias")
	}
	return row, nil
}

// validDestination reports whether a destination is a mailbox on the same domain (kind=alias) or an
// outside address (kind=forward): both are validated as a plain email address.
func validDestination(addr string) bool {
	a, err := mail.ParseAddress(addr)
	return err == nil && a.Address == strings.TrimSpace(addr) && !strings.ContainsAny(addr, "<>()")
}

// AddAlias inserts the row (status creating) and queues mail.alias create.
func (s *Service) AddAlias(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.MailServer, dom db.MailDomain, localPart, kind string, destinations []string) (Alias, error) {
	localPart = strings.ToLower(strings.TrimSpace(localPart))
	if !agentproto.ValidMailLocalPart(localPart) {
		return Alias{}, invalid("local part is not valid")
	}
	if kind != "alias" && kind != "forward" {
		return Alias{}, invalid("kind must be alias or forward")
	}
	if dom.Status != "active" {
		return Alias{}, conflict("the domain is not active yet")
	}
	if len(destinations) == 0 {
		return Alias{}, invalid("at least one destination is required")
	}
	clean := make([]string, 0, len(destinations))
	for _, d := range destinations {
		d = strings.ToLower(strings.TrimSpace(d))
		if !validDestination(d) {
			return Alias{}, invalid(d + " is not a valid email address")
		}
		clean = append(clean, d)
	}
	row, err := q.InsertMailAlias(ctx, db.InsertMailAliasParams{
		ID: newUUID(), TeamID: teamID, MailDomainID: dom.ID, LocalPart: localPart, Kind: kind,
		Destinations: destinationsJSON(clean), Status: "creating", CreatedBy: actor.UserID,
	})
	if err != nil {
		return Alias{}, conflict("an alias " + localPart + "@" + dom.Domain + " already exists")
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "mailalias.add", "mail_alias", row.ID, auditExtra(actor, map[string]any{"email": localPart + "@" + dom.Domain, "kind": kind})); err != nil {
		return Alias{}, err
	}
	s.spawn(func(bg context.Context) { s.upsertAlias(bg, teamID, srv, dom, row) })
	return aliasView(row, dom.Domain), nil
}

func (s *Service) upsertAlias(ctx context.Context, teamID string, srv db.MailServer, dom db.MailDomain, row db.MailAlias) {
	email := row.LocalPart + "@" + dom.Domain
	err := s.withServer(ctx, teamID, srv.ServerID, func(agentKey string, dek []byte) (any, string, error) {
		box, err := s.reseal(ctx, teamID, dek, agentKey, srv)
		if err != nil {
			return nil, "", err
		}
		p := agentproto.MailAliasParams{MailServerID: srv.ID, AdminPort: int(srv.AdminPort), AdminSecret: box, Email: email, Destinations: parseDestinations(row.Destinations), Action: "create"}
		return p, opID(row.ID, "create"), nil
	}, agentproto.KindMailAlias, nil)
	status, errStr := "ready", (*string)(nil)
	if err != nil {
		status, errStr = "failed", strptr(err.Error())
	}
	_ = s.sys(ctx, teamID, func(q *db.Queries) error {
		return q.SetMailAliasStatus(ctx, db.SetMailAliasStatusParams{ID: row.ID, TeamID: teamID, Status: status, Error: errStr})
	})
}

// RemoveAlias queues mail.alias delete and removes the row (best effort against the agent).
func (s *Service) RemoveAlias(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.MailServer, dom db.MailDomain, row db.MailAlias) error {
	if _, err := q.DeleteMailAliasInTeam(ctx, db.DeleteMailAliasInTeamParams{ID: row.ID, TeamID: teamID}); err != nil {
		return err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "mailalias.remove", "mail_alias", row.ID, auditExtra(actor, nil)); err != nil {
		return err
	}
	email := row.LocalPart + "@" + dom.Domain
	s.spawn(func(bg context.Context) {
		_ = s.withServer(bg, teamID, srv.ServerID, func(agentKey string, dek []byte) (any, string, error) {
			box, err := s.reseal(bg, teamID, dek, agentKey, srv)
			if err != nil {
				return nil, "", err
			}
			p := agentproto.MailAliasParams{MailServerID: srv.ID, AdminPort: int(srv.AdminPort), AdminSecret: box, Email: email, Action: "delete"}
			return p, opID(row.ID, "delete"), nil
		}, agentproto.KindMailAlias, nil)
	})
	return nil
}
