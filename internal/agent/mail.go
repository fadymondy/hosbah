package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// MH-577 I1/I2/I3: a team mail server is one Stalwart Mail Server container run by the agent, its
// HTTP management API published on 127.0.0.1 only — never on the server's public interface. The
// agent is the only thing that ever talks to that API; domains, mailboxes and aliases reach it
// through the typed operations below, never through a shell interpolation of a value a person typed.
//
// The client below talks the real Stalwart v0.13 management API (base path /api, HTTP Basic auth as
// the "admin" fallback user), verified against Stalwart's own openapi.yml and source at tag v0.13.4
// (no source copied — only the wire contract): everything is a "principal" (individual, domain, list,
// group) under /api/principal, DKIM lives at /api/dkim and /api/dns/records, and there is no
// /api/health — readiness is polled via /api/principal.

// stalwartBaseURL is always loopback-only: mail.install never publishes the admin port elsewhere.
func stalwartBaseURL(port int) string { return fmt.Sprintf("http://127.0.0.1:%d", port) }

// stalwartClient is Hosbah's thin wrapper over Stalwart's HTTP management API (Basic auth, admin /
// the generated admin secret). It is intentionally small: only the calls I1-I3 need.
type stalwartClient struct {
	ctx    context.Context
	base   string
	secret string
	http   *http.Client
}

func newStalwartClient(ctx context.Context, port int, secret string) *stalwartClient {
	return &stalwartClient{ctx: ctx, base: stalwartBaseURL(port), secret: secret, http: &http.Client{Timeout: 20 * time.Second}}
}

