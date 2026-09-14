// Package mcp is Cloudy's Model Context Protocol transport and its two servers:
//
//	POST /api/mcp        the USER server — team-bound tokens (mcp_tokens, "cld_…")
//	POST /api/admin/mcp  the ADMIN server — platform-admin tokens (admin_mcp_tokens, "cla_…")
//
// Ported from fadymondy.com-v2's internal/mcp/server.go (JSON-RPC 2.0 over streamable HTTP,
// SHA-256-hashed bearer tokens, tool failures reported in-band with isError) and changed where
// that one was weak:
//
//   - Scopes are mandatory. fadymondy.com let any unrevoked token call any tool that declared no
//     Scope (FM-22). Here a tool without a scope cannot exist: scopes_test.go fails the build of
//     any registry containing one, and dispatch refuses it at runtime as well.
//   - Destructive tools need a `confirm` argument equal to the resource's name (cabrain's
//     brain_delete convention), checked by the tool against the row it resolved.
//   - Every call runs in a tenancy.Tx: the user server's tools see only the token's team, with
//     row-level security behind the team filter; the admin server's run as platform admin.
//   - protocolVersion is negotiated: the current revision is offered, older ones are accepted.
package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/fadymondy/cloudy/internal/app"
	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/tenancy"
)

// Protocol revisions this server speaks, newest first. initialize echoes the client's
// revision when it is one of these, and otherwise offers the newest.
var supportedVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// Caller is the authenticated token holder.
type Caller struct {
	TokenID       string
	TokenName     string
	TokenHash     string
	UserID        string
	Email         string
	TeamID        string // user server only
	Role          string // the token owner's team role (user server)
	Scopes        []string
	PlatformAdmin bool
}

// Env is what a tool runs with: the caller, and the open, scoped transaction.
type Env struct {
	App    *app.App
	Caller Caller
	Q      *db.Queries
	Tx     *sql.Tx
}

// Tool is one callable. Scope is mandatory; Destructive tools must take `confirm`.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Scope       string
	Destructive bool
	Run         func(ctx context.Context, env *Env, args Args) (string, error)
}

// Server is one MCP endpoint: its tools, how it authenticates, and the tenancy scope a
// caller's calls run under.
type Server struct {
	Name          string
	Version       string
	Tools         []Tool
	AllowedScopes []string
	Authenticate  func(ctx context.Context, a *app.App, bearer string) (Caller, error)
	TxScope       func(c Caller) tenancy.Scope
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// ErrUnauthorized is returned by an Authenticate func for any bad token; the transport
// answers every such case identically.
var ErrUnauthorized = errors.New("invalid or missing MCP token")

// Handler serves the endpoint. Only POST: this server offers no server-initiated SSE stream,
// which the streamable-HTTP transport signals with 405 on GET.
func (s *Server) Handler(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if v := r.Header.Get("MCP-Protocol-Version"); v != "" && !supported(v) {
			writeRPC(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0",
				Error: &rpcError{Code: -32600, Message: "unsupported MCP-Protocol-Version " + v}})
			return
		}

		bearer := ""
		if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
			bearer = strings.TrimSpace(h[7:])
		}
		caller, err := s.Authenticate(r.Context(), a, bearer)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+s.Name+`"`)
			writeRPC(w, http.StatusUnauthorized, rpcResponse{JSONRPC: "2.0",
				Error: &rpcError{Code: -32001, Message: ErrUnauthorized.Error()}})
			return
		}

		var rpc rpcRequest
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&rpc) != nil {
			writeRPC(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			return
		}
		res := s.Dispatch(r.Context(), a, caller, rpc)
		if len(rpc.ID) == 0 { // a notification gets no body
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeRPC(w, http.StatusOK, res)
	}
}

