// Package mcp is Hosbah's Model Context Protocol transport and its two servers:
//
//	POST /api/mcp        the USER server — team-bound tokens (mcp_tokens, "hbu_…"; "cld_…" before the rename)
//	POST /api/admin/mcp  the ADMIN server — platform-admin tokens (admin_mcp_tokens, "hba_…"; "cla_…" before the rename)
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
	"strconv"
	"strings"

	"github.com/fadymondy/hosbah/internal/app"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/platform"
	"github.com/fadymondy/hosbah/internal/tenancy"
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
	// Scope is the caller's tenancy scope, for reads a tool makes after its transaction.
	Scope tenancy.Scope
	// Then, when a tool sets it, runs AFTER the tool's transaction has committed and its output is
	// appended — for work that must see the committed change or must not hold a transaction open
	// (deploy_app's `wait`). An error from it is reported in-band like any tool error.
	Then func(ctx context.Context) (string, error)
}

// Tool is one callable. Scope is mandatory; Destructive tools must take `confirm` and name a
// ResourceType, which is what an approval request is filed against (scopes_test.go enforces both).
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Scope       string
	Destructive bool
	// ResourceType is the kind of thing a destructive tool destroys ("app", "server", …). It is
	// the subject of the audit entry and of any approval request, so it must match one of the
	// values approval_requests_resource_type_check allows.
	ResourceType string
	Run          func(ctx context.Context, env *Env, args Args) (string, error)
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

// ErrTeamSuspended: the token is valid but its team is suspended by the platform admins. The
// transport answers 423 with the reason, so a machine caller can tell it from a bad token.
var ErrTeamSuspended = errors.New(platform.ErrTeamSuspended.Msg)

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
		if errors.Is(err, ErrTeamSuspended) {
			writeRPC(w, http.StatusLocked, rpcResponse{JSONRPC: "2.0",
				Error: &rpcError{Code: -32003, Message: ErrTeamSuspended.Error()}})
			return
		}
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
		if t.Destructive {
			s.auditRefusal(ctx, a, c, t, args, "missing the destructive scope")
		}
		return "", fmt.Errorf("%s needs the %q scope, which this token does not carry. "+
			"Mint a token with that scope to allow it.", t.Name, t.Scope)
	}
	if t.Destructive && args.Str("confirm") == "" {
		s.auditRefusal(ctx, a, c, t, args, "no confirm argument")
		return "", fmt.Errorf("%s is destructive: pass confirm set to the exact name of the resource it acts on", t.Name)
	}
	// The approval request must be filed and committed before the tool's transaction opens, or a
	// held call would roll back the very request it just told the caller to get approved.
	approvalID := ""
	if t.Destructive && c.TeamID != "" {
		id, err := s.precheck(ctx, a, c, t, args)
		if err != nil {
			reason := "refused: " + err.Error()
			var h errHeld
			if errors.As(err, &h) {
				reason = "held"
			}
			s.auditRefusal(ctx, a, c, t, args, reason)
			return "", err
		}
		approvalID = id
	}

	var out string
	var env *Env
	scope := s.TxScope(c)
	err := tenancy.Tx(ctx, a.SQLDB, scope, func(q *db.Queries, tx *sql.Tx) error {
		var err error
		env = &Env{App: a, Caller: c, Q: q, Tx: tx, Scope: scope}
		// The approval gate and the audit entry run in the tool's own transaction, so a tool
		// that fails rolls back the approval it burnt along with everything else.
		//
		// Team-bound callers only. The admin server's tools act across the platform and have no
		// team whose setting could hold them, so there is nothing to check and nowhere to file
		// the request — gating them here would read the setting of a team that does not exist
		// and, failing closed, break every admin destructive call.
		if t.Destructive && c.TeamID != "" {
			if err := s.gate(ctx, env, t, args, approvalID); err != nil {
				return err
			}
		}
		out, err = t.Run(ctx, env, args)
		return err
	})
	if t.Destructive {
		// A destructive result has no business carrying a secret; redact one rather than return it.
		out = scrubSecrets(out)
		if err != nil {
			reason := "refused: " + err.Error()
			var held errHeld
			if errors.As(err, &held) {
				reason = "held"
			}
			s.auditRefusal(ctx, a, c, t, args, reason)
		}
	}
	if err != nil || env == nil || env.Then == nil {
		return out, err
	}
	more, err := env.Then(ctx)
	if err != nil {
		return "", fmt.Errorf("%s%w", out, err)
	}
	return out + more, nil
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

// Str returns a trimmed string argument ("" if absent). A number or bool is accepted too: clients
// (the CLI, SDKs, LLMs) send "deployment": 12 as readily as "12".
func (a Args) Str(k string) string {
	switch v := a[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
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

// Float64 returns a float argument clamped to [min, max], or def (MH-575 H10: dollar amounts).
func (a Args) Float64(k string, def, min, max float64) float64 {
	v, ok := a[k].(float64)
	if !ok {
		return def
	}
	if v < min {
		v = min
	}
	if v > max {
		v = max
	}
	return v
}

func schema(required []string, props map[string]any) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "required": required, "properties": props}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
