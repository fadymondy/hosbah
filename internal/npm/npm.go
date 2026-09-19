// Package npm is Cloudy's client for Nginx Proxy Manager, used as the public edge in front of
// tunnelled servers: for every domain routed to a server behind an frp tunnel, Cloudy keeps one NPM
// proxy host forwarding that domain to the tunnel's port. It only changes or deletes proxy hosts it
// created (marked in their advanced config), never the operator's own.
package npm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Marker identifies proxy hosts Cloudy owns.
const Marker = "# managed by Cloudy"

// Client talks to one NPM instance.
type Client struct {
	base, email, password string
	http                  *http.Client
	token                 string
}

// New returns a client for NPM at base (e.g. https://npm.example.com).
func New(base, email, password string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), email: email, password: password, http: &http.Client{Timeout: 30 * time.Second}}
}

// WithHTTPClient replaces the HTTP client (tests).
func (c *Client) WithHTTPClient(h *http.Client) *Client { c.http = h; return c }

// ProxyHost is the part of an NPM proxy host Cloudy reads and writes.
type ProxyHost struct {
	ID                    int      `json:"id,omitempty"`
	DomainNames           []string `json:"domain_names"`
	ForwardScheme         string   `json:"forward_scheme"`
	ForwardHost           string   `json:"forward_host"`
	ForwardPort           int      `json:"forward_port"`
	CertificateID         any      `json:"certificate_id"`
	SSLForced             bool     `json:"ssl_forced"`
	HTTP2Support          bool     `json:"http2_support"`
	BlockExploits         bool     `json:"block_exploits"`
	AllowWebsocketUpgrade bool     `json:"allow_websocket_upgrade"`
	AdvancedConfig        string   `json:"advanced_config"`
	Meta                  any      `json:"meta,omitempty"`
	Locations             []any    `json:"locations"`
	AccessListID          int      `json:"access_list_id"`
	CachingEnabled        bool     `json:"caching_enabled"`
	HSTSEnabled           bool     `json:"hsts_enabled"`
	HSTSSubdomains        bool     `json:"hsts_subdomains"`
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("npm: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("npm: %s %s: %s (HTTP %d)", method, path, msg, resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Login gets a token (the password never leaves this call).
func (c *Client) Login(ctx context.Context) error {
	var r struct {
		Token string `json:"token"`
	}
	c.token = ""
	if err := c.do(ctx, http.MethodPost, "/api/tokens", map[string]string{"identity": c.email, "secret": c.password}, &r); err != nil {
		return err
	}
	if r.Token == "" {
		return errors.New("npm: login returned no token")
	}
	c.token = r.Token
	return nil
}

func (c *Client) ensure(ctx context.Context) error {
	if c.token != "" {
		return nil
	}
	return c.Login(ctx)
}

// ProxyHosts lists every proxy host.
func (c *Client) ProxyHosts(ctx context.Context) ([]ProxyHost, error) {
	if err := c.ensure(ctx); err != nil {
		return nil, err
	}
	var hs []ProxyHost
	return hs, c.do(ctx, http.MethodGet, "/api/nginx/proxy-hosts", nil, &hs)
}

func hostFor(hs []ProxyHost, domain string) *ProxyHost {
	for i := range hs {
		for _, d := range hs[i].DomainNames {
			if strings.EqualFold(d, domain) {
				return &hs[i]
			}
		}
	}
	return nil
}

// Route makes domain forward to http://forwardHost:forwardPort with the given certificate. It
// creates a Cloudy-managed proxy host or updates the one Cloudy already made; a proxy host for the
// domain that Cloudy did not create is left alone and reported.
func (c *Client) Route(ctx context.Context, domain, forwardHost string, forwardPort int, certificateID int) error {
	hs, err := c.ProxyHosts(ctx)
	if err != nil {
		return err
	}
	want := ProxyHost{DomainNames: []string{strings.ToLower(domain)}, ForwardScheme: "http", ForwardHost: forwardHost,
		ForwardPort: forwardPort, CertificateID: certificateID, SSLForced: certificateID > 0, HTTP2Support: certificateID > 0,
		BlockExploits: true, AllowWebsocketUpgrade: true, AdvancedConfig: Marker, Locations: []any{}}
	if certificateID == 0 {
		want.CertificateID = 0
	}
	if cur := hostFor(hs, domain); cur != nil {
		if !strings.Contains(cur.AdvancedConfig, Marker) {
			return fmt.Errorf("npm: %s already has a proxy host Cloudy did not create (#%d); not touching it", domain, cur.ID)
		}
		return c.do(ctx, http.MethodPut, fmt.Sprintf("/api/nginx/proxy-hosts/%d", cur.ID), want, nil)
	}
	return c.do(ctx, http.MethodPost, "/api/nginx/proxy-hosts", want, nil)
}

// Unroute deletes the Cloudy-managed proxy host for domain (nothing to do if there is none).
func (c *Client) Unroute(ctx context.Context, domain string) error {
	hs, err := c.ProxyHosts(ctx)
	if err != nil {
		return err
	}
	cur := hostFor(hs, domain)
	if cur == nil || !strings.Contains(cur.AdvancedConfig, Marker) {
		return nil
	}
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/nginx/proxy-hosts/%d", cur.ID), nil, nil)
}