// Dispatch answers one JSON-RPC request for an authenticated caller.
func (s *Server) Dispatch(ctx context.Context, a *app.App, c Caller, rpc rpcRequest) rpcResponse {
	reply := func(result any) rpcResponse { return rpcResponse{JSONRPC: "2.0", ID: rpc.ID, Result: result} }
	fail := func(code int, msg string) rpcResponse {
		return rpcResponse{JSONRPC: "2.0", ID: rpc.ID, Error: &rpcError{Code: code, Message: msg}}
	}

	switch rpc.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(rpc.Params, &p)
		version := supportedVersions[0]
		if supported(p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		return reply(map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
		})
	case "ping", "notifications/initialized":
		return reply(map[string]any{})
	case "tools/list":
		return reply(map[string]any{"tools": s.schemas()})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(rpc.Params, &p) != nil {
			return fail(-32602, "invalid params")
		}
		tool, ok := s.tool(p.Name)
		if !ok {
			return fail(-32602, "unknown tool: "+p.Name)
		}
		text, err := s.call(ctx, a, c, tool, decodeArgs(p.Arguments))
		if err != nil {
			// In-band, so the model sees why and can react — not a transport error.
			return reply(map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true})
		}
		return reply(map[string]any{"content": []map[string]any{{"type": "text", "text": text}}})
	default:
		return fail(-32601, "method not found: "+rpc.Method)
	}
}

func (s *Server) call(ctx context.Context, a *app.App, c Caller, t Tool, args Args) (string, error) {
	// A tool with no scope is refused even if it slipped past scopes_test.go.
	if t.Scope == "" || !contains(s.AllowedScopes, t.Scope) {
		return "", fmt.Errorf("%s is not callable: it declares no valid scope", t.Name)
	}
	if !contains(c.Scopes, t.Scope) {
		return "", fmt.Errorf("%s needs the %q scope, which this token does not carry. "+
			"Mint a token with that scope to allow it.", t.Name, t.Scope)
	}
	if t.Destructive && args.Str("confirm") == "" {
		return "", fmt.Errorf("%s is destructive: pass confirm set to the exact name of the resource it acts on", t.Name)
	}
	var out string
	err := tenancy.Tx(ctx, a.SQLDB, s.TxScope(c), func(q *db.Queries, tx *sql.Tx) error {
		var err error
		out, err = t.Run(ctx, &Env{App: a, Caller: c, Q: q, Tx: tx}, args)
		return err
	})
	return out, err
}

// RequireConfirm is the destructive gate's second half: the confirm argument must equal the
// name of the resource the tool resolved (case-insensitive, trimmed).
func RequireConfirm(args Args, name string) error {
	if !strings.EqualFold(strings.TrimSpace(args.Str("confirm")), strings.TrimSpace(name)) {
		return fmt.Errorf("confirm must equal %q exactly — nothing was changed", name)
	}
	return nil
}

func (s *Server) tool(name string) (Tool, bool) {
	for _, t := range s.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

func (s *Server) schemas() []map[string]any {
	out := make([]map[string]any, 0, len(s.Tools))
	for _, t := range s.Tools {
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": fmt.Sprintf("%s [scope: %s]", t.Description, t.Scope),
			"inputSchema": t.InputSchema,
			"annotations": map[string]any{
				"readOnlyHint":    t.Scope == "read",
				"destructiveHint": t.Destructive,
			},
		})
	}
	return out
}

func supported(v string) bool { return contains(supportedVersions, v) }

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func writeRPC(w http.ResponseWriter, status int, res rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(res)
}

// Args is a tool's decoded arguments.
type Args map[string]any

func decodeArgs(raw json.RawMessage) Args {
	out := Args{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// Str returns a trimmed string argument ("" if absent or not a string).
func (a Args) Str(k string) string {
	if v, ok := a[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// Int returns an integer argument clamped to [min, max], or def.
func (a Args) Int(k string, def, min, max int) int {
	v, ok := a[k].(float64)
	if !ok {
		return def
	}
	n := int(v)
	if n < min {
		n = min
	}
	if n > max {
		n = max
	}
	return n
}

// Bool returns a boolean argument (false if absent).
func (a Args) Bool(k string) bool {
	v, _ := a[k].(bool)
	return v
}

func schema(required []string, props map[string]any) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "required": required, "properties": props}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
