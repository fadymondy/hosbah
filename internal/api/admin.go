package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/tenancy"
	"github.com/fadymondy/cloudy/internal/tokens"
)

type AdminTokenOut struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func toAdminTokenOut(t db.AdminMcpToken) AdminTokenOut {
	return AdminTokenOut{ID: t.ID, Name: t.Name, LastUsedAt: t.LastUsedAt, ExpiresAt: t.ExpiresAt, RevokedAt: t.RevokedAt, CreatedAt: t.CreatedAt}
}

// asPlatformAdmin runs fn with platform-administration context after re-checking the
// account's role in the database. The route tier already required the session's
// `admin` role; this also refuses a session minted before the account was demoted.
func (d *deps) asPlatformAdmin(ctx context.Context, fn func(q *db.Queries, userID string) error) error {
	id, err := current(ctx)
	if err != nil {
		return err
	}
	u, err := db.New(d.a.SQLDB).UserByID(ctx, id.ID)
	if err != nil || !HasRoleCSV(u.Roles, "admin") {
		return huma.Error403Forbidden("platform administrators only")
	}
	return d.tx(ctx, tenancy.Scope{UserID: id.ID, PlatformAdmin: true}, func(q *db.Queries, _ *sql.Tx) error {
		return fn(q, id.ID)
	})
}

func (d *deps) registerAdmin(api huma.API) {
	type adminTokensListOut struct {
		Body struct {
			Tokens []AdminTokenOut `json:"tokens"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-admin-mcp-tokens", Method: http.MethodGet, Path: "/api/admin/mcp-tokens",
		Summary: "Platform-admin MCP tokens", Tags: []string{"Admin"},
	}, func(ctx context.Context, _ *struct{}) (*adminTokensListOut, error) {
		out := &adminTokensListOut{}
		out.Body.Tokens = []AdminTokenOut{}
		err := d.asPlatformAdmin(ctx, func(q *db.Queries, _ string) error {
			rows, err := q.ListAllAdminMcpTokens(ctx)
			if err != nil {
				return huma.Error500InternalServerError("could not list tokens")
			}
			for _, r := range rows {
				out.Body.Tokens = append(out.Body.Tokens, toAdminTokenOut(r))
			}
			return nil
		})
		return out, err
	})

	type adminTokenCreatedOut struct {
		Body struct {
			AdminTokenOut
			Token string `json:"token" doc:"The secret. Shown once; only its SHA-256 is stored."`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "create-admin-mcp-token", Method: http.MethodPost, Path: "/api/admin/mcp-tokens",
		DefaultStatus: http.StatusCreated, Summary: "Mint a platform-admin MCP token", Tags: []string{"Admin"},
	}, func(ctx context.Context, in *struct {
		Body struct {
			Name          string `json:"name" minLength:"1" maxLength:"80"`
			ExpiresInDays int    `json:"expires_in_days" minimum:"1" maximum:"90" default:"30"`
		}
	}) (*adminTokenCreatedOut, error) {
		out := &adminTokenCreatedOut{}
		err := d.asPlatformAdmin(ctx, func(q *db.Queries, userID string) error {
			days := in.Body.ExpiresInDays
			if days <= 0 {
				days = 30
			}
			plain, hash := tokens.New(tokens.PrefixAdminMCP)
			exp := time.Now().Add(time.Duration(days) * 24 * time.Hour).UTC()
			row, err := q.InsertAdminMcpToken(ctx, db.InsertAdminMcpTokenParams{UserID: userID, Name: strings.TrimSpace(in.Body.Name), TokenHash: hash, ExpiresAt: &exp})
			if err != nil {
				return huma.Error500InternalServerError("could not create the token")
			}
			out.Body.AdminTokenOut = toAdminTokenOut(row)
			out.Body.Token = plain
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "revoke-admin-mcp-token", Method: http.MethodDelete, Path: "/api/admin/mcp-tokens/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Revoke a platform-admin MCP token", Tags: []string{"Admin"},
	}, func(ctx context.Context, in *struct {
		ID string `path:"id" maxLength:"64"`
	}) (*struct{}, error) {
		err := d.asPlatformAdmin(ctx, func(q *db.Queries, _ string) error {
			n, err := q.RevokeAdminMcpToken(ctx, in.ID)
			if err != nil || n == 0 {
				return errNotFound
			}
			return nil
		})
		return nil, err
	})
}
