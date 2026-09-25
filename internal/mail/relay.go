package mail

import (
	"context"
	"crypto/rand"
	"math/big"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// MH-577 I6: an SMTP relay credential dedicated to one app — a Stalwart "individual" principal
// created the same way as a mailbox (mail.mailbox op), used only to authenticate outgoing SMTP,
// never to receive mail. Its generated password is sealed with the team DEK the same way a mailbox
// password is, and shown to the caller exactly once, at creation or rotation. "Attach to app" is a
// separate, explicit action that opens the team-sealed password (Hosbah's own control plane can —
// the constraint is that a plaintext secret never crosses an agent operation unsealed, not that
// Hosbah's own sealed store can never be read back for its own purposes) and re-seals it into the
// app's own env vars through deploy.Service.SetEnv, the exact mechanism internal/dbsvc uses to wire
// a shared database credential into an app's environment.

// Relay is an SMTP relay credential as the API shows it. Never carries the password after creation
// or rotation.
type Relay struct {
	ID           string  `json:"id"`
	MailDomainID string  `json:"mail_domain_id"`
	AppID        string  `json:"app_id"`
	LocalPart    string  `json:"local_part"`
	Email        string  `json:"email"`
	Attached     bool    `json:"attached" doc:"Whether mail_relay_attach has pushed this credential's SMTP_* env vars to its app."`
	Status       string  `json:"status" enum:"new,creating,ready,failed,removing"`
	Error        *string `json:"error"`
}

// RelayCreated is AddRelay's answer: the credential plus its generated password, shown once.
type RelayCreated struct {
	Relay    Relay  `json:"relay"`
	Password string `json:"password" doc:"Shown once. Hosbah never shows it again — rotate it to get a new one, or use the attach action to push it into the app's env directly."`
}

func relayView(row db.MailRelayCredential, domain string) Relay {
	return Relay{
		ID: row.ID, MailDomainID: row.MailDomainID, AppID: row.AppID, LocalPart: row.LocalPart,
		Email: row.LocalPart + "@" + domain, Attached: row.Attached, Status: row.Status, Error: row.Error,
	}
}

// ListRelays is a domain's SMTP relay credentials.
func ListRelays(ctx context.Context, q *db.Queries, teamID string, dom db.MailDomain) ([]Relay, error) {
	rows, err := q.ListMailRelayCredentialsOfDomain(ctx, db.ListMailRelayCredentialsOfDomainParams{MailDomainID: dom.ID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	out := make([]Relay, 0, len(rows))
	for _, r := range rows {
		out = append(out, relayView(r, dom.Domain))
	}
	return out, nil
}

// RelayByRef resolves a relay credential by id within the team.
func RelayByRef(ctx context.Context, q *db.Queries, teamID, ref string) (db.MailRelayCredential, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.MailRelayCredential{}, invalid("a relay credential id is required")
	}
	row, err := q.MailRelayCredentialInTeam(ctx, db.MailRelayCredentialInTeamParams{ID: ref, TeamID: teamID})
	if err != nil {
		return db.MailRelayCredential{}, notFound("relay credential")
	}
	return row, nil
}

const (
	relayLocalAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	relayQuotaMB       = 16 // send-only: a small fixed footprint, never surfaced or configurable
	relaySMTPPort      = "587"
)

func randRelayLocalPart() string {
	b := make([]byte, 10)
	for i := range b {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(relayLocalAlphabet))))
		b[i] = relayLocalAlphabet[k.Int64()]
	}
	return "relay-" + string(b)
}

func relaySealContext(id string) string { return "mailrelay:" + id }

// smtpEnvVars is the fixed set of env vars an "attach" or a rotation of an attached credential
// pushes to the app: SMTP_HOST/PORT point at the mail server itself, SMTP_USERNAME/PASSWORD/FROM at
// this one relay credential.
func smtpEnvVars(hostname, email, password string) [][2]string {
	return [][2]string{
		{"SMTP_HOST", hostname}, {"SMTP_PORT", relaySMTPPort},
		{"SMTP_USERNAME", email}, {"SMTP_PASSWORD", password}, {"SMTP_FROM", email},
	}
}

