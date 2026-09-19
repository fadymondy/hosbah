package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"mime"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// MaxServerLogoBytes is the largest logo accepted (keep in step with server_logos_size_check).
const MaxServerLogoBytes = 512 << 10

// logoTypes are the accepted image types. No SVG: it is a document that can carry script.
var logoTypes = []string{"image/png", "image/jpeg", "image/webp"}

// serverLogoURL is where a server's uploaded logo is served, versioned by its etag so the
// response can be cached forever.
func serverLogoURL(teamID, serverID, etag string) *string {
	v := etag
	if len(v) > 16 {
		v = v[:16]
	}
	u := "/api/teams/" + teamID + "/servers/" + serverID + "/logo?v=" + v
	return &u
}

// sniffLogoType says what the bytes really are (magic bytes), or "".
func sniffLogoType(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(b, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}

// attachLogos points logo_url of every server with an uploaded logo at it (one query, no bytes).
func attachLogos(ctx context.Context, q *db.Queries, teamID string, servers []ServerOut) {
	rows, err := q.ListServerLogoVersionsOfTeam(ctx, teamID)
	if err != nil || len(rows) == 0 {
		return
	}
	etags := make(map[string]string, len(rows))
	for _, r := range rows {
		etags[r.ServerID] = r.Etag
	}
	for i := range servers {
		if e, ok := etags[servers[i].ID]; ok {
			servers[i].LogoURL = serverLogoURL(teamID, servers[i].ID, e)
		}
	}
}

func (d *deps) registerServerLogo(api huma.API) {
	type serverOut struct{ Body ServerOut }
	binary := func(desc string) map[string]*huma.MediaType {
		m := map[string]*huma.MediaType{}
		for _, t := range logoTypes {
			m[t] = &huma.MediaType{Schema: &huma.Schema{Type: "string", Format: "binary", Description: desc}}
		}
		return m
	}

	huma.Register(api, huma.Operation{
		OperationID: "put-server-logo", Method: http.MethodPut, Path: "/api/teams/{team}/servers/{server}/logo",
		Summary:      "Upload the server's logo: the raw bytes of a PNG, JPEG or WebP of at most 512 KiB (owner or admin). Audited as server.logo_updated.",
		Tags:         []string{"Servers"},
		MaxBodyBytes: MaxServerLogoBytes,
		// Not required, so that a missing body is refused by the handler (after the team check).
		RequestBody: &huma.RequestBody{Content: binary("The image itself; Content-Type names its format.")},
	}, func(ctx context.Context, in *struct {
		ServerPath
		ContentType string `header:"Content-Type" doc:"image/png, image/jpeg or image/webp"`
		RawBody     []byte
	}) (*serverOut, error) {
		out := &serverOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if !tenancy.Can(ctx, m, tenancy.AbilityManageServers) {
				return errForbidden
			}
			ct, _, _ := mime.ParseMediaType(in.ContentType)
			ct = strings.ToLower(ct)
			ok := false
			for _, t := range logoTypes {
				ok = ok || t == ct
			}
			if !ok {
				return huma.Error415UnsupportedMediaType("a logo is a PNG, JPEG or WebP image (Content-Type image/png, image/jpeg or image/webp)")
			}
			if len(in.RawBody) == 0 {
				return huma.Error422UnprocessableEntity("the request body is the image")
			}
			if len(in.RawBody) > MaxServerLogoBytes {
				return huma.NewError(http.StatusRequestEntityTooLarge, "a logo is at most 512 KiB")
			}
			if sniffLogoType(in.RawBody) != ct {
				return huma.Error422UnprocessableEntity("the file is not a " + ct + " image")
			}
			sum := sha256.Sum256(in.RawBody)
			etag := hex.EncodeToString(sum[:])
			if err := q.UpsertServerLogo(ctx, db.UpsertServerLogoParams{ServerID: s.ID, TeamID: m.TeamID, ContentType: ct, Data: in.RawBody, Etag: etag}); err != nil {
				return huma.Error500InternalServerError("could not store the logo")
			}
			out.Body = d.serverDetail(ctx, q, m, s)
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "server.logo_updated", "server", s.ID,
				map[string]any{"name": s.Name, "content_type": ct, "bytes": len(in.RawBody), "sha256": etag})
		})
		return out, err
	})

	type logoOut struct {
		ContentType string `header:"Content-Type"`
		NoSniff     string `header:"X-Content-Type-Options"`
		CSP         string `header:"Content-Security-Policy"`
		Cache       string `header:"Cache-Control"`
		ETag        string `header:"ETag"`
		Body        []byte
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-server-logo", Method: http.MethodGet, Path: "/api/teams/{team}/servers/{server}/logo",
		Summary: "The server's uploaded logo (any member); 404 without one. The URL in logo_url is versioned, so the image is cached.",
		Tags:    []string{"Servers"},
		Responses: map[string]*huma.Response{"200": {Description: "The image", Content: binary("The image")},
			"304": {Description: "Not modified"}},
	}, func(ctx context.Context, in *struct {
		ServerPath
		V           string `query:"v" maxLength:"64" doc:"Cache-busting version; ignored."`
		IfNoneMatch string `header:"If-None-Match"`
	}) (*logoOut, error) {
		out := &logoOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			l, err := q.GetServerLogo(ctx, db.GetServerLogoParams{ServerID: s.ID, TeamID: m.TeamID})
			if err != nil {
				return errNotFound
			}
			if in.IfNoneMatch == `"`+l.Etag+`"` {
				return huma.Status304NotModified()
			}
			*out = logoOut{ContentType: l.ContentType, NoSniff: "nosniff", CSP: "default-src 'none'; sandbox",
				Cache: "private, max-age=31536000, immutable", ETag: `"` + l.Etag + `"`, Body: l.Data}
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-server-logo", Method: http.MethodDelete, Path: "/api/teams/{team}/servers/{server}/logo",
		Summary: "Remove the server's uploaded logo (owner or admin). Audited as server.logo_removed.", Tags: []string{"Servers"},
	}, func(ctx context.Context, in *ServerPath) (*serverOut, error) {
		out := &serverOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if !tenancy.Can(ctx, m, tenancy.AbilityManageServers) {
				return errForbidden
			}
			n, err := q.DeleteServerLogo(ctx, db.DeleteServerLogoParams{ServerID: s.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not remove the logo")
			}
			out.Body = d.serverDetail(ctx, q, m, s)
			if n == 0 {
				return nil
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "server.logo_removed", "server", s.ID, map[string]any{"name": s.Name})
		})
		return out, err
	})
}
