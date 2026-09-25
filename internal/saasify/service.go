// Package saasify turns an app a team already runs on Hosbah into a SaaS product: isolated,
// per-customer instances of the same repo/branch, each its own app (own container(s), own env,
// own hostname), with lifecycle actions (provision, suspend, resume, upgrade, destroy).
//
// It is deliberately thin: every instance IS an app (deploy.Service.CreateApp), so logs, metrics,
// backups, domains and deployments for an instance keep working exactly like any other app. This
// package only adds the products/product_instances/product_keys/product_upgrades bookkeeping and
// the sequencing (provision → attach, upgrade-all one at a time with a health gate via the normal
// deploy pipeline's own gate).
//
// MH-576 D7. See internal/api/products.go (REST), internal/mcp/products.go (MCP tools) and
// internal/saasify/webhook.go (signed webhook delivery to a product key's URL).
package saasify

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

// ActionError carries an HTTP-ish status, mirroring deploy.ActionError (unexported there).
type ActionError struct {
	Status int
	Msg    string
}

func (e *ActionError) Error() string { return e.Msg }

func notFound(what string) error { return &ActionError{Status: 404, Msg: what + " not found"} }
func conflict(msg string) error  { return &ActionError{Status: 409, Msg: msg} }
func invalid(msg string) error   { return &ActionError{Status: 422, Msg: msg} }

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func validSlug(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-'
		if i == 0 && r == '-' {
			return false
		}
		if !ok {
			return false
		}
	}
	return true
}

// ---- products -------------------------------------------------------------------------------

// CreateProductInput is what the "turn this app into a product" wizard sends. Source is always a
// repo + branch — for source_kind "app" the caller resolves an existing app's repo/branch first;
// for "recipe" a catalog.RecipeStack's Repo/Branch/Runtime.
type CreateProductInput struct {
	Name               string
	Title              string
	Description        string
	SourceKind         string // app | recipe | repo
	SourceRef          string // the app name or recipe key this was created from, for display
	RepoURL            string
	Branch             string
	Runtime            string
	Port               int64
	HealthcheckPath    string
	HealthcheckTimeout int64
	ResourcesMemoryMb  *int64
	ResourcesCpus      *float64
	DomainSuffix       string
	EnvDefaults        map[string]string
}

// CreateProduct saves a new SaaS product definition. It does not provision any instance.
func CreateProduct(ctx context.Context, q *db.Queries, teamID, actorID string, in CreateProductInput) (db.Product, error) {
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	if !validSlug(in.Name) {
		return db.Product{}, invalid("the name must be lowercase letters, digits and dashes (up to 32), not starting with a dash")
	}
	if strings.TrimSpace(in.RepoURL) == "" {
		return db.Product{}, invalid("a repo URL is required")
	}
	switch in.SourceKind {
	case "app", "recipe", "repo":
	default:
		in.SourceKind = "repo"
	}
	if in.Branch == "" {
		in.Branch = "main"
	}
	if in.Port == 0 {
		in.Port = 8080
	}
	if in.HealthcheckPath == "" {
		in.HealthcheckPath = "/"
	}
	if in.HealthcheckTimeout == 0 {
		in.HealthcheckTimeout = 60
	}
	envJSON, err := json.Marshal(in.EnvDefaults)
	if err != nil {
		return db.Product{}, invalid("env_defaults must be a flat string map")
	}
	var title, description, sourceRef, runtime, domainSuffix *string
	if in.Title != "" {
		title = &in.Title
	}
	if in.Description != "" {
		description = &in.Description
	}
	if in.SourceRef != "" {
		sourceRef = &in.SourceRef
	}
	if in.Runtime != "" {
		runtime = &in.Runtime
	}
	if in.DomainSuffix != "" {
		domainSuffix = &in.DomainSuffix
	}
	p, err := q.InsertProduct(ctx, db.InsertProductParams{
		ID: newUUID(), TeamID: teamID, Name: in.Name, Title: title, Description: description,
		SourceKind: in.SourceKind, SourceRef: sourceRef, RepoURL: in.RepoURL, Branch: in.Branch, Runtime: runtime,
		Port: in.Port, HealthcheckPath: in.HealthcheckPath, HealthcheckTimeout: in.HealthcheckTimeout,
		ResourcesMemoryMb: in.ResourcesMemoryMb, ResourcesCpus: nullFloat(in.ResourcesCpus), DomainSuffix: domainSuffix,
		EnvDefaults: string(envJSON), CreatedBy: actorID,
	})
	if err != nil {
		if strings.Contains(err.Error(), "products_team_name_key") {
			return db.Product{}, conflict("a product with that name already exists in this team")
		}
		return db.Product{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actorID, "product.created", "product", p.ID, map[string]any{
		"name": p.Name, "source_kind": p.SourceKind, "repo_url": p.RepoURL, "branch": p.Branch}); err != nil {
		return db.Product{}, err
	}
	return p, nil
}

func nullFloat(f *float64) sql.NullFloat64 {
	if f == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *f, Valid: true}
}

