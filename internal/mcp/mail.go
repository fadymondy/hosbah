package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/mail"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// MailTools are the MH-577 team-mail-server tools. Like the database tools they call the same
// internal/mail actions as REST, so validation, roles, audit and "a mailbox password never leaves
// in a list" are one implementation. Installing a server, and every domain/mailbox/alias write,
// needs tenancy.AbilityManageMail (owner or admin), the same tier REST enforces.
func MailTools() []Tool {
	serverArg := str("Mail server id")
	domainArg := str("Mail domain name or id")
	mailboxArg := str("Mailbox local part (before the @) or id")
	aliasArg := str("Alias id")
	return []Tool{
		{
			Name: "mail_servers_list", Scope: scopes.Read,
			Description: "The team's mail servers (Stalwart): hostname, admin port and status.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				rows, err := mail.ListServers(ctx, env.Q, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list mail servers")
				}
				if len(rows) == 0 {
					return "No mail servers yet. mail_server_install runs one on a team server.", nil
				}
				var b strings.Builder
				for _, r := range rows {
					fmt.Fprintf(&b, "  %-28s %-10s admin port %d  (id %s)\n", r.Hostname, r.Status, r.AdminPort, r.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "mail_server_install", Scope: scopes.Write,
			Description: "Install a Stalwart mail server on one of the team's servers. hostname is its own HELO/PTR name (e.g. mail.example.com), distinct from any hosted domain.",
			InputSchema: schema([]string{"server", "hostname"}, map[string]any{
				"server": str("Server name or id to run the mail server on"), "hostname": str("The mail server's own DNS hostname"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMail); err != nil {
					return "", err
				}
				s, err := mailSvcOf(env)
				if err != nil {
					return "", err
				}
				row, err := s.InstallServer(ctx, env.Q, env.Caller.TeamID, mcpActor(env), mail.InstallInput{Server: args.Str("server"), Hostname: args.Str("hostname")})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Installing mail server %s (id %s) — status %s. Add a domain with mail_domain_add once it is running.", row.Hostname, row.ID, row.Status), nil
			},
		},
		{
			Name: "mail_server_remove", Scope: scopes.Write,
			Description: "Remove a mail server and every domain, mailbox and alias hosted on it.",
			InputSchema: schema([]string{"server"}, map[string]any{"server": serverArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMail); err != nil {
					return "", err
				}
				s, err := mailSvcOf(env)
				if err != nil {
					return "", err
				}
				row, err := mail.ServerByRef(ctx, env.Q, env.Caller.TeamID, args.Str("server"))
				if err != nil {
					return "", err
				}
				if err := s.RemoveServer(ctx, env.Q, env.Caller.TeamID, mcpActor(env), row); err != nil {
					return "", err
				}
				return "Removed mail server " + row.Hostname + ".", nil
			},
		},
		{
			Name: "mail_domains_list", Scope: scopes.Read,
			Description: "The team's mail domains, optionally on one mail server.",
			InputSchema: schema(nil, map[string]any{"server": str("Filter to one mail server's id (optional)")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				rows, err := mail.ListDomains(ctx, env.Q, env.Caller.TeamID, args.Str("server"))
				if err != nil {
					return "", errors.New("could not list mail domains")
				}
				if len(rows) == 0 {
					return "No mail domains yet. mail_domain_add adds one.", nil
				}
				var b strings.Builder
				for _, r := range rows {
					dkim := "no DKIM key yet"
					if r.DKIMSelector != nil {
						dkim = "DKIM " + *r.DKIMSelector
					}
					fmt.Fprintf(&b, "  %-28s %-10s %s  (id %s)\n", r.Domain, r.Status, dkim, r.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "mail_domain_add", Scope: scopes.Write,
			Description: "Add a domain to a mail server; Stalwart generates its DKIM key. See mail_dns_records for what to publish afterwards.",
			InputSchema: schema([]string{"server", "domain"}, map[string]any{"server": serverArg, "domain": str("The domain to host, e.g. example.com")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMail); err != nil {
					return "", err
				}
				s, err := mailSvcOf(env)
				if err != nil {
					return "", err
				}
				srv, err := mail.ServerByRef(ctx, env.Q, env.Caller.TeamID, args.Str("server"))
				if err != nil {
					return "", err
				}
				dom, err := s.AddDomain(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, args.Str("domain"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Adding domain %s (id %s) — status %s. Run mail_dns_records once it is active.", dom.Domain, dom.ID, dom.Status), nil
			},
		},
		{
			Name: "mail_domain_remove", Scope: scopes.Write,
			Description: "Remove a mail domain and its mailboxes and aliases.",
			InputSchema: schema([]string{"domain"}, map[string]any{"domain": domainArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMail); err != nil {
					return "", err
				}
				s, err := mailSvcOf(env)
				if err != nil {
					return "", err
				}
				dom, srv, err := findMailDomain(ctx, env, args.Str("domain"))
				if err != nil {
					return "", err
				}
				if err := s.RemoveDomain(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, dom); err != nil {
					return "", err
				}
				return "Removed domain " + dom.Domain + ".", nil
			},
		},
		{
			Name: "mail_dns_records", Scope: scopes.Read,
			Description: "The MX, SPF, DKIM and DMARC records a mail domain needs.",
			InputSchema: schema([]string{"domain"}, map[string]any{"domain": domainArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				dom, srv, err := findMailDomain(ctx, env, args.Str("domain"))
				if err != nil {
					return "", err
				}
				return recordsText(mail.Records(dom, srv.Hostname)), nil
			},
		},
		{
			Name: "mail_dns_check", Scope: scopes.Read,
			Description: "Resolve a mail domain's DNS records and its server's PTR record; reports ok/missing/mismatch for each.",
			InputSchema: schema([]string{"domain"}, map[string]any{"domain": domainArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				dom, srv, err := findMailDomain(ctx, env, args.Str("domain"))
				if err != nil {
					return "", err
				}
				s, err := mailSvcOf(env)
				if err != nil {
					return "", err
				}
				res, err := s.CheckDomainDNS(ctx, env.Q, env.Caller.TeamID, srv, dom)
				if err != nil {
					return "", err
				}
				var b strings.Builder
				b.WriteString(recordsText(res.Records))
				fmt.Fprintf(&b, "PTR %s -> %s: %s", res.PTR.ServerIP, res.PTR.Hostname, res.PTR.Status)
				if res.PTR.Found != "" {
					fmt.Fprintf(&b, " (found %s)", res.PTR.Found)
				}
				b.WriteString("\n")
				return b.String(), nil
			},
		},
		{
			Name: "mail_dns_publish", Scope: scopes.Write,
			Description: "Publish a mail domain's DNS records through the team's saved Cloudflare credential, when one holds the zone.",
			InputSchema: schema([]string{"domain"}, map[string]any{"domain": domainArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMail); err != nil {
					return "", err
				}
				dom, srv, err := findMailDomain(ctx, env, args.Str("domain"))
				if err != nil {
					return "", err
				}
				s, err := mailSvcOf(env)
				if err != nil {
					return "", err
				}
				res, err := s.PublishDNS(ctx, env.Q, env.Caller.TeamID, dom, srv.Hostname)
				if err != nil {
					return "", err
				}
				var b strings.Builder
				if res.Zone != "" {
					fmt.Fprintf(&b, "zone: %s\n", res.Zone)
				}
				for _, p := range res.Published {
					b.WriteString("published: " + p + "\n")
				}
				for _, sk := range res.Skipped {
					b.WriteString("skipped: " + sk + "\n")
				}
				if len(res.Published) == 0 && len(res.Skipped) == 0 {
					b.WriteString("nothing to publish\n")
				}
				return b.String(), nil
			},
		},
		{
			Name: "mailboxes_list", Scope: scopes.Read,
			Description: "A mail domain's mailboxes.",
			InputSchema: schema([]string{"domain"}, map[string]any{"domain": domainArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				dom, _, err := findMailDomain(ctx, env, args.Str("domain"))
				if err != nil {
					return "", err
				}
				rows, err := mail.ListMailboxes(ctx, env.Q, env.Caller.TeamID, dom)
				if err != nil {
					return "", errors.New("could not list mailboxes")
				}
				if len(rows) == 0 {
					return dom.Domain + " has no mailboxes yet. mailbox_add creates one.", nil
				}
				var b strings.Builder
				for _, r := range rows {
					fmt.Fprintf(&b, "  %-32s %-10s quota %d MB  (id %s)\n", r.Email, r.Status, r.QuotaMB, r.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "mailbox_add", Scope: scopes.SecretsRead,
			Description: "Add a mailbox. Hosbah generates the password and returns it once — save it now, Hosbah never shows it again.",
			InputSchema: schema([]string{"domain", "local_part"}, map[string]any{
				"domain": domainArg, "local_part": str("The part before the @"), "quota_mb": map[string]any{"type": "integer", "default": 1024},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMail); err != nil {
					return "", err
				}
				s, err := mailSvcOf(env)
				if err != nil {
					return "", err
				}
				dom, srv, err := findMailDomain(ctx, env, args.Str("domain"))
				if err != nil {
					return "", err
				}
				created, err := s.AddMailbox(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, dom, args.Str("local_part"), int64(args.Int("quota_mb", 1024, 1, 1048576)))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Created mailbox %s (id %s). Password: %s — shown once.", created.Mailbox.Email, created.Mailbox.ID, created.Password), nil
			},
		},
		{
			Name: "mailbox_remove", Scope: scopes.Write,
			Description: "Remove a mailbox.",
			InputSchema: schema([]string{"domain", "mailbox"}, map[string]any{"domain": domainArg, "mailbox": mailboxArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMail); err != nil {
					return "", err
				}
				s, err := mailSvcOf(env)
				if err != nil {
					return "", err
				}
				dom, srv, err := findMailDomain(ctx, env, args.Str("domain"))
				if err != nil {
					return "", err
				}
				row, err := mail.MailboxByRef(ctx, env.Q, env.Caller.TeamID, dom, args.Str("mailbox"))
				if err != nil {
					return "", err
				}
				if err := s.RemoveMailbox(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, dom, row); err != nil {
					return "", err
				}
				return "Removed mailbox " + row.LocalPart + "@" + dom.Domain + ".", nil
			},
		},
		{
			Name: "mailbox_set_password", Scope: scopes.SecretsRead,
			Description: "Generate a new password for a mailbox and return it once.",
			InputSchema: schema([]string{"domain", "mailbox"}, map[string]any{"domain": domainArg, "mailbox": mailboxArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMail); err != nil {
					return "", err
				}
				s, err := mailSvcOf(env)
				if err != nil {
					return "", err
				}
				dom, srv, err := findMailDomain(ctx, env, args.Str("domain"))
				if err != nil {
					return "", err
				}
				row, err := mail.MailboxByRef(ctx, env.Q, env.Caller.TeamID, dom, args.Str("mailbox"))
				if err != nil {
					return "", err
				}
				pw, err := s.SetPassword(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, dom, row)
				if err != nil {
					return "", err
				}
				return "New password for " + row.LocalPart + "@" + dom.Domain + ": " + pw + " — shown once.", nil
			},
		},
		{
			Name: "mailbox_set_quota", Scope: scopes.Write,
			Description: "Change a mailbox's storage quota.",
			InputSchema: schema([]string{"domain", "mailbox", "quota_mb"}, map[string]any{
				"domain": domainArg, "mailbox": mailboxArg, "quota_mb": map[string]any{"type": "integer"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMail); err != nil {
					return "", err
				}
				s, err := mailSvcOf(env)
				if err != nil {
					return "", err
				}
				dom, srv, err := findMailDomain(ctx, env, args.Str("domain"))
				if err != nil {
					return "", err
				}
				row, err := mail.MailboxByRef(ctx, env.Q, env.Caller.TeamID, dom, args.Str("mailbox"))
				if err != nil {
					return "", err
				}
				if err := s.SetQuota(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, dom, row, int64(args.Int("quota_mb", 0, 1, 1048576))); err != nil {
					return "", err
				}
				return "Quota updated.", nil
			},
		},
		{
			Name: "mail_aliases_list", Scope: scopes.Read,
			Description: "A mail domain's aliases and external forwards.",
			InputSchema: schema([]string{"domain"}, map[string]any{"domain": domainArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				dom, _, err := findMailDomain(ctx, env, args.Str("domain"))
				if err != nil {
					return "", err
				}
				rows, err := mail.ListAliases(ctx, env.Q, env.Caller.TeamID, dom)
				if err != nil {
					return "", errors.New("could not list aliases")
				}
				if len(rows) == 0 {
					return dom.Domain + " has no aliases yet. mail_alias_add creates one.", nil
				}
				var b strings.Builder
				for _, r := range rows {
					fmt.Fprintf(&b, "  %-32s %-8s -> %s  %-10s (id %s)\n", r.Email, r.Kind, strings.Join(r.Destinations, ", "), r.Status, r.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "mail_alias_add", Scope: scopes.Write,
			Description: "Add an alias (fans mail out to one or more mailboxes on the same domain) or an external forward (relays to outside addresses).",
			InputSchema: schema([]string{"domain", "local_part", "kind", "destinations"}, map[string]any{
				"domain": domainArg, "local_part": str("The part before the @"),
				"kind":         map[string]any{"type": "string", "enum": []string{"alias", "forward"}},
				"destinations": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Destination email addresses"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMail); err != nil {
					return "", err
				}
				s, err := mailSvcOf(env)
				if err != nil {
					return "", err
				}
				dom, srv, err := findMailDomain(ctx, env, args.Str("domain"))
				if err != nil {
					return "", err
				}
				dests := strSliceArg(args, "destinations")
				row, err := s.AddAlias(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, dom, args.Str("local_part"), args.Str("kind"), dests)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Added %s %s -> %s (id %s).", row.Kind, row.Email, strings.Join(row.Destinations, ", "), row.ID), nil
			},
		},
		{
			Name: "mail_alias_remove", Scope: scopes.Write,
			Description: "Remove an alias or forward.",
			InputSchema: schema([]string{"domain", "alias"}, map[string]any{"domain": domainArg, "alias": aliasArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMail); err != nil {
					return "", err
				}
				s, err := mailSvcOf(env)
				if err != nil {
					return "", err
				}
				dom, srv, err := findMailDomain(ctx, env, args.Str("domain"))
				if err != nil {
					return "", err
				}
				row, err := mail.AliasByRef(ctx, env.Q, env.Caller.TeamID, args.Str("alias"))
				if err != nil || row.MailDomainID != dom.ID {
					return "", errors.New("mail alias not found")
				}
				if err := s.RemoveAlias(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, dom, row); err != nil {
					return "", err
				}
				return "Removed alias " + row.LocalPart + "@" + dom.Domain + ".", nil
			},
		},
	}
}

// ---- helpers --------------------------------------------------------------------------------------

func mailSvcOf(env *Env) (*mail.Service, error) {
	if env.App == nil {
		return nil, errors.New("the mail service is not running")
	}
	s, ok := env.App.Mail.(*mail.Service)
	if !ok || s == nil {
		return nil, errors.New("the mail service is not running")
	}
	return s, nil
}

// findMailDomain resolves a domain and its mail server together, since almost every domain tool
// needs both.
func findMailDomain(ctx context.Context, env *Env, ref string) (db.MailDomain, db.MailServer, error) {
	if ref == "" {
		return db.MailDomain{}, db.MailServer{}, errors.New("domain is required (a name or an id)")
	}
	dom, err := mail.DomainByRef(ctx, env.Q, env.Caller.TeamID, ref)
	if err != nil {
		return db.MailDomain{}, db.MailServer{}, err
	}
	srv, err := env.Q.MailServerInTeam(ctx, db.MailServerInTeamParams{ID: dom.MailServerID, TeamID: env.Caller.TeamID})
	if err != nil {
		return db.MailDomain{}, db.MailServer{}, errors.New("mail server not found")
	}
	return dom, srv, nil
}

// strSliceArg reads a JSON array-of-strings argument (the MCP client sends real JSON, not a
// comma string), tolerating a lone string for a caller that sends one destination bare.
func strSliceArg(args Args, k string) []string {
	switch v := args[k].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil
		}
		return []string{s}
	default:
		return nil
	}
}

func recordsText(recs []mail.Record) string {
	var b strings.Builder
	for _, r := range recs {
		status := ""
		if r.Status != "" && r.Status != "unknown" {
			status = "  [" + r.Status + "]"
		}
		if r.Type == "MX" {
			fmt.Fprintf(&b, "  MX  %-24s priority %-4d %s%s\n", r.Name, r.Priority, r.Value, status)
		} else {
			fmt.Fprintf(&b, "  TXT %-24s %s%s\n", r.Name, r.Value, status)
		}
		if r.Found != "" {
			fmt.Fprintf(&b, "      found: %s\n", r.Found)
		}
	}
	return b.String()
}
