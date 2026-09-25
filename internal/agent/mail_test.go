package agent

import (
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

// fakeStalwart is a minimal stand-in for Stalwart's HTTP management API: it records every call and
// answers what mail.domain/mail.mailbox/mail.alias expect, so the agent's HTTP client is exercised
// exactly like it would be against a live server — without a Docker container or the real binary.
type fakeStalwart struct {
	t       *testing.T
	mu      []string // "METHOD path" of every call, in order
	secret  string
	domains map[string]bool
}

func newFakeStalwart(t *testing.T, secret string) *httptest.Server {
	t.Helper()
	fs := &fakeStalwart{t: t, secret: secret, domains: map[string]bool{}}
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
	case r.URL.Path == "/api/health":
		w.WriteHeader(http.StatusOK)
	case strings.HasPrefix(r.URL.Path, "/api/domain/") && r.Method == http.MethodPut:
		fs.domains[strings.TrimPrefix(r.URL.Path, "/api/domain/")] = true
		w.WriteHeader(http.StatusOK)
	case strings.HasPrefix(r.URL.Path, "/api/domain/") && r.Method == http.MethodDelete:
		delete(fs.domains, strings.TrimPrefix(r.URL.Path, "/api/domain/"))
		w.WriteHeader(http.StatusOK)
	case strings.HasPrefix(r.URL.Path, "/api/dkim/") && r.Method == http.MethodPost:
		domain := strings.TrimPrefix(r.URL.Path, "/api/dkim/")
		if !fs.domains[domain] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"selector": "hosbah", "public_key": "ZmFrZS1wdWJrZXk="})
	case strings.HasPrefix(r.URL.Path, "/api/principal/") && r.Method == http.MethodPut:
		w.WriteHeader(http.StatusOK)
	case strings.HasPrefix(r.URL.Path, "/api/principal/") && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusOK)
	case strings.HasPrefix(r.URL.Path, "/api/alias/") && r.Method == http.MethodPut:
		w.WriteHeader(http.StatusOK)
	case strings.HasPrefix(r.URL.Path, "/api/alias/") && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
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
	if out.DKIMSelector != "hosbah" || out.DKIMPublicKey != "ZmFrZS1wdWJrZXk=" {
		t.Fatalf("unexpected DKIM result: %+v", out)
	}

	// delete is idempotent and needs no DKIM call.
	oc2, _ := scriptOpCtx(t, priv, agentproto.MailDomainParams{
		MailServerID: "srv1", AdminPort: port, AdminSecret: adminBox, Domain: "example.com", Action: "delete",
	})
	if _, _, err := mailDomain(oc2); err != nil {
		t.Fatalf("mailDomain delete: %v", err)
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