// smtpEnvNames is the same set of names, for removal.
func smtpEnvNames() []string { return []string{"SMTP_HOST", "SMTP_PORT", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM"} }

// AddRelay creates a dedicated sending-only mailbox for app a, seals its generated password with the
// team DEK, and returns it once.
func (s *Service) AddRelay(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.MailServer, dom db.MailDomain, a db.App) (RelayCreated, error) {
	if dom.Status != "active" {
		return RelayCreated{}, conflict("the domain is not active yet")
	}
	dek, err := s.teamDEK(ctx, q, teamID)
	if err != nil {
		return RelayCreated{}, err
	}
	id := newUUID()
	localPart := randRelayLocalPart()
	password := randPassword(24)
	sealed, err := crypto.Seal(dek, teamID, relaySealContext(id), []byte(password))
	if err != nil {
		return RelayCreated{}, err
	}
	row, err := q.InsertMailRelayCredential(ctx, db.InsertMailRelayCredentialParams{
		ID: id, TeamID: teamID, MailDomainID: dom.ID, AppID: a.ID, LocalPart: localPart, PasswordSealed: sealed, Status: "creating", CreatedBy: actor.UserID,
	})
	if err != nil {
		return RelayCreated{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "mailrelay.add", "mail_relay_credential", row.ID,
		auditExtra(actor, map[string]any{"app": a.ID, "email": localPart + "@" + dom.Domain})); err != nil {
		return RelayCreated{}, err
	}
	s.spawn(func(bg context.Context) { s.upsertRelay(bg, teamID, srv, dom, row, password, "create") })
	return RelayCreated{Relay: relayView(row, dom.Domain), Password: password}, nil
}

func (s *Service) upsertRelay(ctx context.Context, teamID string, srv db.MailServer, dom db.MailDomain, row db.MailRelayCredential, password, action string) {
	email := row.LocalPart + "@" + dom.Domain
	err := s.withServer(ctx, teamID, srv.ServerID, func(agentKey string, dek []byte) (any, string, error) {
		adminBox, err := s.reseal(ctx, teamID, dek, agentKey, srv)
		if err != nil {
			return nil, "", err
		}
		p := agentproto.MailboxParams{MailServerID: srv.ID, AdminPort: int(srv.AdminPort), AdminSecret: adminBox, Email: email, QuotaMB: relayQuotaMB, Action: action}
		if password != "" {
			box, err := agentproto.SealBox(agentKey, agentproto.DBSecretContext(srv.ID, "mailrelay:"+email), mustJSON(map[string]string{"password": password}))
			if err != nil {
				return nil, "", err
			}
			p.PasswordSecret = box
		}
		step := "create"
		if action == "update" {
			step = "update:" + row.PasswordSealed // vary the op id when the credential rotates
		}
		return p, opID(row.ID, step), nil
	}, agentproto.KindMailbox, nil)
	status, errStr := "ready", (*string)(nil)
	if err != nil {
		status, errStr = "failed", strptr(err.Error())
	}
	_ = s.sys(ctx, teamID, func(q *db.Queries) error {
		return q.SetMailRelayCredentialStatus(ctx, db.SetMailRelayCredentialStatusParams{ID: row.ID, TeamID: teamID, Status: status, Error: errStr})
	})
}

// RotateRelay generates a new password, seals it, queues mail.mailbox update, and — when the
// credential is already attached to its app — re-pushes SMTP_PASSWORD (and the rest of the fixed
// set) immediately, so the app's env never quietly falls out of sync with the live credential.
// Returned once.
func (s *Service) RotateRelay(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.MailServer, dom db.MailDomain, a db.App, row db.MailRelayCredential) (string, error) {
	dek, err := s.teamDEK(ctx, q, teamID)
	if err != nil {
		return "", err
	}
	password := randPassword(24)
	sealed, err := crypto.Seal(dek, teamID, relaySealContext(row.ID), []byte(password))
	if err != nil {
		return "", err
	}
	if err := q.SetMailRelayCredentialPasswordSealed(ctx, db.SetMailRelayCredentialPasswordSealedParams{ID: row.ID, TeamID: teamID, PasswordSealed: sealed}); err != nil {
		return "", err
	}
	row.PasswordSealed = sealed
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "mailrelay.rotate", "mail_relay_credential", row.ID, auditExtra(actor, nil)); err != nil {
		return "", err
	}
	if row.Attached {
		email := row.LocalPart + "@" + dom.Domain
		for _, kv := range smtpEnvVars(srv.Hostname, email, password) {
			if err := s.deploy.SetEnv(ctx, q, teamID, actor, a, kv[0], kv[1]); err != nil {
				return "", err
			}
		}
	}
	s.spawn(func(bg context.Context) { s.upsertRelay(bg, teamID, srv, dom, row, password, "update") })
	return password, nil
}

// relaySecret opens a relay credential's password (team-sealed) for AttachRelay's own use; it never
// leaves this package in plaintext.
func (s *Service) relaySecret(ctx context.Context, q *db.Queries, teamID string, row db.MailRelayCredential) (string, error) {
	dek, err := s.teamDEK(ctx, q, teamID)
	if err != nil {
		return "", err
	}
	pt, err := crypto.Open(dek, teamID, relaySealContext(row.ID), row.PasswordSealed)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// AttachRelay sets SMTP_HOST/SMTP_PORT/SMTP_USERNAME/SMTP_PASSWORD/SMTP_FROM on a's env from this
// relay credential, through the app's normal env mechanism (each value sealed again, to the app's
// own env context, by deploy.Service.SetEnv — the credential's plaintext password never appears in
// a response or a log, only in the sealed env row).
func (s *Service) AttachRelay(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.MailServer, dom db.MailDomain, a db.App, row db.MailRelayCredential) error {
	if row.Status != "ready" {
		return conflict("the relay credential is not ready yet")
	}
	password, err := s.relaySecret(ctx, q, teamID, row)
	if err != nil {
		return err
	}
	email := row.LocalPart + "@" + dom.Domain
	for _, kv := range smtpEnvVars(srv.Hostname, email, password) {
		if err := s.deploy.SetEnv(ctx, q, teamID, actor, a, kv[0], kv[1]); err != nil {
			return err
		}
	}
	if err := q.SetMailRelayCredentialAttached(ctx, db.SetMailRelayCredentialAttachedParams{ID: row.ID, TeamID: teamID, Attached: true}); err != nil {
		return err
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "mailrelay.attach", "mail_relay_credential", row.ID, auditExtra(actor, map[string]any{"app": a.ID}))
}

// RevokeRelay removes the row (best effort against the agent) and, when it was attached, best-effort
// unsets its SMTP_* env vars from the app so a dead credential is never left configured.
func (s *Service) RevokeRelay(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.MailServer, dom db.MailDomain, a db.App, row db.MailRelayCredential) error {
	if _, err := q.DeleteMailRelayCredentialInTeam(ctx, db.DeleteMailRelayCredentialInTeamParams{ID: row.ID, TeamID: teamID}); err != nil {
		return err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "mailrelay.revoke", "mail_relay_credential", row.ID, auditExtra(actor, nil)); err != nil {
		return err
	}
	if row.Attached {
		for _, name := range smtpEnvNames() {
			_ = s.deploy.UnsetEnv(ctx, q, teamID, actor, a, name)
		}
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
