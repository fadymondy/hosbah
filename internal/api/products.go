// SaaS products (MH-576 D7): a team turns an app it runs into a product, then provisions,
// suspends, resumes, upgrades and destroys per-customer instances of it. Every instance is just
// an app (internal/deploy), so its logs, deployments, domains and env keep working normally —
// this file only adds the product/instance bookkeeping in internal/saasify. The public,
// product-key-authenticated endpoint for a team's own billing/signup system lives in
// internal/api/product_keys_public.go, not here (it never goes through d.inTeam).
package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/resources"
	"github.com/fadymondy/hosbah/internal/saasify"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ---- contract --------------------------------------------------------------------------------

// ProductPath addresses one product of a team (id or name).
type ProductPath struct {
	TeamPath
	Product string `path:"product" maxLength:"64"`
}

// ProductInstancePath addresses one instance of a product.
type ProductInstancePath struct {
	ProductPath
	Instance string `path:"instance" maxLength:"64"`
}

// ProductKeyPath addresses one key of a product.
type ProductKeyPath struct {
	ProductPath
	Key string `path:"key" maxLength:"64"`
}

// ProductUpgradePath addresses one upgrade run of a product.
type ProductUpgradePath struct {
	ProductPath
	Upgrade string `path:"upgrade" maxLength:"64"`
}

func productErr(err error, fallback string) error {
	if err == nil {
		return nil
	}
	if se, ok := err.(*saasify.ActionError); ok {
		switch se.Status {
		case http.StatusNotFound:
			return huma.Error404NotFound(se.Msg)
		case http.StatusConflict:
			return huma.Error409Conflict(se.Msg)
		case http.StatusUnprocessableEntity:
			return huma.Error422UnprocessableEntity(se.Msg)
		}
	}
	return actionErr(err, fallback)
}

func productIn(ctx context.Context, q *db.Queries, m *tenancy.Membership, ref string) (db.Product, error) {
	p, err := saasify.ProductByRef(ctx, q, m.TeamID, ref)
	if err != nil {
		return db.Product{}, errNotFound
	}
	return p, nil
}

func instanceIn(ctx context.Context, q *db.Queries, m *tenancy.Membership, p db.Product, ref string) (db.ProductInstance, error) {
	i, err := saasify.InstanceByRef(ctx, q, m.TeamID, p.ID, ref)
	if err != nil {
		return db.ProductInstance{}, errNotFound
	}
	return i, nil
}

