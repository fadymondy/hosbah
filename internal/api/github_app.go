package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/oauthstate"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The GitHub App for a team (internal/deploy/githubapp.go has the rest):

	GET /api/teams/{team}/github/app          the App's status and the team's installations
	GET /api/teams/{team}/github/install      owner/admin → 303 to GitHub's install page, with a
	                                          single-use state bound to the team, the user and the browser
	GET /api/github/setup                     the App's Setup URL: GitHub sends the browser back here
	GET /api/teams/{team}/github/app/repos    repositories across the team's installations

The setup callback trusts nothing it is given: the state must be the one this browser was sent off
with (internal/oauthstate) and name the signed-in user, who must still manage the team; the
installation is then read back from GitHub with the App's JWT, and only GitHub's answer is stored.
setup_action=update (the installation's settings changed on github.com) refreshes a row the user's
team already holds, confirmed the same way.
*/

const githubInstallCookie = "hosbah_gh_install"

// GitHubInstallationOut is one GitHub App installation of the team (never a token).
type GitHubInstallationOut struct {
	ID                  string     `json:"id"`
	InstallationID      int64      `json:"installation_id"`
	AccountLogin        string     `json:"account_login"`
	AccountType         string     `json:"account_type" enum:"User,Organization,Enterprise"`
	RepositorySelection string     `json:"repository_selection" enum:"all,selected"`
	SuspendedAt         *time.Time `json:"suspended_at"`
	CreatedAt           time.Time  `json:"created_at"`
	ManageURL           string     `json:"manage_url" doc:"Where the installation's repository access is changed on github.com."`
}

// GitHubAppStatusOut is the App's status for a team.
type GitHubAppStatusOut struct {
	Configured    bool                    `json:"configured" doc:"The control plane has the App's credentials (HOSBAH_GITHUB_APP_*)."`
	Slug          string                  `json:"slug"`
	CanManage     bool                    `json:"can_manage" doc:"The caller may install the App for this team (owner or admin)."`
	Installations []GitHubInstallationOut `json:"installations"`
}

func toInstallationOut(in db.GithubInstallation) GitHubInstallationOut {
	manage := "https://github.com/settings/installations/" + itoa(in.InstallationID)
	if in.AccountType == "Organization" {
		manage = "https://github.com/organizations/" + url.PathEscape(in.AccountLogin) + "/settings/installations/" + itoa(in.InstallationID)
	}
	return GitHubInstallationOut{ID: in.ID, InstallationID: in.InstallationID, AccountLogin: in.AccountLogin, AccountType: in.AccountType,
		RepositorySelection: in.RepositorySelection, SuspendedAt: in.SuspendedAt, CreatedAt: in.CreatedAt, ManageURL: manage}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func webLocale(l string) string {
	if l == "ar" {
		return "ar"
	}
	return "en"
}

// redirectOut is a 303 to a page of the web app (and, for the install, to GitHub).
type redirectOut struct {
	Status    int
	Location  string `header:"Location"`
	SetCookie string `header:"Set-Cookie"`
}

func (d *deps) registerGitHubApp(api huma.API) {
	tags := []string{"GitHub App"}

	type githubAppStatusOut struct{ Body GitHubAppStatusOut }
	huma.Register(api, huma.Operation{OperationID: "get-github-app", Method: http.MethodGet, Path: "/api/teams/{team}/github/app", Tags: tags,
		Summary: "The GitHub App: whether it is configured, and the team's installations (the GitHub accounts it is installed on)."},
		func(ctx context.Context, in *struct{ TeamPath }) (*githubAppStatusOut, error) {
			out := &githubAppStatusOut{}
			g := d.deploys().GitHub()
			out.Body = GitHubAppStatusOut{Configured: g.Configured(), Slug: g.Slug(), Installations: []GitHubInstallationOut{}}
			err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
				out.Body.CanManage = tenancy.Can(ctx, m, tenancy.AbilityManageTeam)
				rows, err := q.GitHubInstallationsOfTeam(ctx, m.TeamID)
				if err != nil {
					return huma.Error500InternalServerError("lookup failed")
				}
				for _, r := range rows {
					out.Body.Installations = append(out.Body.Installations, toInstallationOut(r))
				}
				return nil
			})
			return out, err
		})

	huma.Register(api, huma.Operation{OperationID: "install-github-app", Method: http.MethodGet, Path: "/api/teams/{team}/github/install", Tags: tags,
		DefaultStatus: http.StatusSeeOther,
		Summary: "Install (or configure) the GitHub App for this team: 303 to GitHub's installation page with a single-use state bound " +
			"to the team, you and this browser (ten minutes). GitHub sends you back to /api/github/setup. Owner or admin."},
		func(ctx context.Context, in *struct {
			TeamPath
			Locale string `query:"locale" maxLength:"5" doc:"The web app's language to come back to (en or ar)."`
		}) (*redirectOut, error) {
			g := d.deploys().GitHub()
			var state, browser string
			err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTeam, func(_ *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
				if !g.Configured() || g.Slug() == "" {
					return huma.Error503ServiceUnavailable("the GitHub App is not configured on this platform (HOSBAH_GITHUB_APP_ID, _SLUG, _PRIVATE_KEY_FILE, HOSBAH_GITHUB_WEBHOOK_SECRET)")
				}
				var err error
				state, browser, err = oauthstate.Begin(ctx, d.a.SQLDB, oauthstate.Record{Purpose: oauthstate.PurposeGitHubInstall,
					UserID: id.ID, TeamID: m.TeamID, Locale: webLocale(in.Locale)}, time.Now())
				if err != nil {
					return huma.Error500InternalServerError("could not start the installation")
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			return &redirectOut{Status: http.StatusSeeOther,
				Location:  "https://github.com/apps/" + url.PathEscape(g.Slug()) + "/installations/new?" + url.Values{"state": {state}}.Encode(),
				SetCookie: oauthstate.BrowserCookie(githubInstallCookie, browser, oauthstate.Secure(false)).String()}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "github-app-setup", Method: http.MethodGet, Path: "/api/github/setup", Tags: tags,
		DefaultStatus: http.StatusSeeOther,
		Summary: "The GitHub App's Setup URL. Checks the state this browser was sent off with, confirms the installation with GitHub " +
			"(GET /app/installations/{id} with the App's JWT) and binds it to the team; setup_action=update refreshes an installation " +
			"your team already has. 303 back to the team's settings."},
		func(ctx context.Context, in *struct {
			InstallationID int64  `query:"installation_id" minimum:"0"`
			SetupAction    string `query:"setup_action" maxLength:"20"`
			State          string `query:"state" maxLength:"200"`
			Browser        string `cookie:"hosbah_gh_install"`
		}) (*redirectOut, error) {
			id, err := current(ctx)
			if err != nil {
				return nil, err
			}
			clear := oauthstate.ClearCookie(githubInstallCookie, oauthstate.Secure(false)).String()
			to := func(locale, slug string, q url.Values) *redirectOut {
				page := "/" + webLocale(locale) + "/account"
				if slug != "" {
					page = "/" + webLocale(locale) + "/t/" + url.PathEscape(slug) + "/settings"
				}
				return &redirectOut{Status: http.StatusSeeOther, Location: page + "?" + q.Encode(), SetCookie: clear}
			}
			fail := func(locale, slug, code string) *redirectOut {
				return to(locale, slug, url.Values{"github_error": {code}})
			}

			var rec oauthstate.Record
			if strings.TrimSpace(in.State) != "" {
				rec, err = oauthstate.Finish(ctx, d.a.SQLDB, in.State, in.Browser, time.Now(), oauthstate.PurposeGitHubInstall)
				if err != nil || rec.UserID != id.ID {
					return fail("en", "", "state_invalid"), nil
				}
			} else if in.SetupAction == "update" && in.InstallationID > 0 {
				// A change made on github.com: only an installation the user's own team holds.
				team := ""
				_ = d.tx(ctx, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
					row, err := q.GitHubInstallationByInstallationID(ctx, in.InstallationID)
					if err == nil {
						team = row.TeamID
					}
					return nil
				})
				if team == "" {
					return fail("en", "", "state_invalid"), nil
				}
				rec = oauthstate.Record{TeamID: team, UserID: id.ID, Locale: "en"}
			} else {
				return fail("en", "", "state_invalid"), nil
			}

			// The user must still manage the team the state names (a membership or role may have changed).
			slug := ""
			if err := d.inTeam(ctx, rec.TeamID, tenancy.AbilityManageTeam, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
				t, err := q.GetTeam(ctx, m.TeamID)
				if err != nil {
					return errNotFound
				}
				slug = t.Slug
				return nil
			}); err != nil {
				return fail(rec.Locale, "", "forbidden"), nil
			}
			switch {
			case in.SetupAction == "request":
				// An organisation member asked an owner to approve it: nothing is installed yet.
				return to(rec.Locale, slug, url.Values{"github": {"requested"}}), nil
			case in.InstallationID <= 0:
				return fail(rec.Locale, slug, "installation_unknown"), nil
			}
			s := d.deploys()
			confirmed, err := s.ConfirmInstallation(ctx, in.InstallationID)
			if err == nil {
				_, err = s.StoreInstallation(ctx, rec.TeamID, id.ID, confirmed)
			}
			var ie *deploy.InstallationError
			switch {
			case errors.As(err, &ie):
				return fail(rec.Locale, slug, ie.Code), nil
			case err != nil:
				return fail(rec.Locale, slug, "internal"), nil
			}
			return to(rec.Locale, slug, url.Values{"github": {"connected"}}), nil
		})

	type githubAppReposOut struct {
		Body struct {
			Repos         []deploy.GitHubAppRepo `json:"repos"`
			Installations int                    `json:"installations"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-github-app-repos", Method: http.MethodGet, Path: "/api/teams/{team}/github/app/repos", Tags: tags,
		Summary: "Repositories the team's GitHub App installations can reach, newest first (cached for a minute). Their URL is a " +
			"repo_url for a new site; deploys of them clone with a fresh installation token. Owner, admin or deployer."},
		func(ctx context.Context, in *struct{ TeamPath }) (*githubAppReposOut, error) {
			var (
				insts  []db.GithubInstallation
				teamID string
			)
			err := d.inTeam(ctx, in.Team, tenancy.AbilityDeploy, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
				teamID = m.TeamID
				rows, err := q.GitHubInstallationsOfTeam(ctx, m.TeamID)
				insts = rows
				return err
			})
			if err != nil {
				return nil, err
			}
			out := &githubAppReposOut{}
			out.Body.Repos = []deploy.GitHubAppRepo{}
			out.Body.Installations = len(insts)
			if len(insts) == 0 {
				return out, nil
			}
			repos, err := d.deploys().AppRepos(ctx, teamID, insts)
			if errors.Is(err, deploy.ErrGitHubNotConfigured) {
				return nil, huma.Error503ServiceUnavailable(err.Error())
			}
			if err != nil {
				return nil, huma.Error502BadGateway("GitHub could not list the repositories — try again")
			}
			out.Body.Repos = repos
			return out, nil
		})
}
