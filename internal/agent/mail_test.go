package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// fakeStalwart is a minimal stand-in for Stalwart v0.13's HTTP management API: it records every call
// and answers what mail.install/mail.domain/mail.mailbox/mail.alias expect, modeled exactly on the
// real API (base path /api, Basic auth as "admin", everything a "principal" under /api/principal,
// DKIM at /api/dkim and /api/dns/records, settings at /api/settings + /api/reload) — so the agent's
// HTTP client is exercised exactly like it would be against a live server. Any path this fake does not
// recognize answers 404, so drift between the client and the real API shows up as a test failure.
type fakeStalwart struct {
	t          *testing.T
	mu         []string // "METHOD path" of every call, in order
	secret     string
	principals map[string]map[string]any // name -> body, for domain/individual/list principals
	hostname   string
}

func newFakeStalwart(t *testing.T, secret string) *httptest.Server {
	t.Helper()
	fs := &fakeStalwart{t: t, secret: secret, principals: map[string]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(fs.handle))
	t.Cleanup(srv.Close)
	return srv
}

func (fs *fakeStalwart) handle(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	if !ok || user != "admin" || pass != fs.secret {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	fs.mu = append(fs.mu, r.Method+" "+r.URL.Path)
	switch {
	case r.URL.Path == "/api/principal" && r.Method == http.MethodGet:
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []any{}, "total": 0}})
	case r.URL.Path == "/api/principal" && r.Method == http.MethodPost:
		fs.createPrincipal(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/principal/") && r.Method == http.MethodGet:
		name, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/principal/"))
		if _, ok := fs.principals[name]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": fs.principals[name]})
	case strings.HasPrefix(r.URL.Path, "/api/principal/") && r.Method == http.MethodPatch:
		name, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/principal/"))
		if _, ok := fs.principals[name]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": nil})
	case strings.HasPrefix(r.URL.Path, "/api/principal/") && r.Method == http.MethodDelete:
		name, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/principal/"))
		delete(fs.principals, name)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": nil})
	case r.URL.Path == "/api/dkim" && r.Method == http.MethodPost:
		var body struct {
			Domain string `json:"domain"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := fs.principals[body.Domain]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": nil})
	case strings.HasPrefix(r.URL.Path, "/api/dns/records/") && r.Method == http.MethodGet:
		domain, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/dns/records/"))
		if _, ok := fs.principals[domain]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
			{"type": "MX", "name": domain + ".", "content": "10 mail." + domain + "."},
			{"type": "TXT", "name": domain + ".", "content": "v=spf1 mx ~all"},
			{"type": "TXT", "name": "hosbah._domainkey." + domain + ".", "content": "v=DKIM1; k=ed25519; h=sha256; p=ZmFrZS1wdWJrZXk="},
		}})
	case r.URL.Path == "/api/settings" && r.Method == http.MethodPost:
		var updates []struct {
			Values [][2]string `json:"values"`
		}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil || len(updates) == 0 || len(updates[0].Values) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fs.hostname = updates[0].Values[0][1]
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": nil})
	case r.URL.Path == "/api/reload" && r.Method == http.MethodGet:
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": nil})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (fs *fakeStalwart) createPrincipal(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	name, _ := body["name"].(string)
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if _, exists := fs.principals[name]; exists {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "principal already exists"})
		return
	}
	fs.principals[name] = body
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": len(fs.principals)})
}

func portOf(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return port
}

func sealSecret(t *testing.T, pub ed25519.PublicKey, ctx, value string) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"password": value})
	box, err := agentproto.SealBox(pubB64(pub), ctx, raw)
	if err != nil {
		t.Fatalf("seal secret: %v", err)
	}
	return box
}

func TestMailDomainCreateFetchesDKIM(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	const secret = "s3cr3t-admin"
	srv := newFakeStalwart(t, secret)
	port := portOf(t, srv.URL)

	adminBox := sealSecret(t, pub, agentproto.DBSecretContext("srv1", "admin"), secret)
	oc, lines := scriptOpCtx(t, priv, agentproto.MailDomainParams{
		MailServerID: "srv1", AdminPort: port, AdminSecret: adminBox, Domain: "example.com", Action: "create",
	})
	res, code, err := mailDomain(oc)
	if err != nil {
		t.Fatalf("mailDomain create: code=%d err=%v lines=%v", code, err, *lines)
	}
	out := res.(agentproto.MailDomainResult)
	if out.DKIMSelector != "hosbah" {
		t.Fatalf("unexpected DKIM selector: %+v", out)
	}
	if out.DKIMRecord != "v=DKIM1; k=ed25519; h=sha256; p=ZmFrZS1wdWJrZXk=" {
		t.Fatalf("unexpected DKIM record: %+v", out)
	}
	if len(out.DNSRecords) != 3 {
		t.Fatalf("expected all 3 records from /api/dns/records, got %+v", out.DNSRecords)
	}

	// create is idempotent: creating an already-present domain still succeeds and still fetches DKIM.
	oc1b, _ := scriptOpCtx(t, priv, agentproto.MailDomainParams{
		MailServerID: "srv1", AdminPort: port, AdminSecret: adminBox, Domain: "example.com", Action: "create",
	})
	if _, _, err := mailDomain(oc1b); err != nil {
		t.Fatalf("mailDomain create (idempotent): %v", err)
	}

	// delete is idempotent and needs no DKIM call.
	oc2, _ := scriptOpCtx(t, priv, agentproto.MailDomainParams{
		MailServerID: "srv1", AdminPort: port, AdminSecret: adminBox, Domain: "example.com", Action: "delete",
	})
	if _, _, err := mailDomain(oc2); err != nil {
		t.Fatalf("mailDomain delete: %v", err)
	}
	// a second delete of the same (now-gone) domain is also fine.
	oc3, _ := scriptOpCtx(t, priv, agentproto.MailDomainParams{
		MailServerID: "srv1", AdminPort: port, AdminSecret: adminBox, Domain: "example.com", Action: "delete",
	})
	if _, _, err := mailDomain(oc3); err != nil {
		t.Fatalf("mailDomain delete (idempotent): %v", err)
	}
}

func TestMailDomainCreateWrongSecretFails(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	srv := newFakeStalwart(t, "the-real-secret")
	port := portOf(t, srv.URL)
	adminBox := sealSecret(t, pub, agentproto.DBSecretContext("srv1", "admin"), "wrong-secret")
	oc, _ := scriptOpCtx(t, priv, agentproto.MailDomainParams{
		MailServerID: "srv1", AdminPort: port, AdminSecret: adminBox, Domain: "example.com", Action: "create",
	})
	if _, _, err := mailDomain(oc); err == nil {
		t.Fatal("expected an error with the wrong admin secret")
	}
}

func TestMailMailboxCreateSendsSealedPassword(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	const secret = "s3cr3t-admin"
	srv := newFakeStalwart(t, secret)
	port := portOf(t, srv.URL)
	adminBox := sealSecret(t, pub, agentproto.DBSecretContext("srv1", "admin"), secret)
	pwBox := sealSecret(t, pub, agentproto.DBSecretContext("srv1", "mailbox:alice@example.com"), "mailbox-pw")

	oc, _ := scriptOpCtx(t, priv, agentproto.MailboxParams{
		MailServerID: "srv1", AdminPort: port, AdminSecret: adminBox, Email: "alice@example.com",
		QuotaMB: 1024, PasswordSecret: pwBox, Action: "create",
	})
	res, code, err := mailMailbox(oc)
	if err != nil {
		t.Fatalf("mailMailbox create: code=%d err=%v", code, err)
	}
	if res.(agentproto.MailboxResult).Email != "alice@example.com" {
		t.Fatalf("unexpected result: %+v", res)
	}

	// update (quota only, no password) patches rather than re-creating.
	oc2, _ := scriptOpCtx(t, priv, agentproto.MailboxParams{
		MailServerID: "srv1", AdminPort: port, AdminSecret: adminBox, Email: "alice@example.com",
		QuotaMB: 2048, Action: "update",
	})
	if _, _, err := mailMailbox(oc2); err != nil {
		t.Fatalf("mailMailbox update: %v", err)
	}

	oc3, _ := scriptOpCtx(t, priv, agentproto.MailboxParams{
		MailServerID: "srv1", AdminPort: port, AdminSecret: adminBox, Email: "alice@example.com", Action: "delete",
	})
	if _, _, err := mailMailbox(oc3); err != nil {
		t.Fatalf("mailMailbox delete: %v", err)
	}
}

func TestMailAliasCreateAndDelete(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	const secret = "s3cr3t-admin"
	srv := newFakeStalwart(t, secret)
	port := portOf(t, srv.URL)
	adminBox := sealSecret(t, pub, agentproto.DBSecretContext("srv1", "admin"), secret)

	oc, _ := scriptOpCtx(t, priv, agentproto.MailAliasParams{
		MailServerID: "srv1", AdminPort: port, AdminSecret: adminBox, Email: "sales@example.com",
		Destinations: []string{"alice@example.com", "bob@example.com"}, Action: "create",
	})
	if _, _, err := mailAlias(oc); err != nil {
		t.Fatalf("mailAlias create: %v", err)
	}
	oc2, _ := scriptOpCtx(t, priv, agentproto.MailAliasParams{
		MailServerID: "srv1", AdminPort: port, AdminSecret: adminBox, Email: "sales@example.com", Action: "delete",
	})
	if _, _, err := mailAlias(oc2); err != nil {
		t.Fatalf("mailAlias delete: %v", err)
	}
}

// TestStalwartClientSetsHostnameAndReloads exercises setServerHostname's isolated settings-API call
// directly, against the fake's own understanding of that call's shape.
func TestStalwartClientSetsHostnameAndReloads(t *testing.T) {
	const secret = "s3cr3t-admin"
	srv := newFakeStalwart(t, secret)
	port := portOf(t, srv.URL)
	client := newStalwartClient(context.Background(), port, secret)
	if !client.ready() {
		t.Fatal("expected the fake server to answer /api/principal as ready")
	}
	if err := client.setServerHostname("mail.example.org"); err != nil {
		t.Fatalf("setServerHostname: %v", err)
	}
}

func TestMailParamsValidate(t *testing.T) {
	if err := (agentproto.MailboxParams{}).Validate(); err == nil {
		t.Fatal("expected an error for empty params")
	}
	if err := (agentproto.MailAliasParams{MailServerID: "s", AdminSecret: "a", Email: "x@y.com", Action: "create"}).Validate(); err == nil {
		t.Fatal("expected an error: create needs at least one destination")
	}
	if !agentproto.ValidMailDomain("mail.example.com") || agentproto.ValidMailDomain("not a domain") {
		t.Fatal("ValidMailDomain is wrong")
	}
	if !agentproto.ValidMailLocalPart("sales.team") || agentproto.ValidMailLocalPart("") {
		t.Fatal("ValidMailLocalPart is wrong")
	}
}