func (d *deps) registerProducts(api huma.API) {
	tags := []string{"Products"}

	// ---- products ----

	type productsOut struct {
		Body struct {
			Products []resources.ProductResponse `json:"products"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-products", Method: http.MethodGet, Path: "/api/teams/{team}/products",
		Summary: "The team's SaaS products", Tags: tags,
	}, func(ctx context.Context, in *TeamPath) (*productsOut, error) {
		out := &productsOut{}
		out.Body.Products = []resources.ProductResponse{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListProductsOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list products")
			}
			out.Body.Products = resources.TransformProducts(rows)
			return nil
		})
		return out, err
	})

	type createProductIn struct {
		TeamPath
		Body struct {
			Name               string            `json:"name" minLength:"1" maxLength:"32" doc:"Lowercase letters, digits and dashes; unique in the team."`
			Title              string            `json:"title,omitempty" maxLength:"80"`
			Description        string            `json:"description,omitempty" maxLength:"280"`
			SourceKind         string            `json:"source_kind,omitempty" enum:"app,recipe,repo"`
			SourceRef          string            `json:"source_ref,omitempty" maxLength:"120" doc:"The app name or catalogue recipe key this product was made from, for display."`
			RepoURL            string            `json:"repo_url" minLength:"1" maxLength:"500"`
			Branch             string            `json:"branch,omitempty" maxLength:"200"`
			Runtime            string            `json:"runtime,omitempty" maxLength:"60"`
			Port               int64             `json:"port,omitempty" minimum:"0" maximum:"65535"`
			HealthcheckPath    string            `json:"healthcheck_path,omitempty" maxLength:"200"`
			HealthcheckTimeout int64             `json:"healthcheck_timeout,omitempty" minimum:"0" maximum:"600"`
			ResourcesMemoryMb  *int64            `json:"resources_memory_mb,omitempty"`
			ResourcesCpus      *float64          `json:"resources_cpus,omitempty"`
			DomainSuffix       string            `json:"domain_suffix,omitempty" maxLength:"253" doc:"Reserved for a future per-product custom domain; not wired up yet (see docs)."`
			EnvDefaults        map[string]string `json:"env_defaults,omitempty" doc:"Non-secret env applied to every instance at provision time."`
		}
	}
	type productOut struct {
		Body resources.ProductResponse
	}
	huma.Register(api, huma.Operation{OperationID: "create-product", Method: http.MethodPost, Path: "/api/teams/{team}/products",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Turn a repo (an existing app's, or a catalogue recipe's) into a SaaS product definition (owner, admin or deployer)",
	}, func(ctx context.Context, in *createProductIn) (*productOut, error) {
		out := &productOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageProducts, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			b := in.Body
			p, err := saasify.CreateProduct(ctx, q, m.TeamID, id.ID, saasify.CreateProductInput{
				Name: b.Name, Title: b.Title, Description: b.Description, SourceKind: b.SourceKind, SourceRef: b.SourceRef,
				RepoURL: b.RepoURL, Branch: b.Branch, Runtime: b.Runtime, Port: b.Port, HealthcheckPath: b.HealthcheckPath,
				HealthcheckTimeout: b.HealthcheckTimeout, ResourcesMemoryMb: b.ResourcesMemoryMb, ResourcesCpus: b.ResourcesCpus,
				DomainSuffix: b.DomainSuffix, EnvDefaults: b.EnvDefaults,
			})
			if err != nil {
				return productErr(err, "could not create the product")
			}
			out.Body = resources.TransformProduct(p)
			return nil
		})
		return out, err
	})

	type productDetailOut struct {
		Body struct {
			Product   resources.ProductResponse           `json:"product"`
			Instances []resources.ProductInstanceResponse `json:"instances"`
			Keys      []resources.ProductKeyResponse       `json:"keys"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "get-product", Method: http.MethodGet, Path: "/api/teams/{team}/products/{product}",
		Summary: "One product with its instances and keys", Tags: tags,
	}, func(ctx context.Context, in *ProductPath) (*productDetailOut, error) {
		out := &productDetailOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			p, err := productIn(ctx, q, m, in.Product)
			if err != nil {
				return err
			}
			out.Body.Product = resources.TransformProduct(p)
			instances, err := q.ListInstancesOfProduct(ctx, db.ListInstancesOfProductParams{ProductID: p.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not list instances")
			}
			out.Body.Instances = resources.TransformProductInstances(instances)
			keys, err := q.ListKeysOfProduct(ctx, db.ListKeysOfProductParams{ProductID: p.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not list keys")
			}
			out.Body.Keys = resources.TransformProductKeys(keys)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "archive-product", Method: http.MethodDelete, Path: "/api/teams/{team}/products/{product}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Archive a product (owner or admin): no new instances may be provisioned; existing instances are untouched",
	}, func(ctx context.Context, in *ProductPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityDestroyProducts, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			p, err := productIn(ctx, q, m, in.Product)
			if err != nil {
				return err
			}
			_, err = saasify.ArchiveProduct(ctx, q, m.TeamID, id.ID, p)
			return productErr(err, "could not archive the product")
		})
		return nil, err
	})

	// ---- instances ----

	type instancesOut struct {
		Body struct {
			Instances []resources.ProductInstanceResponse `json:"instances"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-product-instances", Method: http.MethodGet, Path: "/api/teams/{team}/products/{product}/instances",
		Summary: "A product's instances", Tags: tags,
	}, func(ctx context.Context, in *ProductPath) (*instancesOut, error) {
		out := &instancesOut{}
		out.Body.Instances = []resources.ProductInstanceResponse{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			p, err := productIn(ctx, q, m, in.Product)
			if err != nil {
				return err
			}
			rows, err := q.ListInstancesOfProduct(ctx, db.ListInstancesOfProductParams{ProductID: p.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not list instances")
			}
			out.Body.Instances = resources.TransformProductInstances(rows)
			return nil
		})
		return out, err
	})

	type provisionIn struct {
		ProductPath
		Body struct {
			Server      string            `json:"server" minLength:"1" maxLength:"64" doc:"Server id or name to deploy this instance's app on."`
			CustomerRef string            `json:"customer_ref" minLength:"1" maxLength:"120" doc:"The team's own identifier for this customer."`
			Name        string            `json:"name,omitempty" maxLength:"32" doc:"Instance slug, unique per product; derived from customer_ref if omitted."`
			Env         map[string]string `json:"env,omitempty" doc:"Overrides/extends the product's env_defaults for this instance only."`
		}
	}
	type instanceOut struct {
		Status int
		Body   resources.ProductInstanceResponse
	}
	huma.Register(api, huma.Operation{OperationID: "provision-product-instance", Method: http.MethodPost, Path: "/api/teams/{team}/products/{product}/instances",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Provision a new customer instance of a product: its own app, own env, own hostname (owner, admin or deployer)",
	}, func(ctx context.Context, in *provisionIn) (*instanceOut, error) {
		out := &instanceOut{Status: http.StatusCreated}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageProducts, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			p, err := productIn(ctx, q, m, in.Product)
			if err != nil {
				return err
			}
			ds := d.deploys()
			b := in.Body
			inst, _, err := saasify.ProvisionInstance(ctx, q, ds, m.TeamID, webActor(id), p, saasify.ProvisionInput{
				Server: b.Server, CustomerRef: b.CustomerRef, Name: b.Name, EnvOverride: b.Env,
			})
			if err != nil {
				return productErr(err, "could not provision the instance")
			}
			out.Body = resources.TransformProductInstance(inst)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "suspend-product-instance", Method: http.MethodPost, Path: "/api/teams/{team}/products/{product}/instances/{instance}/suspend",
		Summary: "Suspend an instance: no more deploys until resumed (owner, admin or deployer)", Tags: tags,
	}, func(ctx context.Context, in *ProductInstancePath) (*instanceOut, error) {
		out := &instanceOut{Status: http.StatusOK}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageProducts, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			p, err := productIn(ctx, q, m, in.Product)
			if err != nil {
				return err
			}
			inst, err := instanceIn(ctx, q, m, p, in.Instance)
			if err != nil {
				return err
			}
			inst, err = saasify.SuspendInstance(ctx, q, d.deploys(), m.TeamID, webActor(id), p, inst)
			if err != nil {
				return productErr(err, "could not suspend the instance")
			}
			out.Body = resources.TransformProductInstance(inst)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "resume-product-instance", Method: http.MethodPost, Path: "/api/teams/{team}/products/{product}/instances/{instance}/resume",
		Summary: "Resume a suspended instance (owner, admin or deployer)", Tags: tags,
	}, func(ctx context.Context, in *ProductInstancePath) (*instanceOut, error) {
		out := &instanceOut{Status: http.StatusOK}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageProducts, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			p, err := productIn(ctx, q, m, in.Product)
			if err != nil {
				return err
			}
			inst, err := instanceIn(ctx, q, m, p, in.Instance)
			if err != nil {
				return err
			}
			inst, err = saasify.ResumeInstance(ctx, q, d.deploys(), m.TeamID, webActor(id), p, inst)
			if err != nil {
				return productErr(err, "could not resume the instance")
			}
			out.Body = resources.TransformProductInstance(inst)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "destroy-product-instance", Method: http.MethodDelete, Path: "/api/teams/{team}/products/{product}/instances/{instance}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Destroy an instance (owner or admin; confirm = its name): its app, containers and images go too",
	}, func(ctx context.Context, in *struct {
		ProductInstancePath
		Body struct {
			Confirm string `json:"confirm" minLength:"1" maxLength:"32" doc:"The instance's name, exactly."`
		}
	}) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityDestroyProducts, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			p, err := productIn(ctx, q, m, in.Product)
			if err != nil {
				return err
			}
			inst, err := instanceIn(ctx, q, m, p, in.Instance)
			if err != nil {
				return err
			}
			_, err = saasify.DestroyInstance(ctx, q, d.deploys(), m.TeamID, webActor(id), p, inst, in.Body.Confirm)
			return productErr(err, "could not destroy the instance")
		})
		return nil, err
	})

	// ---- upgrades ----

	type upgradeOut struct {
		Body resources.ProductUpgradeResponse
	}
	huma.Register(api, huma.Operation{OperationID: "upgrade-product", Method: http.MethodPost, Path: "/api/teams/{team}/products/{product}/upgrades",
		DefaultStatus: http.StatusAccepted, Tags: tags,
		Summary: "Roll every active instance to a new ref, one at a time with a health gate (owner, admin or deployer)",
	}, func(ctx context.Context, in *struct {
		ProductPath
		Body struct {
			Ref string `json:"ref,omitempty" maxLength:"200" doc:"Branch, tag or commit; defaults to the product's branch."`
		}
	}) (*upgradeOut, error) {
		out := &upgradeOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageProducts, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			p, err := productIn(ctx, q, m, in.Product)
			if err != nil {
				return err
			}
			up, err := saasify.UpgradeProduct(ctx, d.a.SQLDB, d.deploys(), m.TeamID, webActor(id), p, in.Body.Ref)
			if err != nil {
				return productErr(err, "could not start the upgrade")
			}
			out.Body = resources.TransformProductUpgrade(up)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "get-product-upgrade", Method: http.MethodGet, Path: "/api/teams/{team}/products/{product}/upgrades/{upgrade}",
		Summary: "Progress of one upgrade run", Tags: tags,
	}, func(ctx context.Context, in *ProductUpgradePath) (*upgradeOut, error) {
		out := &upgradeOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			u, err := q.ProductUpgradeInTeam(ctx, db.ProductUpgradeInTeamParams{ID: in.Upgrade, TeamID: m.TeamID})
			if err != nil {
				return errNotFound
			}
			out.Body = resources.TransformProductUpgrade(u)
			return nil
		})
		return out, err
	})

	// ---- keys ----

	type mintKeyIn struct {
		ProductPath
		Body struct {
			Name       string `json:"name,omitempty" maxLength:"80"`
			WebhookURL string `json:"webhook_url,omitempty" maxLength:"500" doc:"Receives a signed POST (X-Hosbah-Signature: sha256=hex of HMAC-SHA256 over the raw body) on every instance state change."`
		}
	}
	type mintKeyOut struct {
		Status int
		Body   struct {
			Key           resources.ProductKeyResponse `json:"key"`
			Token         string                       `json:"token" doc:"hbk_… — shown once."`
			WebhookSecret string                        `json:"webhook_secret,omitempty" doc:"Shown once; sign your own verification the same way (HMAC-SHA256 hex)."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "mint-product-key", Method: http.MethodPost, Path: "/api/teams/{team}/products/{product}/keys",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Mint a product-scoped API key for the team's own billing/signup system (owner or admin)",
	}, func(ctx context.Context, in *mintKeyIn) (*mintKeyOut, error) {
		out := &mintKeyOut{Status: http.StatusCreated}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityDestroyProducts, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			p, err := productIn(ctx, q, m, in.Product)
			if err != nil {
				return err
			}
			var dek []byte
			if in.Body.WebhookURL != "" {
				dek, err = d.ops.TeamDEK(ctx, q, m.TeamID)
				if err != nil {
					return huma.Error500InternalServerError("could not load the team key")
				}
			}
			k, tok, secret, err := saasify.MintKey(ctx, q, m.TeamID, id.ID, p, saasify.MintKeyInput{Name: in.Body.Name, WebhookURL: in.Body.WebhookURL, DEK: dek})
			if err != nil {
				return productErr(err, "could not mint the key")
			}
			out.Body.Key, out.Body.Token, out.Body.WebhookSecret = resources.TransformProductKey(k), tok, secret
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "revoke-product-key", Method: http.MethodPost, Path: "/api/teams/{team}/products/{product}/keys/{key}/revoke",
		Summary: "Revoke a product key at once (owner or admin)", Tags: tags,
	}, func(ctx context.Context, in *ProductKeyPath) (*struct {
		Body resources.ProductKeyResponse
	}, error) {
		out := &struct {
			Body resources.ProductKeyResponse
		}{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityDestroyProducts, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			p, err := productIn(ctx, q, m, in.Product)
			if err != nil {
				return err
			}
			k, err := q.ProductKeyInTeam(ctx, db.ProductKeyInTeamParams{ID: in.Key, TeamID: m.TeamID})
			if err != nil || k.ProductID != p.ID {
				return errNotFound
			}
			k, err = saasify.RevokeKey(ctx, q, m.TeamID, id.ID, k)
			if err != nil {
				return productErr(err, "could not revoke the key")
			}
			out.Body = resources.TransformProductKey(k)
			return nil
		})
		return out, err
	})
}
