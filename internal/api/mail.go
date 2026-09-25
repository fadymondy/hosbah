package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/mail"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// MH-577: team mail servers, REST under /api/teams/{team}/mail/... . Viewers read; owner/admin write
// (tenancy.AbilityManageMail), the same tier as manage_servers.

// ---- plumbing ------------------------------------------------------------------------------------

func (d *deps) mailSvc() *mail.Service {
	if d.a == nil {
		return nil
	}
	s, _ := d.a.Mail.(*mail.Service)
	return s
}

// mailErr maps a mail.Error to its HTTP status.
func mailErr(err error, fallback string) error {
	if err == nil {
		return nil
	}
	var me *mail.Error
	if errors.As(err, &me) {
		switch me.Status {
		case http.StatusNotFound:
			return huma.Error404NotFound(me.Message)
		case http.StatusConflict:
			return huma.Error409Conflict(me.Message)
		case http.StatusUnprocessableEntity:
			return huma.Error422UnprocessableEntity(me.Message)
		case http.StatusForbidden:
			return huma.Error403Forbidden(me.Message)
		}
	}
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	return huma.Error500InternalServerError(fallback)
}

// MailServerPath / MailDomainPath / MailboxPath / MailAliasPath address a mail server, domain,
// mailbox and alias under a team.
type MailServerPath struct {
	TeamPath
	Server string `path:"server" maxLength:"64"`
}
type MailDomainPath struct {
	TeamPath
	Domain string `path:"domain" maxLength:"253"`
}
type MailboxPath struct {
	MailDomainPath
	Mailbox string `path:"mailbox" maxLength:"64"`
}
type MailAliasPath struct {
	MailDomainPath
	Alias string `path:"alias" maxLength:"64"`
}

func mailServerIn(ctx context.Context, q *db.Queries, teamID, ref string) (db.MailServer, error) {
	row, err := mail.ServerByRef(ctx, q, teamID, ref)
	if err != nil {
		return db.MailServer{}, errNotFound
	}
	return row, nil
}
func mailDomainIn(ctx context.Context, q *db.Queries, teamID, ref string) (db.MailDomain, error) {
	row, err := mail.DomainByRef(ctx, q, teamID, ref)
	if err != nil {
		return db.MailDomain{}, errNotFound
	}
	return row, nil
}

// domainServer resolves a domain's own mail server (for dispatching ops).
func domainServer(ctx context.Context, q *db.Queries, teamID string, dom db.MailDomain) (db.MailServer, error) {
	srv, err := q.MailServerInTeam(ctx, db.MailServerInTeamParams{ID: dom.MailServerID, TeamID: teamID})
	if err != nil {
		return db.MailServer{}, errNotFound
	}
	return srv, nil
}

// ---- routes ----------------------------------------------------------------------------------