// ProductByRef resolves a team's product by id or name.
func ProductByRef(ctx context.Context, q *db.Queries, teamID, ref string) (db.Product, error) {
	p, err := q.ProductInTeamByRef(ctx, db.ProductInTeamByRefParams{TeamID: teamID, Ref: strings.TrimSpace(ref)})
	if err != nil {
		return db.Product{}, notFound("product")
	}
	return p, nil
}

// ArchiveProduct marks a product archived: no new instances may be provisioned, existing ones
// are untouched (they still need suspending or destroying explicitly).
func ArchiveProduct(ctx context.Context, q *db.Queries, teamID, actorID string, p db.Product) (db.Product, error) {
	out, err := q.SetProductStatus(ctx, db.SetProductStatusParams{ID: p.ID, TeamID: teamID, Status: "archived"})
	if err != nil {
		return db.Product{}, err
	}
	return out, tenancy.Record(ctx, q, teamID, actorID, "product.archived", "product", p.ID, nil)
}

// ---- instances --------------------------------------------------------------------------------

// ProvisionInput is one customer's instance of a product.
type ProvisionInput struct {
	Server      string // server id or name to deploy the instance's app on
	CustomerRef string // the team's own identifier for the customer (opaque to Hosbah)
	Name        string // instance slug, unique within the product; "" derives one from CustomerRef
	EnvOverride map[string]string
}

