package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/totp"
)

type UserOut struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	PlatformAdmin bool   `json:"platform_admin"`
}

type TeamSummary struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type MeOut struct {
	Body struct {
		User  UserOut       `json:"user"`
		Teams []TeamSummary `json:"teams"`
	}
}

type ReportSummary struct {
	Key       string    `json:"key"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	IssueType string    `json:"issue_type"`
	CreatedAt time.Time `json:"created_at"`
}

// HasRoleCSV reports whether a users.roles CSV contains role.
func HasRoleCSV(csv, role string) bool {
	for _, r := range strings.Split(csv, ",") {
		if strings.TrimSpace(r) == role {
			return true
		}
	}
	return false
}

func (d *deps) registerMe(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-me", Method: http.MethodGet, Path: "/api/me",
		Summary: "Who am I, and which teams am I in", Tags: []string{"Account"},
	}, func(ctx context.Context, _ *struct{}) (*MeOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		out := &MeOut{}
		out.Body.Teams = []TeamSummary{}
		err = d.tx(ctx, tenancy.Scope{UserID: id.ID}, func(q *db.Queries, _ *sql.Tx) error {
			u, err := q.UserByID(ctx, id.ID)
			if err != nil {
				return huma.Error401Unauthorized("unauthorized")
			}
			// Roles from the database, not the session: a demoted admin stops being
			// shown admin controls at once (the API tier re-checks on every call).
			out.Body.User = UserOut{ID: u.ID, Email: u.Email, PlatformAdmin: HasRoleCSV(u.Roles, "admin")}
			rows, err := q.ListTeamsForUser(ctx, id.ID)
			if err != nil {
				return huma.Error500InternalServerError("could not list teams")
			}
			for _, r := range rows {
				out.Body.Teams = append(out.Body.Teams, TeamSummary{ID: r.ID, Name: r.Name, Slug: r.Slug, Role: r.Role, CreatedAt: r.CreatedAt})
			}
			return nil
		})
		return out, err
	})

	type twoFAOut struct {
		Body struct {
			Enabled bool `json:"enabled"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-my-2fa", Method: http.MethodGet, Path: "/api/me/2fa",
		Summary: "Whether two-factor sign-in is on for my account", Tags: []string{"Account"},
	}, func(ctx context.Context, _ *struct{}) (*twoFAOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		out := &twoFAOut{}
		row, err := db.New(d.a.SQLDB).TotpForSubject(ctx, id.ID)
		if err == nil {
			out.Body.Enabled = row.Enabled == "true"
		} else if !isNoRows(err) {
			return nil, huma.Error500InternalServerError("lookup failed")
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "disable-my-2fa", Method: http.MethodPost, Path: "/api/me/2fa/disable",
		Summary: "Turn off two-factor sign-in (needs a current code)", Tags: []string{"Account"},
	}, func(ctx context.Context, in *struct {
		Body struct {
			Code string `json:"code" minLength:"6" maxLength:"6"`
		}
	}) (*twoFAOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		q := db.New(d.a.SQLDB)
		row, err := q.TotpForSubject(ctx, id.ID)
		if err != nil || row.Enabled != "true" {
			return nil, huma.Error409Conflict("two-factor sign-in is not on")
		}
		if !totp.Valid(row.Secret, in.Body.Code, time.Now()) {
			return nil, huma.Error422UnprocessableEntity("that code is not valid")
		}
		if err := q.DeleteTotp(ctx, id.ID); err != nil {
			return nil, huma.Error500InternalServerError("could not disable")
		}
		return &twoFAOut{}, nil
	})

	type reportsOut struct {
		Body struct {
			Reports []ReportSummary `json:"reports"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-my-reports", Method: http.MethodGet, Path: "/api/me/reports",
		Summary: "Problems I have reported", Tags: []string{"Feedback"},
	}, func(ctx context.Context, _ *struct{}) (*reportsOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		out := &reportsOut{}
		out.Body.Reports = []ReportSummary{}
		err = d.tx(ctx, tenancy.Scope{UserID: id.ID}, func(q *db.Queries, _ *sql.Tx) error {
			rows, err := q.ListReportsByReporter(ctx, &id.ID)
			if err != nil {
				return huma.Error500InternalServerError("could not list reports")
			}
			for _, r := range rows {
				out.Body.Reports = append(out.Body.Reports, ReportSummary{Key: r.Key, Title: r.Title, Status: r.Status, IssueType: r.IssueType, CreatedAt: r.CreatedAt})
			}
			return nil
		})
		return out, err
	})
}