func (d *deps) registerMail(api huma.API) {
	tags := []string{"Mail"}

	// ---- servers ------------------------------------------------------------------------------

	type serverListOut struct {
		Body struct {
			Servers []mail.MailServer `json:"servers"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-mail-servers", Method: http.MethodGet, Path: "/api/teams/{team}/mail/servers",
		Summary: "The team's mail servers", Tags: tags,
	}, func(ctx context.Context, in *TeamPath) (*serverListOut, error) {
		out := &serverListOut{}
		out.Body.Servers = []mail.MailServer{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := mail.ListServers(ctx, q, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list mail servers")
			}
			out.Body.Servers = rows
			return nil
		})
		return out, err
	})

	type serverCreateOut struct{ Body mail.MailServer }
	huma.Register(api, huma.Operation{OperationID: "install-mail-server", Method: http.MethodPost, Path: "/api/teams/{team}/mail/servers",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Install a Stalwart mail server on one of the team's servers (owner or admin)",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body struct {
			Server   string `json:"server" minLength:"1" maxLength:"64" doc:"Server id or name."`
			Hostname string `json:"hostname" minLength:"1" maxLength:"253" doc:"The mail server's own HELO/PTR hostname, e.g. mail.example.com."`
		}
	}) (*serverCreateOut, error) {
		out := &serverCreateOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageMail, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			row, err := d.mailSvc().InstallServer(ctx, q, m.TeamID, webActor(id), mail.InstallInput{Server: in.Body.Server, Hostname: in.Body.Hostname})
			if err != nil {
				return mailErr(err, "could not install the mail server")
			}
			out.Body = row
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-mail-server", Method: http.MethodDelete, Path: "/api/teams/{team}/mail/servers/{server}",
		DefaultStatus: http.StatusNoContent, Tags: tags, Summary: "Remove a mail server and everything hosted on it (owner or admin)",
	}, func(ctx context.Context, in *MailServerPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageMail, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			row, err := mailServerIn(ctx, q, m.TeamID, in.Server)
			if err != nil {
				return err
			}
			return mailErr(d.mailSvc().RemoveServer(ctx, q, m.TeamID, webActor(id), row), "could not remove the mail server")
		})
		return nil, err
	})

	// ---- domains ------------------------------------------------------------------------------

	type domainListOut struct {
		Body struct {
			Domains []mail.Domain `json:"domains"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-mail-domains", Method: http.MethodGet, Path: "/api/teams/{team}/mail/domains",
		Summary: "The team's mail domains, optionally filtered to one server", Tags: tags,
	}, func(ctx context.Context, in *struct {
		TeamPath
		Server string `query:"server" doc:"Filter to one mail server's id."`
	}) (*domainListOut, error) {
		out := &domainListOut{}
		out.Body.Domains = []mail.Domain{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := mail.ListDomains(ctx, q, m.TeamID, in.Server)
			if err != nil {
				return huma.Error500InternalServerError("could not list mail domains")
			}
			out.Body.Domains = rows
			return nil
		})
		return out, err
	})

	type domainCreateOut struct{ Body mail.Domain }
	huma.Register(api, huma.Operation{OperationID: "add-mail-domain", Method: http.MethodPost, Path: "/api/teams/{team}/mail/domains",
		DefaultStatus: http.StatusCreated, Tags: tags, Summary: "Add a domain to a mail server (owner or admin)",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body struct {
			Server string `json:"server" minLength:"1" maxLength:"64" doc:"Mail server id."`
			Domain string `json:"domain" minLength:"1" maxLength:"253"`
		}
	}) (*domainCreateOut, error) {
		out := &domainCreateOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageMail, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := mailServerIn(ctx, q, m.TeamID, in.Body.Server)
			if err != nil {
				return err
			}
			row, err := d.mailSvc().AddDomain(ctx, q, m.TeamID, webActor(id), srv, in.Body.Domain)
			if err != nil {
				return mailErr(err, "could not add the domain")
			}
			out.Body = row
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-mail-domain", Method: http.MethodDelete, Path: "/api/teams/{team}/mail/domains/{domain}",
		DefaultStatus: http.StatusNoContent, Tags: tags, Summary: "Remove a mail domain and its mailboxes/aliases (owner or admin)",
	}, func(ctx context.Context, in *MailDomainPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageMail, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			dom, err := mailDomainIn(ctx, q, m.TeamID, in.Domain)
			if err != nil {
				return err
			}
			srv, err := domainServer(ctx, q, m.TeamID, dom)
			if err != nil {
				return err
			}
			return mailErr(d.mailSvc().RemoveDomain(ctx, q, m.TeamID, webActor(id), srv, dom), "could not remove the domain")
		})
		return nil, err
	})

	// ---- DNS ------------------------------------------------------------------------------------

	type dnsOut struct{ Body mail.DomainDNS }
	huma.Register(api, huma.Operation{OperationID: "get-mail-domain-dns", Method: http.MethodGet, Path: "/api/teams/{team}/mail/domains/{domain}/dns",
		Summary: "The DNS records this domain needs and, once checked, their live status", Tags: tags,
	}, func(ctx context.Context, in *MailDomainPath) (*dnsOut, error) {
		out := &dnsOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			dom, err := mailDomainIn(ctx, q, m.TeamID, in.Domain)
			if err != nil {
				return err
			}
			srv, err := domainServer(ctx, q, m.TeamID, dom)
			if err != nil {
				return err
			}
			out.Body.Records = mail.Records(dom, srv.Hostname)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "check-mail-domain-dns", Method: http.MethodPost, Path: "/api/teams/{team}/mail/domains/{domain}/dns/check",
		Tags: tags, Summary: "Resolve this domain's DNS records and PTR against what Hosbah's Stalwart mail server expects",
	}, func(ctx context.Context, in *MailDomainPath) (*dnsOut, error) {
		out := &dnsOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			dom, err := mailDomainIn(ctx, q, m.TeamID, in.Domain)
			if err != nil {
				return err
			}
			srv, err := domainServer(ctx, q, m.TeamID, dom)
			if err != nil {
				return err
			}
			res, err := d.mailSvc().CheckDomainDNS(ctx, q, m.TeamID, srv, dom)
			if err != nil {
				return mailErr(err, "could not check the domain's DNS")
			}
			out.Body = res
			return nil
		})
		return out, err
	})

	type publishOut struct{ Body mail.PublishResult }
	huma.Register(api, huma.Operation{OperationID: "publish-mail-domain-dns", Method: http.MethodPost, Path: "/api/teams/{team}/mail/domains/{domain}/dns/publish",
		Tags: tags, Summary: "Publish this domain's DNS records through the team's Cloudflare credential (owner or admin)",
	}, func(ctx context.Context, in *MailDomainPath) (*publishOut, error) {
		out := &publishOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageMail, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			dom, err := mailDomainIn(ctx, q, m.TeamID, in.Domain)
			if err != nil {
				return err
			}
			srv, err := domainServer(ctx, q, m.TeamID, dom)
			if err != nil {
				return err
			}
			res, err := d.mailSvc().PublishDNS(ctx, q, m.TeamID, dom, srv.Hostname)
			if err != nil {
				return mailErr(err, "could not publish the domain's DNS records")
			}
			out.Body = res
			return nil
		})
		return out, err
	})

	// ---- mailboxes ----------------------------------------------------------------------------

	type mailboxListOut struct {
		Body struct {
			Mailboxes []mail.Mailbox `json:"mailboxes"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-mailboxes", Method: http.MethodGet, Path: "/api/teams/{team}/mail/domains/{domain}/mailboxes",
		Summary: "A domain's mailboxes", Tags: tags,
	}, func(ctx context.Context, in *MailDomainPath) (*mailboxListOut, error) {
		out := &mailboxListOut{}
		out.Body.Mailboxes = []mail.Mailbox{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			dom, err := mailDomainIn(ctx, q, m.TeamID, in.Domain)
			if err != nil {
				return err
			}
			rows, err := mail.ListMailboxes(ctx, q, m.TeamID, dom)
			if err != nil {
				return huma.Error500InternalServerError("could not list mailboxes")
			}
			out.Body.Mailboxes = rows
			return nil
		})
		return out, err
	})

	type mailboxCreateOut struct{ Body mail.MailboxCreated }
	huma.Register(api, huma.Operation{OperationID: "add-mailbox", Method: http.MethodPost, Path: "/api/teams/{team}/mail/domains/{domain}/mailboxes",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Add a mailbox. Hosbah generates the password and returns it once (owner or admin)",
	}, func(ctx context.Context, in *struct {
		MailDomainPath
		Body struct {
			LocalPart string `json:"local_part" minLength:"1" maxLength:"64"`
			QuotaMB   int64  `json:"quota_mb,omitempty" doc:"Defaults to 1024."`
		}
	}) (*mailboxCreateOut, error) {
		out := &mailboxCreateOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageMail, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			dom, err := mailDomainIn(ctx, q, m.TeamID, in.Domain)
			if err != nil {
				return err
			}
			srv, err := domainServer(ctx, q, m.TeamID, dom)
			if err != nil {
				return err
			}
			created, err := d.mailSvc().AddMailbox(ctx, q, m.TeamID, webActor(id), srv, dom, in.Body.LocalPart, in.Body.QuotaMB)
			if err != nil {
				return mailErr(err, "could not add the mailbox")
			}
			out.Body = created
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-mailbox", Method: http.MethodDelete, Path: "/api/teams/{team}/mail/domains/{domain}/mailboxes/{mailbox}",
		DefaultStatus: http.StatusNoContent, Tags: tags, Summary: "Remove a mailbox (owner or admin)",
	}, func(ctx context.Context, in *MailboxPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageMail, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			dom, err := mailDomainIn(ctx, q, m.TeamID, in.Domain)
			if err != nil {
				return err
			}
			srv, err := domainServer(ctx, q, m.TeamID, dom)
			if err != nil {
				return err
			}
			row, err := mail.MailboxByRef(ctx, q, m.TeamID, dom, in.Mailbox)
			if err != nil {
				return errNotFound
			}
			return mailErr(d.mailSvc().RemoveMailbox(ctx, q, m.TeamID, webActor(id), srv, dom, row), "could not remove the mailbox")
		})
		return nil, err
	})

	type passwordOut struct {
		Body struct {
			Password string `json:"password" doc:"Shown once."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "set-mailbox-password", Method: http.MethodPost, Path: "/api/teams/{team}/mail/domains/{domain}/mailboxes/{mailbox}/password",
		Tags: tags, Summary: "Generate a new mailbox password and return it once (owner or admin)",
	}, func(ctx context.Context, in *MailboxPath) (*passwordOut, error) {
		out := &passwordOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageMail, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			dom, err := mailDomainIn(ctx, q, m.TeamID, in.Domain)
			if err != nil {
				return err
			}
			srv, err := domainServer(ctx, q, m.TeamID, dom)
			if err != nil {
				return err
			}
			row, err := mail.MailboxByRef(ctx, q, m.TeamID, dom, in.Mailbox)
			if err != nil {
				return errNotFound
			}
			pw, err := d.mailSvc().SetPassword(ctx, q, m.TeamID, webActor(id), srv, dom, row)
			if err != nil {
				return mailErr(err, "could not reset the mailbox password")
			}
			out.Body.Password = pw
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "set-mailbox-quota", Method: http.MethodPost, Path: "/api/teams/{team}/mail/domains/{domain}/mailboxes/{mailbox}/quota",
		Tags: tags, Summary: "Change a mailbox's quota (owner or admin)",
	}, func(ctx context.Context, in *struct {
		MailboxPath
		Body struct {
			QuotaMB int64 `json:"quota_mb" minimum:"1"`
		}
	}) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageMail, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			dom, err := mailDomainIn(ctx, q, m.TeamID, in.Domain)
			if err != nil {
				return err
			}
			srv, err := domainServer(ctx, q, m.TeamID, dom)
			if err != nil {
				return err
			}
			row, err := mail.MailboxByRef(ctx, q, m.TeamID, dom, in.Mailbox)
			if err != nil {
				return errNotFound
			}
			return mailErr(d.mailSvc().SetQuota(ctx, q, m.TeamID, webActor(id), srv, dom, row, in.Body.QuotaMB), "could not change the mailbox's quota")
		})
		return nil, err
	})

	// ---- aliases --------------------------------------------------------------------------------

	type aliasListOut struct {
		Body struct {
			Aliases []mail.Alias `json:"aliases"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-mail-aliases", Method: http.MethodGet, Path: "/api/teams/{team}/mail/domains/{domain}/aliases",
		Summary: "A domain's aliases and external forwards", Tags: tags,
	}, func(ctx context.Context, in *MailDomainPath) (*aliasListOut, error) {
		out := &aliasListOut{}
		out.Body.Aliases = []mail.Alias{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			dom, err := mailDomainIn(ctx, q, m.TeamID, in.Domain)
			if err != nil {
				return err
			}
			rows, err := mail.ListAliases(ctx, q, m.TeamID, dom)
			if err != nil {
				return huma.Error500InternalServerError("could not list aliases")
			}
			out.Body.Aliases = rows
			return nil
		})
		return out, err
	})

	type aliasCreateOut struct{ Body mail.Alias }
	huma.Register(api, huma.Operation{OperationID: "add-mail-alias", Method: http.MethodPost, Path: "/api/teams/{team}/mail/domains/{domain}/aliases",
		DefaultStatus: http.StatusCreated, Tags: tags, Summary: "Add an alias (fans out to mailboxes) or an external forward (owner or admin)",
	}, func(ctx context.Context, in *struct {
		MailDomainPath
		Body struct {
			LocalPart    string   `json:"local_part" minLength:"1" maxLength:"64"`
			Kind         string   `json:"kind" enum:"alias,forward"`
			Destinations []string `json:"destinations" minItems:"1"`
		}
	}) (*aliasCreateOut, error) {
		out := &aliasCreateOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageMail, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			dom, err := mailDomainIn(ctx, q, m.TeamID, in.Domain)
			if err != nil {
				return err
			}
			srv, err := domainServer(ctx, q, m.TeamID, dom)
			if err != nil {
				return err
			}
			row, err := d.mailSvc().AddAlias(ctx, q, m.TeamID, webActor(id), srv, dom, in.Body.LocalPart, in.Body.Kind, in.Body.Destinations)
			if err != nil {
				return mailErr(err, "could not add the alias")
			}
			out.Body = row
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-mail-alias", Method: http.MethodDelete, Path: "/api/teams/{team}/mail/domains/{domain}/aliases/{alias}",
		DefaultStatus: http.StatusNoContent, Tags: tags, Summary: "Remove an alias or forward (owner or admin)",
	}, func(ctx context.Context, in *MailAliasPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageMail, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			dom, err := mailDomainIn(ctx, q, m.TeamID, in.Domain)
			if err != nil {
				return err
			}
			srv, err := domainServer(ctx, q, m.TeamID, dom)
			if err != nil {
				return err
			}
			row, err := mail.AliasByRef(ctx, q, m.TeamID, in.Alias)
			if err != nil || row.MailDomainID != dom.ID {
				return errNotFound
			}
			return mailErr(d.mailSvc().RemoveAlias(ctx, q, m.TeamID, webActor(id), srv, dom, row), "could not remove the alias")
		})
		return nil, err
	})
}