// ProvisionInstance creates one customer's isolated instance of a product: its own app (own
// container, own hostname on the platform apps domain), and applies the product's env defaults
// plus the instance's own overrides. Database-per-instance (internal/dbsvc) is not wired up yet —
// see the report; a product can still point every instance's DATABASE_URL env var at a shared or
// per-team database by hand until that lands.
func ProvisionInstance(ctx context.Context, q *db.Queries, ds *deploy.Service, teamID string, actor deploy.Actor, p db.Product, in ProvisionInput) (db.ProductInstance, db.App, error) {
	if p.Status != "active" {
		return db.ProductInstance{}, db.App{}, conflict("the product is archived — resume it before provisioning")
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	if in.Name == "" {
		in.Name = strings.ToLower(strings.TrimSpace(in.CustomerRef))
	}
	if !validSlug(in.Name) {
		return db.ProductInstance{}, db.App{}, invalid("the instance name must be lowercase letters, digits and dashes (up to 32)")
	}
	if strings.TrimSpace(in.CustomerRef) == "" {
		return db.ProductInstance{}, db.App{}, invalid("a customer reference is required")
	}
	inst, err := q.InsertProductInstance(ctx, db.InsertProductInstanceParams{
		ID: newUUID(), TeamID: teamID, ProductID: p.ID, CustomerRef: in.CustomerRef, Name: in.Name, CreatedBy: actor.UserID,
	})
	if err != nil {
		if strings.Contains(err.Error(), "product_instances_product_id_name_key") {
			return db.ProductInstance{}, db.App{}, conflict("an instance with that name already exists for this product")
		}
		return db.ProductInstance{}, db.App{}, err
	}
	appName := clip(p.Name+"-"+in.Name, 40)
	a, _, err := ds.CreateApp(ctx, q, teamID, actor, deploy.CreateAppInput{
		Name: appName, Server: in.Server, RepoURL: p.RepoURL, Branch: p.Branch,
		Port: int(p.Port), HealthPath: p.HealthcheckPath, HealthTimeout: int(p.HealthcheckTimeout),
	})
	if err != nil {
		_, _ = q.FailProductInstance(ctx, db.FailProductInstanceParams{ID: inst.ID, TeamID: teamID})
		return db.ProductInstance{}, db.App{}, err
	}
	env := map[string]string{}
	if err := json.Unmarshal([]byte(p.EnvDefaults), &env); err != nil {
		env = map[string]string{}
	}
	for k, v := range in.EnvOverride {
		env[k] = v
	}
	for k, v := range env {
		if err := ds.SetEnv(ctx, q, teamID, actor, a, k, v); err != nil {
			_, _ = q.FailProductInstance(ctx, db.FailProductInstanceParams{ID: inst.ID, TeamID: teamID})
			return db.ProductInstance{}, db.App{}, err
		}
	}
	host := platformHostname(ctx, q, teamID, a.ID)
	inst, err = q.AttachProductInstanceApp(ctx, db.AttachProductInstanceAppParams{
		ID: inst.ID, TeamID: teamID, AppID: &a.ID, Hostname: host,
	})
	if err != nil {
		return db.ProductInstance{}, db.App{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "product_instance.provisioned", "product_instance", inst.ID, auditExtra(actor, map[string]any{
		"product": p.Name, "customer_ref": in.CustomerRef, "app_id": a.ID})); err != nil {
		return db.ProductInstance{}, db.App{}, err
	}
	dispatchWebhook(ctx, q, teamID, p.ID, "instance.provisioned", inst)
	if _, err := ds.QueueDeployment(ctx, q, teamID, actor, a, "deploy", "", nil, ""); err != nil {
		// The instance and its app exist; the first deploy can be retried from the app page.
		return inst, a, nil
	}
	return inst, a, nil
}

// platformHostname mirrors deploy's own (unexported) platformHost: the app's platform hostname
// (VerifyToken "platform"), assigned automatically by CreateApp — or nil if none was assigned
// (no apps domain configured).
func platformHostname(ctx context.Context, q *db.Queries, teamID, appID string) *string {
	ds, err := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: appID, TeamID: teamID})
	if err != nil {
		return nil
	}
	for _, d := range ds {
		if d.VerifyToken == "platform" {
			h := d.Hostname
			return &h
		}
	}
	return nil
}

