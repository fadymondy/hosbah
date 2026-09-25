package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/saasify"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ProductTools are the D7 SaaS-product tools: turn an app or a repo into a product, then
// provision, suspend, resume, upgrade and destroy per-customer instances of it. They call the
// same internal/saasify functions as the REST API (internal/api/products.go), so validation,
// roles and audit are one implementation.
func ProductTools() []Tool {
	productArg := str("Product name or id")
	return []Tool{
		{
			Name: "list_products", Scope: scopes.Read,
			Description: "The team's SaaS products: name, source repo/branch, status, instance count.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				rows, err := env.Q.ListProductsOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list products")
				}
				if len(rows) == 0 {
					return "No products yet. save_product turns an app's repo into one.", nil
				}
				var b strings.Builder
				for _, p := range rows {
					n, _ := env.Q.CountInstancesOfProduct(ctx, p.ID)
					fmt.Fprintf(&b, "  %-20s %-8s %s @ %s  instances %d  (id %s)\n", p.Name, p.Status, p.RepoURL, p.Branch, n, p.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "save_product", Scope: scopes.Write,
			Description: "Create a SaaS product: a repo + branch (an existing app's, or any public git URL) that provision_instance can spin up per-customer copies of.",
			InputSchema: schema([]string{"name", "repo_url"}, map[string]any{
				"name":     str("Lowercase letters, digits and dashes (up to 32)"),
				"repo_url": str("A public git URL"),
				"branch":   str("Default main"),
				"port":     map[string]any{"type": "integer", "description": "Default 8080"},
				"title":    str("Display name"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageProducts); err != nil {
					return "", err
				}
				p, err := saasify.CreateProduct(ctx, env.Q, env.Caller.TeamID, env.Caller.UserID, saasify.CreateProductInput{
					Name: args.Str("name"), Title: args.Str("title"), RepoURL: args.Str("repo_url"), Branch: args.Str("branch"),
					Port: int64(args.Int("port", 8080, 1, 65535)),
				})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Created product %s (id %s).", p.Name, p.ID), nil
			},
		},
		{
			Name: "product_instances", Scope: scopes.Read,
			Description: "A product's instances: customer, hostname, status.",
			InputSchema: schema([]string{"product"}, map[string]any{"product": productArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				p, err := findProduct(ctx, env, args)
				if err != nil {
					return "", err
				}
				rows, err := env.Q.ListInstancesOfProduct(ctx, db.ListInstancesOfProductParams{ProductID: p.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", errors.New("could not list instances")
				}
				if len(rows) == 0 {
					return "No instances yet. provision_instance adds one.", nil
				}
				var b strings.Builder
				for _, i := range rows {
					host := "—"
					if i.Hostname != nil {
						host = *i.Hostname
					}
					fmt.Fprintf(&b, "  %-20s %-10s customer %-24s %s  (id %s)\n", i.Name, i.Status, i.CustomerRef, host, i.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "provision_instance", Scope: scopes.Deploy,
			Description: "Provision a new customer's instance of a product: its own app, own env, own hostname, first deploy queued at once.",
			InputSchema: schema([]string{"product", "server", "customer_ref"}, map[string]any{
				"product":      productArg,
				"server":       str("Server id or name to deploy this instance on"),
				"customer_ref": str("The team's own identifier for this customer"),
				"name":         str("Instance slug; derived from customer_ref if omitted"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageProducts); err != nil {
					return "", err
				}
				p, err := findProduct(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				inst, a, err := saasify.ProvisionInstance(ctx, env.Q, s, env.Caller.TeamID, mcpActor(env), p, saasify.ProvisionInput{
					Server: args.Str("server"), CustomerRef: args.Str("customer_ref"), Name: args.Str("name"),
				})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Provisioned instance %s of %s (app %s, id %s). Its first deploy is queued.", inst.Name, p.Name, a.Name, inst.ID), nil
			},
		},
		{
			Name: "suspend_instance", Scope: scopes.Deploy,
			Description: "Suspend an instance: no more deploys until resumed.",
			InputSchema: schema([]string{"product", "instance"}, map[string]any{"product": productArg, "instance": str("Instance name or id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageProducts); err != nil {
					return "", err
				}
				p, inst, err := findInstance(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				if _, err := saasify.SuspendInstance(ctx, env.Q, s, env.Caller.TeamID, mcpActor(env), p, inst); err != nil {
					return "", err
				}
				return "Suspended " + inst.Name + ".", nil
			},
		},
		{
			Name: "resume_instance", Scope: scopes.Deploy,
			Description: "Resume a suspended instance.",
			InputSchema: schema([]string{"product", "instance"}, map[string]any{"product": productArg, "instance": str("Instance name or id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageProducts); err != nil {
					return "", err
				}
				p, inst, err := findInstance(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				if _, err := saasify.ResumeInstance(ctx, env.Q, s, env.Caller.TeamID, mcpActor(env), p, inst); err != nil {
					return "", err
				}
				return "Resumed " + inst.Name + ".", nil
			},
		},
		{
			Name: "upgrade_product", Scope: scopes.Deploy,
			Description: "Roll every active instance of a product to a new ref, one at a time with a health gate. Returns an upgrade id; poll product_instances for progress.",
			InputSchema: schema([]string{"product"}, map[string]any{"product": productArg, "ref": str("Branch, tag or commit; defaults to the product's branch")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageProducts); err != nil {
					return "", err
				}
				p, err := findProduct(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				if env.App == nil || env.App.SQLDB == nil {
					return "", errors.New("the database is not available")
				}
				up, err := saasify.UpgradeProduct(ctx, env.App.SQLDB, s, env.Caller.TeamID, mcpActor(env), p, args.Str("ref"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Upgrade %s started for %d instance(s).", up.ID, up.Total), nil
			},
		},
		{
			Name: "mint_product_key", Scope: scopes.Write,
			Description: "Mint a product-scoped API key (hbk_…) for the team's own billing/signup system to call the public instance-provisioning endpoint. Shown once.",
			InputSchema: schema([]string{"product"}, map[string]any{"product": productArg, "name": str("Key label"),
				"webhook_url": str("Receives a signed POST on every instance state change")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityDestroyProducts); err != nil {
					return "", err
				}
				p, err := findProduct(ctx, env, args)
				if err != nil {
					return "", err
				}
				var dek []byte
				if url := args.Str("webhook_url"); url != "" && env.App != nil {
					if o, ok := env.App.Ops.(interface {
						TeamDEK(context.Context, *db.Queries, string) ([]byte, error)
					}); ok {
						dek, _ = o.TeamDEK(ctx, env.Q, env.Caller.TeamID)
					}
				}
				k, tok, secret, err := saasify.MintKey(ctx, env.Q, env.Caller.TeamID, env.Caller.UserID, p, saasify.MintKeyInput{
					Name: args.Str("name"), WebhookURL: args.Str("webhook_url"), DEK: dek,
				})
				if err != nil {
					return "", err
				}
				out := fmt.Sprintf("Minted key %s: %s\nUse it as Authorization: Bearer %s against POST /api/products/instances.", k.Name, tok, tok)
				if secret != "" {
					out += "\nWebhook secret (HMAC-SHA256 hex over the raw body, header X-Hosbah-Signature): " + secret
				}
				return out, nil
			},
		},
		// ---- destructive ----
		{
			Name: "destroy_instance", Scope: scopes.Destructive, Destructive: true, ResourceType: "product_instance",
			Description: "Destroy a customer's instance: its app, containers and images go too. confirm must equal the instance's name.",
			InputSchema: schema([]string{"product", "instance", "confirm"}, map[string]any{
				"product": productArg, "instance": str("Instance name or id"), "confirm": str("The instance's name, exactly"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityDestroyProducts); err != nil {
					return "", err
				}
				p, inst, err := findInstance(ctx, env, args)
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, inst.Name); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				if _, err := saasify.DestroyInstance(ctx, env.Q, s, env.Caller.TeamID, mcpActor(env), p, inst, args.Str("confirm")); err != nil {
					return "", err
				}
				return "Destroyed " + inst.Name + ".", nil
			},
		},
	}
}

func findProduct(ctx context.Context, env *Env, args Args) (db.Product, error) {
	ref := args.Str("product")
	if ref == "" {
		return db.Product{}, errors.New("product is required (a name or an id)")
	}
	return saasify.ProductByRef(ctx, env.Q, env.Caller.TeamID, ref)
}

func findInstance(ctx context.Context, env *Env, args Args) (db.Product, db.ProductInstance, error) {
	p, err := findProduct(ctx, env, args)
	if err != nil {
		return db.Product{}, db.ProductInstance{}, err
	}
	ref := args.Str("instance")
	if ref == "" {
		return db.Product{}, db.ProductInstance{}, errors.New("instance is required (a name or an id)")
	}
	inst, err := saasify.InstanceByRef(ctx, env.Q, env.Caller.TeamID, p.ID, ref)
	if err != nil {
		return db.Product{}, db.ProductInstance{}, err
	}
	return p, inst, nil
}
