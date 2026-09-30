package circlexo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	circlexo "github.com/circlexo/circlexo-go"
	"github.com/circlexo/circlexo-go/webhooks"
	"github.com/go-chi/chi/v5"
)

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Events is the hub's webhook receiver (POST /api/circlexo/events, PUBLIC, authenticated by the
// Standard Webhooks signature, which the SDK handler checks first: a bad or missing signature is a
// 401 and nothing runs). Redeliveries are dropped by webhook id, and a handler that fails answers
// 500 so the hub retries.
func (s *Service) Events() http.Handler {
	h := webhooks.NewHandler("")
	h.Secrets = s.WebhookSecrets
	h.Seen = dedupe{s.DB}
	h.Now = s.now
	h.Handle(webhooks.AppInstalled, s.onInstalled)
	h.Handle(webhooks.AppRemoved, func(ctx context.Context, e *webhooks.Envelope) error { return s.Deprovision(ctx, e.OrgID) })
	h.Handle(webhooks.MemberAdded, s.onMember)
	h.Handle(webhooks.MemberRoleChanged, s.onMember)
	h.Handle(webhooks.MemberRemoved, s.onMemberRemoved)
	h.Handle(webhooks.SessionRevoked, func(ctx context.Context, e *webhooks.Envelope) error {
		d, err := e.Session()
		if err != nil {
			return nil // malformed: retrying will not fix it
		}
		return s.revokeSession(ctx, d.User.ID, d.SessionID)
	})
	h.Handle(webhooks.EntitlementChanged, func(ctx context.Context, e *webhooks.Envelope) error {
		d, _ := e.Entitlement()
		if e.OrgID != "" {
			s.Ents.Invalidate(e.OrgID, d.Version)
		}
		return nil
	})
	// subscription.* is in the manifest's subscription list but not in the hub's event catalogue
	// yet; whatever it is called, a subscription change means the entitlements may have changed.
	h.Default = func(ctx context.Context, e *webhooks.Envelope) error {
		if strings.HasPrefix(e.Type, "subscription.") && e.OrgID != "" {
			s.Ents.Invalidate(e.OrgID, 0)
		}
		return nil
	}
	return h
}

func (s *Service) onInstalled(ctx context.Context, e *webhooks.Envelope) error {
	d, _ := e.Org()
	_, err := s.Provision(ctx, ProvisionInput{OrgID: e.OrgID, Slug: d.Slug, Name: d.Name, Locale: d.DefaultLocale})
	return err
}

// activeLink is the link of an org whose app is installed, or nil (an event for an org we do not
// serve is not an error: acknowledge it).
func (s *Service) activeLink(ctx context.Context, orgID string) (*OrgLink, error) {
	link, err := s.OrgByID(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !link.Active) {
		return nil, nil
	}
	return link, err
}

func (s *Service) onMember(ctx context.Context, e *webhooks.Envelope) error {
	d, err := e.Member()
	if err != nil {
		return nil
	}
	link, err := s.activeLink(ctx, e.OrgID)
	if err != nil || link == nil {
		return err
	}
	id, ok, err := s.resolveMember(ctx, circlexo.Member{UserID: d.User.ID, Email: d.User.Email, Role: d.Role}, true)
	if err != nil || !ok {
		return err
	}
	return s.setMember(ctx, link, id, d.Role)
}

func (s *Service) onMemberRemoved(ctx context.Context, e *webhooks.Envelope) error {
	d, err := e.Member()
	if err != nil {
		return nil
	}
	link, err := s.activeLink(ctx, e.OrgID)
	if err != nil || link == nil {
		return err
	}
	id, ok, err := s.resolveMember(ctx, circlexo.Member{UserID: d.User.ID, Email: d.User.Email}, false)
	if err != nil || !ok {
		return err
	}
	return s.removeMember(ctx, link, id)
}

// provisionRequest is what the provision and deprovision endpoints accept: a signed webhook
// envelope (org.app_installed / org.app_removed) or a plain signed {org_id, slug, name, locale}.
type provisionRequest struct {
	OrgID  string `json:"org_id"`
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Locale string `json:"locale"`
}

// readSigned verifies the Standard Webhooks signature on r and returns its request. Anything
// unsigned or badly signed is a 401 before the body is looked at.
func (s *Service) readSigned(w http.ResponseWriter, r *http.Request) (provisionRequest, bool) {
	var req provisionRequest
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return req, false
	}
	if err := webhooks.Verify(s.WebhookSecrets, r.Header, body, webhooks.DefaultTolerance, s.now()); err != nil {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return req, false
	}
	var env webhooks.Envelope
	if json.Unmarshal(body, &env) == nil && env.Type != "" {
		d, _ := env.Org()
		req = provisionRequest{OrgID: env.OrgID, Slug: d.Slug, Name: d.Name, Locale: d.DefaultLocale}
	} else if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return req, false
	}
	if req.OrgID == "" {
		http.Error(w, "org_id is required", http.StatusBadRequest)
		return req, false
	}
	return req, true
}

// ProvisionHandler is POST /api/circlexo/provision (the manifest's provision URL, PUBLIC and
// signed): idempotent create-or-reuse of the org's team, then the tenant confirmation. It answers
// {product_tenant_id, slug}, the pair the hub stores for the org.
func (s *Service) ProvisionHandler(w http.ResponseWriter, r *http.Request) {
	req, ok := s.readSigned(w, r)
	if !ok {
		return
	}
	link, err := s.Provision(r.Context(), ProvisionInput{OrgID: req.OrgID, Slug: req.Slug, Name: req.Name, Locale: req.Locale})
	if err != nil {
		s.Log.Error("circlexo: provision", "org", req.OrgID, "err", err)
		http.Error(w, "provisioning failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"product_tenant_id": link.TeamID, "slug": link.TeamSlug})
}

// DeprovisionHandler is POST /api/circlexo/deprovision (PUBLIC, signed). It never deletes data.
func (s *Service) DeprovisionHandler(w http.ResponseWriter, r *http.Request) {
	req, ok := s.readSigned(w, r)
	if !ok {
		return
	}
	if err := s.Deprovision(r.Context(), req.OrgID); err != nil {
		s.Log.Error("circlexo: deprovision", "org", req.OrgID, "err", err)
		http.Error(w, "deprovisioning failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Mount registers every route. All of them are PUBLIC to the route guard (authorization.go lists the
// exact API paths): the sign-in routes are browser navigations, the rest authenticate by signature.
func (s *Service) Mount(r chi.Router) {
	r.Get(LoginPath, s.Login)
	r.Get(CallbackPath, s.Callback)
	r.Get(LogoutPath, s.Logout)
	r.Get(LaunchPath, s.Launch)
	r.Method(http.MethodPost, EventsPath, s.Events())
	r.Post(ProvisionPath, s.ProvisionHandler)
	r.Post(DeprovisionPath, s.DeprovisionHandler)
}
