package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// appLogoURL is where an app's uploaded logo is served. By id: {app} resolves an id or a name,
// and the id is the one that cannot collide with another app's name.
func appLogoURL(teamID, appID, etag string) *string { return deploy.AppLogoURL(teamID, appID, etag) }

// appOutWithLogo is toAppOut plus logo_url (one bytes-free query).
func appOutWithLogo(ctx context.Context, q *db.Queries, teamID string, a db.App, server string) AppOut {
	o := toAppOut(a, server)
	if etag, err := q.AppLogoVersion(ctx, db.AppLogoVersionParams{AppID: a.ID, TeamID: teamID}); err == nil {
		o.LogoURL = appLogoURL(teamID, a.ID, etag)
	}
	return o
}

// attachAppLogos points logo_url of every app with an uploaded logo at it (one query, no bytes).
func attachAppLogos(ctx context.Context, q *db.Queries, teamID string, apps []AppOut) {
	rows, err := q.ListAppLogoVersionsOfTeam(ctx, teamID)
	if err != nil || len(rows) == 0 {
		return
	}
	etags := make(map[string]string, len(rows))
	for _, r := range rows {
		etags[r.AppID] = r.Etag
	}
	for i := range apps {
		if e, ok := etags[apps[i].ID]; ok {
			apps[i].LogoURL = appLogoURL(teamID, apps[i].ID, e)
		}
	}
}

// registerAppLogo: an app's uploaded logo, the same rules as a server's (logo.go). Owner or admin
// write (manage_apps, like the appearance PATCH); any member reads. Git and imported apps alike.
func (d *deps) registerAppLogo(api huma.API) {
	type appOut struct{ Body AppOut }
	tags := []string{"Apps"}

	huma.Register(api, huma.Operation{
		OperationID: "put-app-logo", Method: http.MethodPut, Path: "/api/teams/{team}/apps/{app}/logo",
		Summary:      "Upload the app's logo: the raw bytes of a PNG, JPEG or WebP of at most 512 KiB (owner or admin). Audited as app.logo_updated.",
		Tags:         tags,
		MaxBodyBytes: MaxLogoBytes,
		// Not required, so that a missing body is refused by the handler (after the team check).
		RequestBody: &huma.RequestBody{Content: logoBinary("The image itself; Content-Type names its format.")},
	}, func(ctx context.Context, in *struct {
		AppPath
		ContentType string `header:"Content-Type" doc:"image/png, image/jpeg or image/webp"`
		RawBody     []byte
	}) (*appOut, error) {
		out := &appOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if err := need(ctx, m, tenancy.AbilityManageApps); err != nil {
				return err
			}
			ct, etag, err := checkLogo(in.ContentType, in.RawBody)
			if err != nil {
				return err
			}
			if err := q.UpsertAppLogo(ctx, db.UpsertAppLogoParams{AppID: a.ID, TeamID: m.TeamID, ContentType: ct, Data: in.RawBody, Etag: etag}); err != nil {
				return huma.Error500InternalServerError("could not store the logo")
			}
			out.Body = appOutWithLogo(ctx, q, m.TeamID, a, "")
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "app.logo_updated", "app", a.ID,
				map[string]any{"name": a.Name, "content_type": ct, "bytes": len(in.RawBody), "sha256": etag})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-app-logo", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}/logo",
		Summary: "The app's uploaded logo (any member); 404 without one. The URL in logo_url is versioned, so the image is cached.",
		Tags:    tags,
		Responses: map[string]*huma.Response{"200": {Description: "The image", Content: logoBinary("The image")},
			"304": {Description: "Not modified"}},
	}, func(ctx context.Context, in *struct {
		AppPath
		V           string `query:"v" maxLength:"64" doc:"Cache-busting version; ignored."`
		IfNoneMatch string `header:"If-None-Match"`
	}) (*logoOut, error) {
		out := &logoOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			l, err := q.GetAppLogo(ctx, db.GetAppLogoParams{AppID: a.ID, TeamID: m.TeamID})
			if err != nil {
				return errNotFound
			}
			return serveLogo(out, in.IfNoneMatch, l.ContentType, l.Etag, l.Data)
		})
		return out, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-app-logo", Method: http.MethodDelete, Path: "/api/teams/{team}/apps/{app}/logo",
		Summary: "Remove the app's uploaded logo (owner or admin). Audited as app.logo_removed.", Tags: tags,
	}, func(ctx context.Context, in *AppPath) (*appOut, error) {
		out := &appOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if err := need(ctx, m, tenancy.AbilityManageApps); err != nil {
				return err
			}
			n, err := q.DeleteAppLogo(ctx, db.DeleteAppLogoParams{AppID: a.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not remove the logo")
			}
			out.Body = toAppOut(a, "")
			if n == 0 {
				return nil
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "app.logo_removed", "app", a.ID, map[string]any{"name": a.Name})
		})
		return out, err
	})
}