// auditExtra mirrors deploy.Actor's own unexported audit() so tenancy.Record entries look the
// same whichever package wrote them.
func auditExtra(actor deploy.Actor, extra map[string]any) map[string]any {
	if extra == nil {
		extra = map[string]any{}
	}
	if actor.Via != "" && actor.Via != "web" {
		extra["via"] = actor.Via
	}
	if actor.TokenID != "" {
		extra["mcp_token_id"] = actor.TokenID
	}
	return extra
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// InstanceByRef resolves an instance of a product by id or name, never crossing product or team.
func InstanceByRef(ctx context.Context, q *db.Queries, teamID, productID, ref string) (db.ProductInstance, error) {
	i, err := q.ProductInstanceInTeamByRef(ctx, db.ProductInstanceInTeamByRefParams{TeamID: teamID, ProductID: productID, Ref: strings.TrimSpace(ref)})
	if err != nil {
		return db.ProductInstance{}, notFound("instance")
	}
	return i, nil
}

// SuspendInstance pauses the instance's app (autodeploy paused; the container keeps its data but
// no longer serves new deploys). A full stop is left to a follow-up (see report: no agent
// container-stop op exists yet, so this reduces "suspend" to "no more deploys" — the workload
// itself keeps running until the agent grows a docker.stop op).
func SuspendInstance(ctx context.Context, q *db.Queries, ds *deploy.Service, teamID string, actor deploy.Actor, p db.Product, inst db.ProductInstance) (db.ProductInstance, error) {
	if inst.Status != "active" {
		return db.ProductInstance{}, conflict("the instance is not active")
	}
	a, err := instanceApp(ctx, q, teamID, inst)
	if err != nil {
		return db.ProductInstance{}, err
	}
	if err := ds.SetPaused(ctx, q, teamID, actor, a, true); err != nil {
		return db.ProductInstance{}, err
	}
	out, err := q.SetProductInstanceStatus(ctx, db.SetProductInstanceStatusParams{ID: inst.ID, TeamID: teamID, Status: "suspended"})
	if err != nil {
		return db.ProductInstance{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "product_instance.suspended", "product_instance", inst.ID, auditExtra(actor, nil)); err != nil {
		return db.ProductInstance{}, err
	}
	dispatchWebhook(ctx, q, teamID, p.ID, "instance.suspended", out)
	return out, nil
}

// ResumeInstance un-pauses the instance's app.
func ResumeInstance(ctx context.Context, q *db.Queries, ds *deploy.Service, teamID string, actor deploy.Actor, p db.Product, inst db.ProductInstance) (db.ProductInstance, error) {
	if inst.Status != "suspended" {
		return db.ProductInstance{}, conflict("the instance is not suspended")
	}
	a, err := instanceApp(ctx, q, teamID, inst)
	if err != nil {
		return db.ProductInstance{}, err
	}
	if err := ds.SetPaused(ctx, q, teamID, actor, a, false); err != nil {
		return db.ProductInstance{}, err
	}
	out, err := q.SetProductInstanceStatus(ctx, db.SetProductInstanceStatusParams{ID: inst.ID, TeamID: teamID, Status: "active"})
	if err != nil {
		return db.ProductInstance{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "product_instance.resumed", "product_instance", inst.ID, auditExtra(actor, nil)); err != nil {
		return db.ProductInstance{}, err
	}
	dispatchWebhook(ctx, q, teamID, p.ID, "instance.resumed", out)
	return out, nil
}

// DestroyInstance deletes the instance's app (its route, containers and images) and marks the
// instance destroyed. confirm must equal the instance's name.
func DestroyInstance(ctx context.Context, q *db.Queries, ds *deploy.Service, teamID string, actor deploy.Actor, p db.Product, inst db.ProductInstance, confirm string) (db.ProductInstance, error) {
	if confirm != inst.Name {
		return db.ProductInstance{}, invalid("confirm must equal the instance's name — nothing was destroyed")
	}
	if inst.Status == "destroyed" {
		return db.ProductInstance{}, conflict("the instance is already destroyed")
	}
	if inst.AppID != nil {
		a, err := instanceApp(ctx, q, teamID, inst)
		if err == nil {
			if err := ds.DeleteApp(ctx, q, teamID, actor, a); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return db.ProductInstance{}, err
			}
		}
	}
	out, err := q.SetProductInstanceStatus(ctx, db.SetProductInstanceStatusParams{ID: inst.ID, TeamID: teamID, Status: "destroyed"})
	if err != nil {
		return db.ProductInstance{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "product_instance.destroyed", "product_instance", inst.ID, auditExtra(actor, map[string]any{"name": inst.Name})); err != nil {
		return db.ProductInstance{}, err
	}
	dispatchWebhook(ctx, q, teamID, p.ID, "instance.destroyed", out)
	return out, nil
}

func instanceApp(ctx context.Context, q *db.Queries, teamID string, inst db.ProductInstance) (db.App, error) {
	if inst.AppID == nil {
		return db.App{}, notFound("the instance's app")
	}
	a, err := q.AppInTeamByRef(ctx, db.AppInTeamByRefParams{TeamID: teamID, Ref: *inst.AppID})
	if err != nil {
		return db.App{}, notFound("the instance's app")
	}
	return a, nil
}

// ---- upgrades ---------------------------------------------------------------------------------

// UpgradeProduct queues a deploy of ref (branch, tag or "" for the product's branch) on every
// active instance, one at a time, health-gated by the normal deploy pipeline. It runs in the
// background (a request cannot hold a transaction open for an upgrade that may take minutes); the
// returned row is progress, polled by GET .../products/{product}/upgrades/{upgrade}.
func UpgradeProduct(ctx context.Context, sqlDB *sql.DB, ds *deploy.Service, teamID string, actor deploy.Actor, p db.Product, ref string) (db.ProductUpgrade, error) {
	var up db.ProductUpgrade
	err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{UserID: actor.UserID, TeamID: teamID}, func(q *db.Queries, tx *sql.Tx) error {
		instances, err := q.ListInstancesOfProduct(ctx, db.ListInstancesOfProductParams{ProductID: p.ID, TeamID: teamID})
		if err != nil {
			return err
		}
		total := 0
		for _, i := range instances {
			if i.Status == "active" {
				total++
			}
		}
		if total == 0 {
			return invalid("the product has no active instances to upgrade")
		}
		if ref == "" {
			ref = p.Branch
		}
		up, err = q.InsertProductUpgrade(ctx, db.InsertProductUpgradeParams{ID: newUUID(), TeamID: teamID, ProductID: p.ID, Ref: ref, Total: int64(total), CreatedBy: actor.UserID})
		if err != nil {
			return err
		}
		return tenancy.Record(ctx, q, teamID, actor.UserID, "product.upgrade_started", "product", p.ID, auditExtra(actor, map[string]any{"ref": ref, "instances": total}))
	})
	if err != nil {
		return db.ProductUpgrade{}, err
	}
	go runUpgrade(sqlDB, ds, teamID, actor, p, up)
	return up, nil
}

func runUpgrade(sqlDB *sql.DB, ds *deploy.Service, teamID string, actor deploy.Actor, p db.Product, up db.ProductUpgrade) {
	ctx := context.Background()
	completed, failed := int64(0), int64(0)
	_ = tenancy.Tx(ctx, sqlDB, tenancy.Scope{UserID: actor.UserID, TeamID: teamID}, func(q *db.Queries, tx *sql.Tx) error {
		instances, err := q.ListInstancesOfProduct(ctx, db.ListInstancesOfProductParams{ProductID: p.ID, TeamID: teamID})
		if err != nil {
			return err
		}
		for _, inst := range instances {
			if inst.Status != "active" || inst.AppID == nil {
				continue
			}
			_, _ = q.AdvanceProductUpgrade(ctx, db.AdvanceProductUpgradeParams{ID: up.ID, TeamID: teamID, Completed: completed, Failed: failed, CurrentInstanceID: &inst.ID})
			a, err := q.AppInTeamByRef(ctx, db.AppInTeamByRefParams{TeamID: teamID, Ref: *inst.AppID})
			if err != nil {
				failed++
				continue
			}
			dep, err := ds.QueueDeployment(ctx, q, teamID, actor, a, "deploy", up.Ref, nil, "")
			if err != nil {
				failed++
				continue
			}
			if waitDeployment(ctx, sqlDB, teamID, dep.ID) {
				completed++
			} else {
				failed++
			}
		}
		status := "succeeded"
		if failed > 0 {
			status = "failed"
		}
		_, _ = q.AdvanceProductUpgrade(ctx, db.AdvanceProductUpgradeParams{ID: up.ID, TeamID: teamID, Completed: completed, Failed: failed})
		_, _ = q.FinishProductUpgrade(ctx, db.FinishProductUpgradeParams{ID: up.ID, TeamID: teamID, Status: status})
		return nil
	})
}

// waitDeployment polls a queued deployment until it leaves queued/running, up to 10 minutes (the
// same shape as the deploy pipeline's own health gate timeout order of magnitude). It opens its
// own short transactions rather than holding one for the whole wait.
func waitDeployment(ctx context.Context, sqlDB *sql.DB, teamID, deploymentID string) bool {
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		var status string
		err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{TeamID: teamID, System: true}, func(q *db.Queries, tx *sql.Tx) error {
			d, err := q.DeploymentInTeam(ctx, db.DeploymentInTeamParams{ID: deploymentID, TeamID: teamID})
			if err != nil {
				return err
			}
			status = d.Status
			return nil
		})
		if err != nil {
			return false
		}
		switch status {
		case "succeeded":
			return true
		case "failed", "cancelled":
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(3 * time.Second):
		}
	}
	return false
}

