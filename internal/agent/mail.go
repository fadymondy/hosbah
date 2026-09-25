package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// MH-577 I1/I2/I3: a team mail server is one Stalwart Mail Server container run by the agent, its
// HTTP management API published on 127.0.0.1 only — never on the server's public interface. The
// agent is the only thing that ever talks to that API; domains, mailboxes and aliases reach it
// through the typed operations below, never through a shell interpolation of a value a person typed.

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

func (c *stalwartClient) do(method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(c.ctx, method, c.base+path, rdr) //#nosec G107 -- base is always 127.0.0.1, fixed by mail.install
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("admin", c.secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the mail server's management API: %w", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound && (method == http.MethodDelete) {
		return out, nil // idempotent delete
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("the mail server's management API answered %d: %s", resp.StatusCode, clip(string(out), 200))
	}
	return out, nil
}

// CreateDomain adds a domain and returns its DKIM selector and base64 public key. Idempotent:
// creating an already-present domain, or fetching its DKIM key when it already exists, both succeed.
func (c *stalwartClient) CreateDomain(domain string) error {
	_, err := c.do(http.MethodPut, "/api/domain/"+domain, map[string]any{"name": domain})
	return err
}

func (c *stalwartClient) DeleteDomain(domain string) error {
	_, err := c.do(http.MethodDelete, "/api/domain/"+domain, nil)
	return err
}

// EnsureDKIM generates (or returns the existing) DKIM key for a domain.
func (c *stalwartClient) EnsureDKIM(domain string) (selector, publicKey string, err error) {
	raw, err := c.do(http.MethodPost, "/api/dkim/"+domain, map[string]any{"algorithm": "ed25519"})
	if err != nil {
		return "", "", err
	}
	var out struct {
		Selector  string `json:"selector"`
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Selector == "" || out.PublicKey == "" {
		return "", "", errors.New("the mail server did not return a DKIM key")
	}
	return out.Selector, out.PublicKey, nil
}

// UpsertMailbox creates or updates a mailbox (Stalwart's "individual" principal). password, when
// empty, leaves the login untouched (an update that only changes the quota).
func (c *stalwartClient) UpsertMailbox(email string, quotaMB int, password string) error {
	body := map[string]any{"type": "individual", "email": email, "quota": quotaMB * 1024 * 1024}
	if password != "" {
		body["secrets"] = []string{password}
	}
	_, err := c.do(http.MethodPut, "/api/principal/"+email, body)
	return err
}

func (c *stalwartClient) DeleteMailbox(email string) error {
	_, err := c.do(http.MethodDelete, "/api/principal/"+email, nil)
	return err
}

// UpsertAlias creates an alias/forward. Stalwart fans an alias's mail out to each destination.
func (c *stalwartClient) UpsertAlias(email string, destinations []string) error {
	_, err := c.do(http.MethodPut, "/api/alias/"+email, map[string]any{"email": email, "destinations": destinations})
	return err
}

func (c *stalwartClient) DeleteAlias(email string) error {
	_, err := c.do(http.MethodDelete, "/api/alias/"+email, nil)
	return err
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
	args := []string{"run", "-d", "--name", p.Container, "--restart", "unless-stopped",
		"--label", agentproto.LabelDataService + "=" + p.MailServerID,
		"--log-opt", "max-size=10m", "--log-opt", "max-file=3",
		"-v", p.Volume + ":/opt/stalwart",
		"-p", fmt.Sprintf("127.0.0.1:%d:8080", p.AdminPort),
		"-p", "25:25", "-p", "465:465", "-p", "587:587", "-p", "993:993", "-p", "4190:4190",
		"-e", "STALWART_HOSTNAME", "-e", "STALWART_ADMIN_SECRET", p.Image}
	env := []string{"STALWART_HOSTNAME=" + p.Hostname, "STALWART_ADMIN_SECRET=" + secret}
	if code, err := oc.RunEnv(env, "docker", args...); err != nil {
		return nil, code, errors.New("could not start the mail server container")
	}

	deadline := time.Now().Add(60 * time.Second)
	client := newStalwartClient(oc.Ctx, p.AdminPort, secret)
	for {
		if _, err := client.do(http.MethodGet, "/api/health", nil); err == nil {
			break
		}
		if time.Now().After(deadline) {
			dumpLogs(oc, p.Container, 40)
			return nil, 1, errors.New("the mail server did not become ready in time")
		}
		time.Sleep(2 * time.Second)
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
		selector, pub, err := client.EnsureDKIM(p.Domain)
		if err != nil {
			return nil, 1, err
		}
		oc.Info("added domain %s (DKIM selector %s)", p.Domain, selector)
		return agentproto.MailDomainResult{DKIMSelector: selector, DKIMPublicKey: pub}, 0, nil
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
		if err := client.UpsertMailbox(p.Email, p.QuotaMB, password); err != nil {
			return nil, 1, err
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
		if err := client.UpsertAlias(p.Email, p.Destinations); err != nil {
			return nil, 1, err
		}
		oc.Info("set alias %s -> %v", p.Email, p.Destinations)
		return agentproto.MailAliasResult{Email: p.Email}, 0, nil
	}
}
