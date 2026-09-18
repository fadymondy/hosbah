package mcp

/*
The destructive gate (P4).

A destructive tool has to clear four things before it acts, and the first two were already here:

 1. the token carries the `destructive` scope (never granted by default);
 2. `confirm` equals the exact name of the resource the tool resolved;
 3. if the team has `require_destructive_approval` on, a human has approved THIS call;
 4. whatever happens — allowed, refused, or held — it is written to the audit log with the id of
    the MCP token that asked.

(3) and (4) are new, and they live here rather than in each tool for the reason the scope check
does: a tool author who forgets is the whole failure mode. `call` runs this for every tool with
Destructive set, so a new destructive tool is gated by existing, not by remembering.

Why `confirm` is the resource key
---------------------------------
An approval has to name the thing it authorises, or approving "delete this app" would authorise
deleting any app. Resolving the resource here would mean duplicating each tool's lookup, so
instead the gate keys on the `confirm` argument — which the tool itself then checks against the
row it resolved (RequireConfirm). A mismatched confirm therefore fails inside the tool and the
transaction rolls back, taking the burnt approval with it, so a wrong confirm cannot spend an
approval meant for something else.

Why the burn is inside the tool's transaction
---------------------------------------------
`call` runs the gate and the tool in one tenancy.Tx. If the tool fails, the rollback un-burns the
approval and the caller can retry on the same approval. If it succeeds, the approval is spent in
the same commit as the deletion, so there is no window where an approval is consumed but nothing
happened, or something happened twice.
*/

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/cloudy/internal/app"
	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/tenancy"
)

// ApprovalTTL is how long an approval request stays actionable. Short on purpose: it is a
// standing permission to destroy something, and a request nobody answered within the hour is
// far more likely forgotten than still wanted.
const ApprovalTTL = time.Hour

// Audit actions for destructive MCP calls. All three carry the token id.
const (
	auditDestructiveAllowed = "mcp.destructive.allowed"
	auditDestructiveRefused = "mcp.destructive.refused"
	auditDestructiveHeld    = "mcp.destructive.held"
)

/*
precheck runs BEFORE the tool's transaction, and it has to.

An approval request filed inside the tool's transaction would be rolled back by the very refusal
that created it — the caller would be told "request X is pending", nobody would ever see request
X, and retrying would file another invisible one. So the request is opened and committed on its
own connection, and only the burn happens later, inside the tool's transaction where it belongs.

It returns the id of an approval that is ready to be burnt, or "" when the team does not require
approval at all.
*/
func (s *Server) precheck(ctx context.Context, a *app.App, c Caller, t Tool, args Args) (string, error) {
	if a == nil || a.SQLDB == nil {
		return "", nil
	}
	name := strings.TrimSpace(args.Str("confirm"))
	var approvalID string
	var held error

	err := tenancy.Tx(ctx, a.SQLDB, s.TxScope(c), func(q *db.Queries, _ *sql.Tx) error {
		required, err := q.TeamRequiresApproval(ctx, c.TeamID)
		if err != nil {
			return err
		}
		if !required {
			return nil
		}
		req, err := q.OpenApprovalRequest(ctx, db.OpenApprovalRequestParams{
			TeamID:       c.TeamID,
			TokenID:      c.TokenID,
			UserID:       c.UserID,
			Tool:         t.Name,
			ResourceType: t.ResourceType,
			ResourceName: name,
			ArgsSummary:  argsSummary(t, args),
			ExpiresAt:    time.Now().Add(ApprovalTTL),
		})
		if err != nil {
			return err
		}
		if req.Status != "approved" {
			// Not an error to the transaction: the row must COMMIT, or the approver never sees it.
			held = errHeld{req: req, tool: t.Name, resource: name}
			return nil
		}
		approvalID = req.ID
		return nil
	})
	if err != nil {
		// Fail closed. If we cannot tell whether approval is required, we do not destroy anything.
		return "", fmt.Errorf("%s could not be checked against this team's approval setting, so it was not run", t.Name)
	}
	return approvalID, held
}

// gate is the in-transaction half: it burns the approval precheck resolved and writes the
// "allowed" audit entry, both in the tool's own transaction — so a tool that fails rolls back
// its audit entry and un-burns its approval together with whatever it was doing.
func (s *Server) gate(ctx context.Context, env *Env, t Tool, args Args, approvalID string) error {
	c := env.Caller
	name := strings.TrimSpace(args.Str("confirm"))

	if approvalID != "" {
		if _, err := env.Q.BurnApprovalRequest(ctx, db.BurnApprovalRequestParams{ID: approvalID, TeamID: c.TeamID}); err != nil {
			// Another caller burnt it first, or it expired between precheck and here.
			return fmt.Errorf("the approval for %s on %q is no longer usable — ask for it again", t.Name, name)
		}
	}

	return tenancy.Record(ctx, env.Q, c.TeamID, c.UserID, auditDestructiveAllowed, t.ResourceType, name,
		map[string]any{
			"tool":           t.Name,
			"via":            "mcp",
			"mcp_token_id":   c.TokenID,
			"mcp_token_name": c.TokenName,
			"resource":       name,
			"args":           json.RawMessage(argsSummary(t, args)),
			"approval":       approvalID != "",
		})
}