// ---- keys ---------------------------------------------------------------------------------------

// MintKeyInput is a new product-scoped API key (for the team's own billing/signup system).
type MintKeyInput struct {
	Name       string
	WebhookURL string
	DEK        []byte // the team's data-encryption key (ops.Service.TeamDEK), for sealing the webhook secret
}

// MintKey issues an hbk_ token scoped to exactly one product, and (if a webhook URL was given) a
// webhook signing secret, sealed at rest with the team DEK. The plaintext token and the plaintext
// webhook secret are both returned once and never stored.
func MintKey(ctx context.Context, q *db.Queries, teamID, actorID string, p db.Product, in MintKeyInput) (db.ProductKey, string, string, error) {
	if strings.TrimSpace(in.Name) == "" {
		in.Name = "default"
	}
	plainTok, hash := tokens.New(tokens.PrefixProduct)
	var webhookURL *string
	var sealed *string
	var plainSecret string
	if in.WebhookURL != "" {
		if !strings.HasPrefix(in.WebhookURL, "https://") && !strings.HasPrefix(in.WebhookURL, "http://") {
			return db.ProductKey{}, "", "", invalid("webhook_url must be an http(s) URL")
		}
		webhookURL = &in.WebhookURL
		secretBytes := make([]byte, 32)
		_, _ = rand.Read(secretBytes)
		plainSecret = hex.EncodeToString(secretBytes)
		if in.DEK != nil {
			s, err := sealWebhookSecret(in.DEK, teamID, plainSecret)
			if err != nil {
				return db.ProductKey{}, "", "", err
			}
			sealed = &s
		}
	}
	k, err := q.InsertProductKey(ctx, db.InsertProductKeyParams{
		ID: newUUID(), TeamID: teamID, ProductID: p.ID, Name: in.Name, KeyHash: hash,
		WebhookUrl: webhookURL, WebhookSecretSealed: sealed, CreatedBy: actorID,
	})
	if err != nil {
		return db.ProductKey{}, "", "", err
	}
	if err := tenancy.Record(ctx, q, teamID, actorID, "product_key.minted", "product", p.ID, map[string]any{"key_id": k.ID, "name": k.Name}); err != nil {
		return db.ProductKey{}, "", "", err
	}
	return k, plainTok, plainSecret, nil
}

