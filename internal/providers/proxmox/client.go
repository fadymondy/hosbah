package proxmox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrUnauthorized is returned (wrapped) when Proxmox answers 401 or 403.
var ErrUnauthorized = errors.New("proxmox: the Proxmox token was refused or lacks permission")

// apiError is a non-2xx answer. Proxmox puts the message in the HTTP reason phrase and, for
// parameter errors, per-field messages in the body's "errors" object.
type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string { return fmt.Sprintf("proxmox: HTTP %d: %s", e.Status, e.Msg) }

// taskError is an async task that finished with an exit status other than "OK".
type taskError struct{ UPID, ExitStatus string }

func (e *taskError) Error() string {
	return fmt.Sprintf("proxmox: task %s failed: %s", e.UPID, e.ExitStatus)
}

// isNotFound reports whether err means the guest does not exist. Proxmox mostly answers a missing
// guest with HTTP 500 "Configuration file '...' does not exist" rather than 404.
func isNotFound(err error) bool {
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return true
	}
	return err != nil && !errors.Is(err, ErrUnauthorized) && strings.Contains(err.Error(), "does not exist")
}

type client struct {
	base   string // https://host:8006/api2/json
	auth   string // Authorization header value; contains the secret
	secret string
	http   *http.Client
	poll   time.Duration // task-status polling interval
}

func newClient(apiURL, tokenID, secret, fingerprint string, insecure bool) (*client, error) {
	u, err := url.Parse(strings.TrimSpace(apiURL))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("proxmox: api_url %q is not a URL", apiURL)
	}
	if u.Scheme != "https" {
		return nil, errors.New("proxmox: api_url must be https (the token would travel in clear)")
	}
	path := strings.TrimSuffix(u.Path, "/")
	if !strings.HasSuffix(path, "/api2/json") {
		path += "/api2/json"
	}
	u.Path, u.RawQuery, u.Fragment = path, "", ""

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	switch {
	case fingerprint != "":
		want, err := parseFingerprint(fingerprint)
		if err != nil {
			return nil, err
		}
		// Pinning replaces chain verification: Proxmox certificates are self-signed, so the only
		// meaningful check is "is this exactly the certificate the owner told us about".
		// VerifyConnection runs on resumed sessions too, unlike VerifyPeerCertificate.
		tlsCfg.InsecureSkipVerify = true
		tlsCfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("proxmox: server presented no certificate")
			}
			got := sha256.Sum256(cs.PeerCertificates[0].Raw)
			if subtle.ConstantTimeCompare(got[:], want) != 1 {
				return fmt.Errorf("proxmox: server certificate fingerprint %s does not match the pinned tls_fingerprint", hex.EncodeToString(got[:]))
			}
			return nil
		}
	case insecure:
		tlsCfg.InsecureSkipVerify = true
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = tlsCfg
	return &client{
		base:   u.String(),
		auth:   "PVEAPIToken=" + tokenID + "=" + secret,
		secret: secret,
		// A per-request ceiling so one hung request cannot stall a caller whose context has no
		// deadline; long operations are tasks, which are polled with short requests.
		http: &http.Client{Transport: tr, Timeout: 2 * time.Minute},
		poll: time.Second,
	}, nil
}

func parseFingerprint(s string) ([]byte, error) {
	clean := strings.NewReplacer(":", "", " ", "").Replace(strings.TrimSpace(s))
	b, err := hex.DecodeString(clean)
	if err != nil || len(b) != sha256.Size {
		return nil, errors.New("proxmox: tls_fingerprint must be a SHA-256 fingerprint (64 hex digits, colons optional)")
	}
	return b, nil
}

// do calls method on path (relative to /api2/json). GET and DELETE send params as the query; POST
// and PUT send them form-encoded. When out is non-nil the response's "data" is decoded into it.
func (c *client) do(ctx context.Context, method, path string, params url.Values, out any) error {
	return c.scrub(c.doRaw(ctx, method, path, params, out))
}

func (c *client) doRaw(ctx context.Context, method, path string, params url.Values, out any) error {
	target := c.base + path
	var body io.Reader
	if method == http.MethodPost || method == http.MethodPut {
		body = strings.NewReader(params.Encode())
	} else if len(params) > 0 {
		target += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return fmt.Errorf("proxmox: %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("proxmox: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("proxmox: %s %s: reading response: %w", method, path, err)
	}
	var env struct {
		Data   json.RawMessage   `json:"data"`
		Errors map[string]string `json:"errors"`
	}
	_ = json.Unmarshal(raw, &env) // error bodies are not always JSON; the status line carries the message

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)))
		if len(env.Errors) > 0 {
			fields := make([]string, 0, len(env.Errors))
			for k, v := range env.Errors {
				fields = append(fields, k+": "+strings.TrimSpace(v))
			}
			sort.Strings(fields)
			msg += " (" + strings.Join(fields, "; ") + ")"
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w (%s %s: HTTP %d %s)", ErrUnauthorized, method, path, resp.StatusCode, msg)
		}
		return fmt.Errorf("%s %s: %w", method, path, &apiError{Status: resp.StatusCode, Msg: msg})
	}
	if out == nil || len(env.Data) == 0 || bytes.Equal(env.Data, []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("proxmox: %s %s: decoding response: %w", method, path, err)
	}
	return nil
}

// scrub is a last line of defence: nothing should ever put the secret in an error, but if a
// transport error were to echo a request back, it must not leak.
func (c *client) scrub(err error) error {
	if err == nil || c.secret == "" || !strings.Contains(err.Error(), c.secret) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), c.secret, "[redacted]"))
}

// startTask performs an async call and waits for its task. Endpoints that finished synchronously
// answer data=null, in which case there is nothing to wait for.
func (c *client) startTask(ctx context.Context, method, path string, params url.Values) error {
	var upid string
	if err := c.do(ctx, method, path, params, &upid); err != nil {
		return err
	}
	if upid == "" {
		return nil
	}
	return c.waitTask(ctx, upid)
}

// waitTask polls a task until it stops, then requires exit status "OK".
func (c *client) waitTask(ctx context.Context, upid string) error {
	// UPID:<node>:<pid>:<pstart>:<starttime>:<type>:<id>:<user>: — the task lives on <node>, which
	// for some calls differs from the node the request was sent to.
	parts := strings.Split(upid, ":")
	if len(parts) < 3 || parts[0] != "UPID" || parts[1] == "" {
		return fmt.Errorf("proxmox: unexpected task id %q", upid)
	}
	path := "/nodes/" + url.PathEscape(parts[1]) + "/tasks/" + url.PathEscape(upid) + "/status"
	t := time.NewTicker(c.poll)
	defer t.Stop()
	for {
		var st struct {
			Status     string `json:"status"`
			ExitStatus string `json:"exitstatus"`
		}
		if err := c.do(ctx, http.MethodGet, path, nil, &st); err != nil {
			return err
		}
		if st.Status == "stopped" {
			if st.ExitStatus != "OK" {
				return c.scrub(&taskError{UPID: upid, ExitStatus: st.ExitStatus})
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("proxmox: waiting for task %s: %w", upid, ctx.Err())
		case <-t.C:
		}
	}
}

// flexInt decodes a JSON number or numeric string; Proxmox is inconsistent (nextid is a string,
// vmid in listings a number) and absent fields decode as 0.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("proxmox: expected an integer, got %s", b)
	}
	*f = flexInt(n)
	return nil
}