// do makes one call and hands back the raw status and body; every higher-level method below decides
// for itself which statuses are success, since that differs per Stalwart endpoint (e.g. a delete
// treats 404 as already-done, a DKIM generation treats "already exists" as fine).
func (c *stalwartClient) do(method, path string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(c.ctx, method, c.base+path, rdr) //#nosec G107 -- base is always 127.0.0.1, fixed by mail.install
	if err != nil {
		return 0, nil, err
	}
	req.SetBasicAuth("admin", c.secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("could not reach the mail server's management API: %w", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, out, nil
}

// okOrErr turns a non-2xx status into a descriptive error; call is the "METHOD path" for the message.
func okOrErr(status int, out []byte, call string) error {
	if status >= 200 && status < 300 {
		return nil
	}
	return fmt.Errorf("the mail server's management API answered %d for %s: %s", status, call, clip(string(out), 200))
}

// principalPath builds /api/principal/{name}, escaping name (an email address, or a bare domain).
func principalPath(name string) string { return "/api/principal/" + url.PathEscape(name) }

// principalExists reports whether a principal (domain, mailbox or alias/list) already exists, so
// creation stays idempotent without depending on Stalwart's error text for "already exists".
func (c *stalwartClient) principalExists(name string) (bool, error) {
	status, out, err := c.do(http.MethodGet, principalPath(name), nil)
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, okOrErr(status, out, "GET "+principalPath(name))
	}
}

// deletePrincipal removes any principal by name; a 404 is treated as already-deleted so repeated
// deletes (and cleanup of a row whose agent op never ran) stay idempotent.
func (c *stalwartClient) deletePrincipal(name string) error {
	status, out, err := c.do(http.MethodDelete, principalPath(name), nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		return nil
	}
	return okOrErr(status, out, "DELETE "+principalPath(name))
}

// CreateDomain adds a domain principal. Idempotent: creating an already-present domain succeeds.
func (c *stalwartClient) CreateDomain(domain string) error {
	exists, err := c.principalExists(domain)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	status, out, err := c.do(http.MethodPost, "/api/principal", map[string]any{"type": "domain", "name": domain})
	if err != nil {
		return err
	}
	return okOrErr(status, out, "POST /api/principal (domain)")
}

func (c *stalwartClient) DeleteDomain(domain string) error { return c.deletePrincipal(domain) }

// EnsureDKIM generates a DKIM key for domain if it has none, then reads the DKIM record back from
// Stalwart's own DNS endpoint (the source of truth for the exact TXT content it signs with), along
// with every other record Stalwart reports for the domain. Idempotent: /api/dkim errors when a key
// already exists, so a failed generate call is tolerated and the record is read back regardless.
func (c *stalwartClient) EnsureDKIM(domain string) (selector, record string, all []agentproto.MailDNSRecord, err error) {
	status, out, err := c.do(http.MethodPost, "/api/dkim", map[string]any{
		"id": nil, "algorithm": "Ed25519", "domain": domain, "selector": nil,
	})
	if err != nil {
		return "", "", nil, err
	}
	if status >= 300 && status != http.StatusBadRequest && status != http.StatusConflict {
		return "", "", nil, okOrErr(status, out, "POST /api/dkim")
	}
	return c.dnsRecords(domain)
}

// dnsRecords reads GET /api/dns/records/{domain} and picks out the DKIM TXT record among them
// (its owner name is "<selector>._domainkey.<domain>.").
func (c *stalwartClient) dnsRecords(domain string) (selector, dkimRecord string, all []agentproto.MailDNSRecord, err error) {
	status, out, err := c.do(http.MethodGet, "/api/dns/records/"+url.PathEscape(domain), nil)
	if err != nil {
		return "", "", nil, err
	}
	if err := okOrErr(status, out, "GET /api/dns/records"); err != nil {
		return "", "", nil, err
	}
	var env struct {
		Data []agentproto.MailDNSRecord `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		return "", "", nil, fmt.Errorf("the mail server returned an unexpected DNS records response: %w", err)
	}
	all = env.Data
	for _, r := range env.Data {
		if r.Type != "TXT" || !strings.Contains(r.Name, "._domainkey.") {
			continue
		}
		sel, _, ok := strings.Cut(strings.TrimSuffix(r.Name, "."), "._domainkey.")
		if !ok {
			continue
		}
		selector, dkimRecord = sel, r.Content
		break
	}
	if selector == "" || dkimRecord == "" {
		return "", "", nil, errors.New("the mail server did not return a DKIM record")
	}
	return selector, dkimRecord, all, nil
}

// CreateMailbox creates a mailbox: an "individual" principal named by its full email address (unique,
// simplest lookup), with that address as its one email and (when given) its login password.
func (c *stalwartClient) CreateMailbox(email string, quotaMB int, password string) error {
	body := map[string]any{
		"type": "individual", "name": email, "emails": []string{email},
		"quota": int64(quotaMB) * 1024 * 1024, "roles": []string{"user"},
	}
	if password != "" {
		body["secrets"] = []string{password}
	}
	status, out, err := c.do(http.MethodPost, "/api/principal", body)
	if err != nil {
		return err
	}
	return okOrErr(status, out, "POST /api/principal (mailbox)")
}

// UpdateMailbox patches an existing mailbox's quota and, when given, its password.
func (c *stalwartClient) UpdateMailbox(email string, quotaMB int, password string) error {
	actions := []map[string]any{{"action": "set", "field": "quota", "value": int64(quotaMB) * 1024 * 1024}}
	if password != "" {
		actions = append(actions, map[string]any{"action": "set", "field": "secrets", "value": []string{password}})
	}
	status, out, err := c.do(http.MethodPatch, principalPath(email), actions)
	if err != nil {
		return err
	}
	return okOrErr(status, out, "PATCH /api/principal (mailbox)")
}

func (c *stalwartClient) DeleteMailbox(email string) error { return c.deletePrincipal(email) }

// CreateAlias models an alias to a local mailbox and a forward to external addresses the same way: a
// Stalwart "list" principal named by the alias address, whose mail fans out to its externalMembers.
// Destinations always go in externalMembers (arbitrary addresses), even one that happens to be a
// mailbox on this same server: Stalwart delivers to an externalMember that resolves to a local
// address exactly like a normal delivery, so one code path covers both a same-domain alias and an
// outside forward without first resolving which destinations are local principals.
func (c *stalwartClient) CreateAlias(email string, destinations []string) error {
	body := map[string]any{"type": "list", "name": email, "emails": []string{email}, "externalMembers": destinations}
	status, out, err := c.do(http.MethodPost, "/api/principal", body)
	if err != nil {
		return err
	}
	return okOrErr(status, out, "POST /api/principal (alias)")
}

func (c *stalwartClient) DeleteAlias(email string) error { return c.deletePrincipal(email) }

// setServerHostname sets server.hostname through Stalwart's generic settings API and reloads it live.
// Stalwart's settings API takes a small typed-update language (its openapi.yml "settings-put" schema:
// a list of {type, prefix, values, assert_empty} operations) that nothing else in this client needs,
// so it is kept isolated to this one function and exercised against the fake in mail_test.go.
func (c *stalwartClient) setServerHostname(hostname string) error {
	body := []map[string]any{{
		"type": "insert", "prefix": nil,
		"values":       [][2]string{{"server.hostname", hostname}},
		"assert_empty": false,
	}}
	status, out, err := c.do(http.MethodPost, "/api/settings", body)
	if err != nil {
		return err
	}
	if err := okOrErr(status, out, "POST /api/settings"); err != nil {
		return err
	}
	status, out, err = c.do(http.MethodGet, "/api/reload", nil)
	if err != nil {
		return err
	}
	return okOrErr(status, out, "GET /api/reload")
}

// ready polls Stalwart's admin API until it answers: there is no /api/health in v0.13, so this uses
// the same GET /api/principal?limit=1 call a caller would use to list principals.
func (c *stalwartClient) ready() bool {
	status, _, err := c.do(http.MethodGet, "/api/principal?limit=1", nil)
	return err == nil && status == http.StatusOK
}

// ---- mail.install ----------------------------------------------------------------------------------

func mailInstall(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.MailInstallParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if code, err := dbDockerReady(oc); err != nil {
		return nil, code, err
	}
	admin, err := oc.openSecret(p.MailServerID, "admin", p.AdminSecret)
	if err != nil {
		return nil, 1, err
	}
	secret := admin["password"]
	if secret == "" {
		return nil, 1, errors.New("the mail server's admin secret could not be opened")
	}

	if img, _ := execOutput(oc, "docker", "inspect", "--format", "{{.Config.Image}}", p.Container); img == p.Image && containerRunning(oc, p.Container) {
		oc.Info("%s is already running %s — nothing to do", p.Container, p.Image)
		return agentproto.MailInstallResult{Container: p.Container, Volume: p.Volume}, 0, nil
	}
	_ = exec.CommandContext(oc.Ctx, "docker", "rm", "-f", p.Container).Run() //#nosec G204 -- idempotent re-run
	if _, err := oc.Run("docker", "volume", "create", p.Volume); err != nil {
		return nil, 1, errors.New("could not create the mail server's data volume")
	}

	oc.Info("starting %s on 127.0.0.1:%d (admin API)", p.Image, p.AdminPort)
	// On FIRST boot only, Stalwart reads STALWART_ADMIN_PASSWORD and writes it into its own config in
	// the /opt/stalwart volume as the "admin" fallback-admin secret; there is no STALWART_HOSTNAME or
	// STALWART_ADMIN_SECRET env var. The hostname is set afterwards through the settings API below, so
	// it (unlike the admin password) can be changed later without recreating the container. 443/80 are
	// never published — Caddy owns those; only the admin API (8080) is published, and only on loopback.
	args := []string{"run", "-d", "--name", p.Container, "--restart", "unless-stopped",
		"--label", agentproto.LabelDataService + "=" + p.MailServerID,
		"--log-opt", "max-size=10m", "--log-opt", "max-file=3",
		"-v", p.Volume + ":/opt/stalwart",
		"-p", fmt.Sprintf("127.0.0.1:%d:8080", p.AdminPort),
		"-p", "25:25", "-p", "465:465", "-p", "587:587", "-p", "143:143", "-p", "993:993", "-p", "4190:4190",
		"-e", "STALWART_ADMIN_PASSWORD", p.Image}
	env := []string{"STALWART_ADMIN_PASSWORD=" + secret}
	if code, err := oc.RunEnv(env, "docker", args...); err != nil {
		return nil, code, errors.New("could not start the mail server container")
	}

	deadline := time.Now().Add(60 * time.Second)
	client := newStalwartClient(oc.Ctx, p.AdminPort, secret)
	for !client.ready() {
		if time.Now().After(deadline) {
			dumpLogs(oc, p.Container, 40)
			return nil, 1, errors.New("the mail server did not become ready in time")
		}
		time.Sleep(2 * time.Second)
	}
	if err := client.setServerHostname(p.Hostname); err != nil {
		return nil, 1, fmt.Errorf("could not set the mail server's hostname: %w", err)
	}
	oc.Info("%s is ready", p.Container)
	return agentproto.MailInstallResult{Container: p.Container, Volume: p.Volume}, 0, nil
}

// ---- mail.domain -----------------------------------------------------------------------------------

func mailDomain(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.MailDomainParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	admin, err := oc.openSecret(p.MailServerID, "admin", p.AdminSecret)
	if err != nil {
		return nil, 1, err
	}
	client := newStalwartClient(oc.Ctx, p.AdminPort, admin["password"])
	switch p.Action {
	case "delete":
		if err := client.DeleteDomain(p.Domain); err != nil {
			return nil, 1, err
		}
		oc.Info("removed domain %s", p.Domain)
		return agentproto.MailDomainResult{}, 0, nil
	default:
		if err := client.CreateDomain(p.Domain); err != nil {
			return nil, 1, err
		}
		selector, record, all, err := client.EnsureDKIM(p.Domain)
		if err != nil {
			return nil, 1, err
		}
		oc.Info("added domain %s (DKIM selector %s)", p.Domain, selector)
		return agentproto.MailDomainResult{DKIMSelector: selector, DKIMRecord: record, DNSRecords: all}, 0, nil
	}
}

// ---- mail.mailbox ----------------------------------------------------------------------------------

func mailMailbox(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.MailboxParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	admin, err := oc.openSecret(p.MailServerID, "admin", p.AdminSecret)
	if err != nil {
		return nil, 1, err
	}
	client := newStalwartClient(oc.Ctx, p.AdminPort, admin["password"])
	switch p.Action {
	case "delete":
		if err := client.DeleteMailbox(p.Email); err != nil {
			return nil, 1, err
		}
		oc.Info("removed mailbox %s", p.Email)
		return agentproto.MailboxResult{Email: p.Email}, 0, nil
	default:
		var password string
		if p.PasswordSecret != "" {
			box, err := oc.openSecret(p.MailServerID, "mailbox:"+p.Email, p.PasswordSecret)
			if err != nil {
				return nil, 1, err
			}
			password = box["password"]
		}
		var upsertErr error
		if p.Action == "create" {
			upsertErr = client.CreateMailbox(p.Email, p.QuotaMB, password)
		} else {
			upsertErr = client.UpdateMailbox(p.Email, p.QuotaMB, password)
		}
		if upsertErr != nil {
			return nil, 1, upsertErr
		}
		oc.Info("%s mailbox %s (quota %d MB)", map[string]string{"create": "created", "update": "updated"}[p.Action], p.Email, p.QuotaMB)
		return agentproto.MailboxResult{Email: p.Email}, 0, nil
	}
}

// ---- mail.alias ------------------------------------------------------------------------------------

func mailAlias(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.MailAliasParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	admin, err := oc.openSecret(p.MailServerID, "admin", p.AdminSecret)
	if err != nil {
		return nil, 1, err
	}
	client := newStalwartClient(oc.Ctx, p.AdminPort, admin["password"])
	switch p.Action {
	case "delete":
		if err := client.DeleteAlias(p.Email); err != nil {
			return nil, 1, err
		}
		oc.Info("removed alias %s", p.Email)
		return agentproto.MailAliasResult{Email: p.Email}, 0, nil
	default:
		if err := client.CreateAlias(p.Email, p.Destinations); err != nil {
			return nil, 1, err
		}
		oc.Info("set alias %s -> %v", p.Email, p.Destinations)
		return agentproto.MailAliasResult{Email: p.Email}, 0, nil
	}
}