// RevokeKey disables a product key at once.
func RevokeKey(ctx context.Context, q *db.Queries, teamID, actorID string, k db.ProductKey) (db.ProductKey, error) {
	out, err := q.RevokeProductKey(ctx, db.RevokeProductKeyParams{ID: k.ID, TeamID: teamID})
	if err != nil {
		return db.ProductKey{}, err
	}
	return out, tenancy.Record(ctx, q, teamID, actorID, "product_key.revoked", "product", k.ProductID, map[string]any{"key_id": k.ID})
}

// ResolveKey looks up a plaintext hbk_ token, returning the key, its product and its team — or an
// error if it does not exist, is revoked, or its product/team no longer exists. This is the only
// entry point the public product-key REST endpoint uses; every instance action after it is scoped
// to exactly this key's product_id AND team_id (see internal/api/product_keys_public.go).
func ResolveKey(ctx context.Context, q *db.Queries, plainToken string) (db.ProductKey, db.Product, error) {
	if !tokens.HasPrefix(plainToken, tokens.Product) {
		return db.ProductKey{}, db.Product{}, notFound("key")
	}
	hash := tokens.Hash(plainToken)
	k, err := q.ProductKeyByHash(ctx, hash)
	if err != nil {
		return db.ProductKey{}, db.Product{}, notFound("key")
	}
	if k.RevokedAt != nil {
		return db.ProductKey{}, db.Product{}, &ActionError{Status: 401, Msg: "this key has been revoked"}
	}
	p, err := q.ProductByID(ctx, k.ProductID)
	if err != nil || p.TeamID != k.TeamID {
		return db.ProductKey{}, db.Product{}, notFound("key")
	}
	return k, p, nil
}
