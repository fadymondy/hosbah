// Package hosbah is the Go SDK for Hosbah. It talks to a team's MCP endpoint (POST /api/mcp) with a
// team-bound token (hbu_…, or cld_… minted before the rename; under MCP tokens), so everything the dashboard can do is reachable
// with the same scopes, audit trail and destructive-action approvals — and new Hosbah features are
// available through Call before a typed helper exists for them.
//
//	c := hosbah.New("https://app.hosbah.com", os.Getenv("HOSBAH_TOKEN"))
//	out, err := c.Deploy(ctx, "shop", hosbah.Wait())
//
// It depends on the standard library only.
package hosbah

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Client calls one Hosbah team with one token.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	id      atomic.Int64
}

// New returns a client for baseURL (the Hosbah origin, e.g. https://app.hosbah.com).
func New(baseURL, token string, opts ...Option) *Client {
	c := &Client{baseURL: strings.TrimRight(baseURL, "/"), token: strings.TrimSpace(token), http: &http.Client{Timeout: 10 * time.Minute}}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the HTTP client (timeouts, proxies, tests).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// ToolError is a refusal from a tool (not found, missing scope, validation…). Its text is Hosbah's.
type ToolError struct{ Tool, Message string }

func (e *ToolError) Error() string { return "hosbah: " + e.Tool + ": " + e.Message }

// APIError is a transport or protocol failure (bad token, server error).
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("hosbah: %s (HTTP %d)", e.Message, e.Status) }

// Tool describes one callable tool.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) rpc(ctx context.Context, method string, params any, out any) error {
	if c.token == "" {
		return errors.New("hosbah: no token (mint one under MCP tokens)")
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id.Add(1), "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/mcp", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("hosbah: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	var r rpcResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 200 || msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Message: msg}
	}
	if r.Error != nil {
		return &APIError{Status: resp.StatusCode, Message: r.Error.Message}
	}
	if resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// Tools lists what this token can call.
func (c *Client) Tools(ctx context.Context) ([]Tool, error) {
	var r struct {
		Tools []Tool `json:"tools"`
	}
	err := c.rpc(ctx, "tools/list", map[string]any{}, &r)
	return r.Tools, err
}

// Call runs any tool with its arguments and returns its text output.
func (c *Client) Call(ctx context.Context, tool string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	var r struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := c.rpc(ctx, "tools/call", map[string]any{"name": tool, "arguments": args}, &r); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range r.Content {
		b.WriteString(p.Text)
	}
	if r.IsError {
		return "", &ToolError{Tool: tool, Message: b.String()}
	}
	return b.String(), nil
}

// ---- typed helpers (each is Call with a fixed tool name) ---------------------------------------

// Arg sets an optional argument on a helper.
type Arg func(map[string]any)

// With sets any argument by name.
func With(k string, v any) Arg { return func(m map[string]any) { m[k] = v } }

// Wait makes Deploy block until the deployment finishes.
func Wait() Arg { return With("wait", true) }

func args(base map[string]any, extra []Arg) map[string]any {
	for _, a := range extra {
		a(base)
	}
	return base
}

// Whoami says which team and scopes the token has.
func (c *Client) Whoami(ctx context.Context) (string, error) { return c.Call(ctx, "whoami", nil) }

// Servers lists the team's servers.
func (c *Client) Servers(ctx context.Context) (string, error) { return c.Call(ctx, "list_servers", nil) }

// Apps lists the team's apps.
func (c *Client) Apps(ctx context.Context) (string, error) { return c.Call(ctx, "list_apps", nil) }

// Deploy queues a deployment of app (Wait() blocks until it finishes).
func (c *Client) Deploy(ctx context.Context, app string, opts ...Arg) (string, error) {
	return c.Call(ctx, "deploy_app", args(map[string]any{"app": app}, opts))
}

// SetEnv sets an app variable (sealed; applied on the next deploy).
func (c *Client) SetEnv(ctx context.Context, app, name, value string) (string, error) {
	return c.Call(ctx, "set_env", map[string]any{"app": app, "name": name, "value": value})
}

// AddDomain routes hostname to app.
func (c *Client) AddDomain(ctx context.Context, app, hostname string, opts ...Arg) (string, error) {
	return c.Call(ctx, "add_domain", args(map[string]any{"app": app, "hostname": hostname}, opts))
}

// AddProxyHost puts upstream behind hostname on server (Caddy proxy manager).
func (c *Client) AddProxyHost(ctx context.Context, server, hostname, upstream string, opts ...Arg) (string, error) {
	return c.Call(ctx, "add_proxy_host", args(map[string]any{"server": server, "hostname": hostname, "upstream": upstream}, opts))
}

// ConnectServer adds an existing server over SSH (With("password", …) or With("private_key", …)).
func (c *Client) ConnectServer(ctx context.Context, name, host string, opts ...Arg) (string, error) {
	return c.Call(ctx, "add_server", args(map[string]any{"name": name, "method": "connect", "host": host}, opts))
}