// errHeld is the in-band answer when a call is waiting on a human. It is a distinct type so
// tests can assert the hold without matching on prose.
type errHeld struct {
	req      db.ApprovalRequest
	tool     string
	resource string
}

func (e errHeld) Error() string {
	return fmt.Sprintf("%s on %q needs approval in this team and was NOT run. "+
		"Request %s is now pending — an owner or admin approves it in Settings → Approvals, "+
		"after which calling %s again with the same confirm will go through once. "+
		"The request expires at %s.",
		e.tool, e.resource, e.req.ID, e.tool, e.req.ExpiresAt.UTC().Format(time.RFC3339))
}

// Request exposes the filed request (used by the REST layer and tests).
func (e errHeld) Request() db.ApprovalRequest { return e.req }

/*
auditRefusal records a destructive call that did not run, in its OWN transaction.

It has to be separate: a refusal is returned as an error, the tool's transaction rolls back, and
an audit row written inside it would roll back with everything else — so the calls most worth
having in the log are exactly the ones that would vanish from it.

A failure to write the audit row is deliberately ignored. The call has already been refused;
turning a logging problem into a second, different error message would only confuse the caller
about what happened to their resource (nothing).
*/
func (s *Server) auditRefusal(ctx context.Context, a *app.App, c Caller, t Tool, args Args, reason string) {
	if a == nil || a.SQLDB == nil || c.TeamID == "" {
		return
	}
	action := auditDestructiveRefused
	if reason == "held" {
		action = auditDestructiveHeld
	}
	_ = tenancy.Tx(ctx, a.SQLDB, s.TxScope(c), func(q *db.Queries, _ *sql.Tx) error {
		return tenancy.Record(ctx, q, c.TeamID, c.UserID, action, t.ResourceType, strings.TrimSpace(args.Str("confirm")),
			map[string]any{
				"tool":           t.Name,
				"via":            "mcp",
				"mcp_token_id":   c.TokenID,
				"mcp_token_name": c.TokenName,
				"resource":       strings.TrimSpace(args.Str("confirm")),
				"args":           json.RawMessage(argsSummary(t, args)),
				"reason":         reason,
			})
	})
}

/*
argsSummary is what the approver sees, and it is built from argument NAMES plus the resource.

No argument VALUE is copied except `confirm`, which is the resource's own name and is shown in
the dashboard anyway. That rule is what keeps this table safe to list over the API: a destructive
tool may perfectly well take a value that must not be stored — and the moment one does, an
allowlist of "safe" arguments would already have leaked it, because the person adding the tool
would have had to remember to update the list.
*/
func argsSummary(t Tool, args Args) string {
	names := make([]string, 0, len(args))
	for k := range args {
		if k == "confirm" {
			continue
		}
		names = append(names, k)
	}
	sort.Strings(names)
	body, err := json.Marshal(map[string]any{
		"tool":      t.Name,
		"resource":  strings.TrimSpace(args.Str("confirm")),
		"arguments": names,
	})
	if err != nil {
		return "{}"
	}
	return string(body)
}

/*
scrubSecrets is the last line of the plan's "results never contain secrets" rule, applied to the
output of destructive tools only.

reveal_env and reveal_db_url exist to return a secret and are gated on secrets:read; scrubbing
them would break the feature. A destructive tool has no such reason, so anything secret-shaped in
its output is a mistake, and this turns that mistake into a redaction instead of a leak.

It is a backstop, not the design — the tools are written not to echo values in the first place.
*/
var secretShaped = []*regexp.Regexp{
	// Cloudy's own token families: MCP (cld_/cla_), enrolment (cle_), deploy hook (cdh_).
	regexp.MustCompile(`\bc(?:ld|la|le|dh)_[A-Za-z0-9]{16,}`),
	// Sealed envelopes, which must never be transported outside a signed operation.
	regexp.MustCompile(`\bcsv1:[A-Za-z0-9+/=._-]{8,}`),
	// Any URL carrying credentials, e.g. postgres://user:password@host/db.
	regexp.MustCompile(`\b([a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@]+):[^\s/@]+@`),
}

func scrubSecrets(out string) string {
	for i, re := range secretShaped {
		if i == len(secretShaped)-1 {
			out = re.ReplaceAllString(out, "$1:[redacted]@")
			continue
		}
		out = re.ReplaceAllString(out, "[redacted]")
	}
	return out
}

