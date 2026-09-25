// The public, product-key-authenticated instance provisioning API (MH-576 D7): a team's own
// billing/signup system calls this — never a browser session — to create, suspend, resume and
// delete instances of exactly the one product its key was minted for. It never goes through
// d.inTeam (there is no session); every call resolves its own team and product from the bearer
// token first and every query after that names both team_id and product_id.
//
// Wired into the public tier in internal/server/authorization.go (prefix /api/products/), the
// same shape as the RUM beacon and OTLP ingest endpoints: a secret bearer key authenticates the
// request, not a path segment or query param.
package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/resources"
	"github.com/fadymondy/hosbah/internal/saasify"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

func bearerToken(auth string) string {
	const p = "Bearer "
	if len(auth) > len(p) && strings.EqualFold(auth[:len(p)], p) {
		return strings.TrimSpace(auth[len(p):])
	}
	return ""
}

// keyAuth resolves the bearer product key, rate-limits it, and runs fn in a transaction scoped
// to exactly that key's team. The team isn't known until the key is looked up, so the transaction
// opens scoped to the token's hash instead (tenancy.Scope{TokenHash: hash}) — the same pattern as
// the RUM beacon (internal/rum/public.go) and OTLP ingest (internal/apm/ingest.go) endpoints — and
// saasify.ResolveKey switches it to the key's team as soon as the key row is found. That token-hash
// scope is backed by the product_keys_token_select RLS policy (never System — a product key never
// reaches another team's row, but it also never needs the extra system policies).
func (d *deps) keyAuth(ctx context.Context, authHeader string, fn func(q *db.Queries, k db.ProductKey, p db.Product) error) error {
	tok := bearerToken(authHeader)
	if tok == "" {
		return huma.Error401Unauthorized("missing bearer token")
	}
	if !saasify.KeyRateLimiter.Allow(saasify.HashForRateLimit(tok)) {
		return huma.Error429TooManyRequests("too many requests")
	}
	hash := tokens.Hash(tok)
	return tenancy.Tx(ctx, d.a.SQLDB, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
		k, p, err := saasify.ResolveKey(ctx, q, tx, tok)
		if err != nil {
			return productErr(err, "invalid key")
		}
		_ = q.TouchProductKey(ctx, k.ID)
		return fn(q, k, p)
	})
}

func keyActor(k db.ProductKey) deploy.Actor { return deploy.Actor{UserID: "product_key:" + k.ID, Via: "hook"} }

func (d *deps) registerProductKeysPublic(api huma.API) {
	tags := []string{"Products (product key)"}

	type instanceOut struct {
		Status int
		Body   resources.ProductInstanceResponse
	}

	huma.Register(api, huma.Operation{OperationID: "product-key-provision-instance", Method: http.MethodPost, Path: "/api/products/instances",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Provision a new customer instance of the key's product (Bearer hbk_… key)",
	}, func(ctx context.Context, in *struct {
		Authorization string `header:"Authorization"`
		Body          struct {
			Server      string            `json:"server" minLength:"1" maxLength:"64"`
			CustomerRef string            `json:"customer_ref" minLength:"1" maxLength:"120"`
			Name        string            `json:"name,omitempty" maxLength:"32"`
			Env         map[string]string `json:"env,omitempty"`
		}
	}) (*instanceOut, error) {
		out := &instanceOut{Status: http.StatusCreated}
		err := d.keyAuth(ctx, in.Authorization, func(q *db.Queries, k db.ProductKey, p db.Product) error {
			inst, _, err := saasify.ProvisionInstance(ctx, q, d.deploys(), p.TeamID, keyActor(k), p, saasify.ProvisionInput{
				Server: in.Body.Server, CustomerRef: in.Body.CustomerRef, Name: in.Body.Name, EnvOverride: in.Body.Env,
			})
			if err != nil {
				return productErr(err, "could not provision the instance")
			}
			out.Body = resources.TransformProductInstance(inst)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "product-key-suspend-instance", Method: http.MethodPost, Path: "/api/products/instances/{instance}/suspend",
		Summary: "Suspend an instance of the key's product (Bearer hbk_… key)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Authorization string `header:"Authorization"`
		Instance      string `path:"instance" maxLength:"64"`
	}) (*instanceOut, error) {
		out := &instanceOut{Status: http.StatusOK}
		err := d.keyAuth(ctx, in.Authorization, func(q *db.Queries, k db.ProductKey, p db.Product) error {
			inst, err := saasify.InstanceByRef(ctx, q, p.TeamID, p.ID, in.Instance)
			if err != nil {
				return errNotFound
			}
			inst, err = saasify.SuspendInstance(ctx, q, d.deploys(), p.TeamID, keyActor(k), p, inst)
			if err != nil {
				return productErr(err, "could not suspend the instance")
			}
			out.Body = resources.TransformProductInstance(inst)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "product-key-resume-instance", Method: http.MethodPost, Path: "/api/products/instances/{instance}/resume",
		Summary: "Resume an instance of the key's product (Bearer hbk_… key)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Authorization string `header:"Authorization"`
		Instance      string `path:"instance" maxLength:"64"`
	}) (*instanceOut, error) {
		out := &instanceOut{Status: http.StatusOK}
		err := d.keyAuth(ctx, in.Authorization, func(q *db.Queries, k db.ProductKey, p db.Product) error {
			inst, err := saasify.InstanceByRef(ctx, q, p.TeamID, p.ID, in.Instance)
			if err != nil {
				return errNotFound
			}
			inst, err = saasify.ResumeInstance(ctx, q, d.deploys(), p.TeamID, keyActor(k), p, inst)
			if err != nil {
				return productErr(err, "could not resume the instance")
			}
			out.Body = resources.TransformProductInstance(inst)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "product-key-destroy-instance", Method: http.MethodDelete, Path: "/api/products/instances/{instance}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Destroy an instance of the key's product (Bearer hbk_… key; confirm = the instance's name)",
	}, func(ctx context.Context, in *struct {
		Authorization string `header:"Authorization"`
		Instance      string `path:"instance" maxLength:"64"`
		Body          struct {
			Confirm string `json:"confirm" minLength:"1" maxLength:"32"`
		}
	}) (*struct{}, error) {
		err := d.keyAuth(ctx, in.Authorization, func(q *db.Queries, k db.ProductKey, p db.Product) error {
			inst, err := saasify.InstanceByRef(ctx, q, p.TeamID, p.ID, in.Instance)
			if err != nil {
				return errNotFound
			}
			_, err = saasify.DestroyInstance(ctx, q, d.deploys(), p.TeamID, keyActor(k), p, inst, in.Body.Confirm)
			return productErr(err, "could not destroy the instance")
		})
		return nil, err
	})
}
