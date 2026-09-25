package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

type McpTokenOut struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func toTokenOut(t db.McpToken) McpTokenOut {
	return McpTokenOut{ID: t.ID, Name: t.Name, Scopes: scopes.Parse(t.Scopes), LastUsedAt: t.LastUsedAt,
		ExpiresAt: t.ExpiresAt, RevokedAt: t.RevokedAt, CreatedAt: t.CreatedAt}
}

func (d *deps) registerTokens(api huma.API) {
	type tokensListOut struct {
		Body struct {
			Tokens []McpTokenOut `json:"tokens"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-mcp-tokens", Method: http.MethodGet, Path: "/api/teams/{team}/mcp-tokens",
		Summary: "The team's MCP tokens (never the secret)", Tags: []string{"MCP"},
	}, func(ctx context.Context, in *TeamPath) (*tokensListOut, error) {
		out := &tokensListOut{}
		out.Body.Tokens = []McpTokenOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTokens, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListMcpTokensOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list tokens")
			}
			for _, r := range rows {
				out.Body.Tokens = append(out.Body.Tokens, toTokenOut(r))
			}
			return nil
		})
		return out, err
	})

	type tokenCreatedOut struct {
		Body struct {
			McpTokenOut
			Token string `json:"token" doc:"The secret. Shown once; only its SHA-256 is stored."`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "create-mcp-token", Method: http.MethodPost, Path: "/api/teams/{team}/mcp-tokens",
		DefaultStatus: http.StatusCreated, Summary: "Mint a team MCP token (owner or admin)", Tags: []string{"MCP"},
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body struct {
			Name          string   `json:"name" minLength:"1" maxLength:"80"`
			Scopes        []string `json:"scopes" minItems:"1" maxItems:"5" uniqueItems:"true" enum:"read,deploy,write,secrets:read,destructive"`
			ExpiresInDays int      `json:"expires_in_days" minimum:"1" maximum:"365" default:"90"`
		}
	}) (*tokenCreatedOut, error) {
		out := &tokenCreatedOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTokens, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			granted, ok := scopes.Normalize(in.Body.Scopes)
			if !ok || len(granted) == 0 {
				return huma.Error422UnprocessableEntity("unknown scope")
			}
			days := in.Body.ExpiresInDays
			if days <= 0 {
				days = 90
			}
			plain, hash := tokens.New(tokens.PrefixUserMCP)
			exp := time.Now().Add(time.Duration(days) * 24 * time.Hour).UTC()
			row, err := q.InsertMcpToken(ctx, db.InsertMcpTokenParams{
				TeamID: m.TeamID, UserID: id.ID, Name: strings.TrimSpace(in.Body.Name),
				TokenHash: hash, Scopes: scopes.Join(granted), ExpiresAt: &exp,
			})
			if err != nil {
				return huma.Error500InternalServerError("could not create the token")
			}
			if err := tenancy.Record(ctx, q, m.TeamID, id.ID, "mcp_token.created", "mcp_token", row.ID,
				map[string]any{"name": row.Name, "scopes": granted, "expires_at": exp}); err != nil {
				return huma.Error500InternalServerError("audit failed")
			}
			out.Body.McpTokenOut = toTokenOut(row)
			out.Body.Token = plain
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "revoke-mcp-token", Method: http.MethodDelete, Path: "/api/teams/{team}/mcp-tokens/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Revoke a team MCP token (owner or admin)", Tags: []string{"MCP"},
	}, func(ctx context.Context, in *struct {
		TeamPath
		ID string `path:"id" maxLength:"64"`
	}) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTokens, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			n, err := q.RevokeMcpToken(ctx, db.RevokeMcpTokenParams{ID: in.ID, TeamID: m.TeamID})
			if err != nil || n == 0 {
				return errNotFound
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "mcp_token.revoked", "mcp_token", in.ID, nil)
		})
		return nil, err
	})

	// rotate-mcp-token (MH-580 J7): mint a new token with the same name/scopes/lifetime, revoke the
	// old one, and return the new secret once — the same "issue, then kill the old one" shape as
	// database user password rotation. Never partial: if the new token cannot be inserted, the old
	// one is left alone; only once it exists is the old one revoked.
	type tokenRotatedOut struct {
		Body struct {
			McpTokenOut
			Token string `json:"token" doc:"The new secret. Shown once."`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "rotate-mcp-token", Method: http.MethodPost, Path: "/api/teams/{team}/mcp-tokens/{id}/rotate",
		Summary: "Rotate a team MCP token: mint a replacement with the same name and scopes, then revoke this one (owner or admin)", Tags: []string{"MCP"},
	}, func(ctx context.Context, in *struct {
		TeamPath
		ID string `path:"id" maxLength:"64"`
	}) (*tokenRotatedOut, error) {
		out := &tokenRotatedOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTokens, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			old, err := q.GetMcpToken(ctx, in.ID)
			if err != nil || old.TeamID != m.TeamID || old.RevokedAt != nil {
				return errNotFound
			}
			days := 90
			if old.ExpiresAt != nil {
				if d := time.Until(*old.ExpiresAt); d > 0 {
					days = int(d.Hours()/24) + 1
				}
			}
			plain, hash := tokens.New(tokens.PrefixUserMCP)
			exp := time.Now().Add(time.Duration(days) * 24 * time.Hour).UTC()
			row, err := q.InsertMcpToken(ctx, db.InsertMcpTokenParams{
				TeamID: m.TeamID, UserID: id.ID, Name: old.Name, TokenHash: hash, Scopes: old.Scopes, ExpiresAt: &exp,
			})
			if err != nil {
				return huma.Error500InternalServerError("could not mint the replacement token")
			}
			if _, err := q.RevokeMcpToken(ctx, db.RevokeMcpTokenParams{ID: old.ID, TeamID: m.TeamID}); err != nil {
				return huma.Error500InternalServerError("could not revoke the previous token")
			}
			if err := tenancy.Record(ctx, q, m.TeamID, id.ID, "mcp_token.rotated", "mcp_token", row.ID,
				map[string]any{"name": row.Name, "previous_id": old.ID}); err != nil {
				return huma.Error500InternalServerError("audit failed")
			}
			out.Body.McpTokenOut = toTokenOut(row)
			out.Body.Token = plain
			return nil
		})
		return out, err
	})
}
