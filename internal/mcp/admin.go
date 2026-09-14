package mcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/cloudy/internal/app"
	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/tenancy"
	"github.com/fadymondy/cloudy/internal/tokens"
)

// ScopeAdmin is the admin server's one scope. Every admin token carries it; it exists so the
// admin registry obeys the same "every tool declares a scope" rule as the user registry and so
// narrower admin scopes can be split out later without touching the transport.
const ScopeAdmin = "admin"

// AdminServer is the platform-administration MCP at /api/admin/mcp: triage Cloudy's own issues
// (reports + report_comments, filed through "Report a problem") and manage teams and users.
// Tokens live in admin_mcp_tokens, and a call is refused the moment the minting account is no
// longer a platform admin.
func AdminServer() *Server {
	return &Server{
		Name:          "cloudy-admin",
		Version:       "0.1.0",
		Tools:         AdminTools(),
		AllowedScopes: []string{ScopeAdmin},
		Authenticate:  authenticateAdmin,
		TxScope: func(c Caller) tenancy.Scope {
			return tenancy.Scope{UserID: c.UserID, PlatformAdmin: true}
		},
	}
}

func authenticateAdmin(ctx context.Context, a *app.App, bearer string) (Caller, error) {
	if !strings.HasPrefix(bearer, tokens.PrefixAdminMCP) || a == nil {
		return Caller{}, ErrUnauthorized
	}
	hash := tokens.Hash(bearer)
	var c Caller
	err := tenancy.Tx(ctx, a.SQLDB, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, _ *sql.Tx) error {
		t, err := q.AdminMcpTokenByHash(ctx, hash)
		if err != nil || t.RevokedAt != nil || (t.ExpiresAt != nil && time.Now().After(*t.ExpiresAt)) {
			return ErrUnauthorized
		}
		u, err := q.UserByID(ctx, t.UserID)
		if err != nil || !hasRole(u.Roles, "admin") {
			return ErrUnauthorized
		}
		_ = q.TouchAdminMcpToken(ctx, t.ID)
		c = Caller{TokenID: t.ID, TokenName: t.Name, TokenHash: hash, UserID: t.UserID, Email: u.Email,
			Scopes: []string{ScopeAdmin}, PlatformAdmin: true}
		return nil
	})
	if err != nil {
		return Caller{}, ErrUnauthorized
	}
	return c, nil
}

func hasRole(csv, role string) bool {
	for _, r := range strings.Split(csv, ",") {
		if strings.TrimSpace(r) == role {
			return true
		}
	}
	return false
}

func validStatus(v string) bool {
	switch v {
	case "backlog", "todo", "in_progress", "waiting_approval", "in_review", "done":
		return true
	}
	return false
}

func findReport(ctx context.Context, env *Env, ref string) (db.Report, error) {
	if ref == "" {
		return db.Report{}, errors.New("key is required")
	}
	r, err := env.Q.ReportByRef(ctx, ref)
	if err != nil {
		return db.Report{}, errors.New("issue not found")
	}
	return r, nil
}

// setReport applies an allowlisted column set to one report. Column names never come from
// the caller — only values do.
func setReport(ctx context.Context, env *Env, id string, fields map[string]any) error {
	allowed := map[string]bool{"status": true, "state": true, "priority": true, "issue_type": true, "resolution": true,
		"title": true, "body": true, "closed_at": true, "started_at": true, "finished_at": true, "agent_seconds": true}
	sets := []string{"updated_at = now()"}
	vals := []any{id}
	for k, v := range fields {
		if !allowed[k] {
			return fmt.Errorf("column %q is not updatable", k)
		}
		vals = append(vals, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", k, len(vals)))
	}
	_, err := env.Tx.ExecContext(ctx, "UPDATE reports SET "+strings.Join(sets, ", ")+" WHERE id = $1", vals...) //#nosec G202 -- allowlisted column names
	return err
}

func optStr(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

// AdminTools is the admin server's registry.
func AdminTools() []Tool {
	statusEnum := []string{"backlog", "todo", "in_progress", "waiting_approval", "in_review", "done"}
	return []Tool{
		{
			Name:        "list_issues",
			Description: "List Cloudy's issues (reports filed through Report a problem, or created here). Filter by status, state, issue_type or a title search.",
			Scope:       ScopeAdmin,
			InputSchema: schema(nil, map[string]any{
				"status": str("backlog, todo, in_progress, waiting_approval, in_review, done"), "state": str("open or closed"),
				"issue_type": str("bug, feature, task"), "q": str("Title contains"),
				"limit":              map[string]any{"type": "integer", "default": 50, "maximum": 200},
				"include_human_only": map[string]any{"type": "boolean"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				rows, err := env.Q.ListReportsFiltered(ctx, db.ListReportsFilteredParams{
					Status: optStr(args.Str("status")), State: optStr(args.Str("state")), IssueType: optStr(args.Str("issue_type")),
					Q: optStr(args.Str("q")), IncludeHumanOnly: args.Bool("include_human_only"), Lim: int32(args.Int("limit", 50, 1, 200)),
				})
				if err != nil {
					return "", errors.New("could not list issues")
				}
				if len(rows) == 0 {
					return "No issues matched.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d issue(s):\n\n", len(rows))
				for _, r := range rows {
					fmt.Fprintf(&b, "%s  [%s/%s/%s]  %s\n", r.Key, r.IssueType, r.Status, r.Priority, r.Title)
					if r.Route != nil && *r.Route != "" {
						fmt.Fprintf(&b, "    route: %s\n", *r.Route)
					}
				}
				return b.String(), nil
			},
		},
		{
			Name:        "get_issue",
			Description: "One issue in full — body, page context and its comment thread. Accepts the key (CLD-1) or the id.",
			Scope:       ScopeAdmin,
			InputSchema: schema([]string{"key"}, map[string]any{"key": str("Issue key (CLD-1) or id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				r, err := findReport(ctx, env, args.Str("key"))
				if err != nil {
					return "", err
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%s — %s\ntype=%s status=%s state=%s priority=%s\n", r.Key, r.Title, r.IssueType, r.Status, r.State, r.Priority)
				for label, v := range map[string]*string{"url": r.PageURL, "route": r.Route, "viewport": r.Viewport, "user agent": r.UserAgent, "team": r.TeamID} {
					if v != nil && *v != "" {
						fmt.Fprintf(&b, "%s: %s\n", label, *v)
					}
				}
				if r.ReporterID != nil {
					if u, err := env.Q.UserByID(ctx, *r.ReporterID); err == nil {
						fmt.Fprintf(&b, "reporter: %s\n", u.Email)
					}
				}
				if r.Screenshot != nil && *r.Screenshot != "" {
					fmt.Fprintf(&b, "screenshot: attached (%d bytes)\n", len(*r.Screenshot))
				}
				if r.Body != nil && *r.Body != "" {
					fmt.Fprintf(&b, "\n%s\n", *r.Body)
				}
				comments, _ := env.Q.ListCommentsOfReport(ctx, r.ID)
				fmt.Fprintf(&b, "\n%d comment(s):\n", len(comments))
				for _, c := range comments {
					fmt.Fprintf(&b, "  [%s %s] %s\n", c.AuthorKind, c.CreatedAt.Format(time.RFC3339), c.Body)
				}
				return b.String(), nil
			},
		},
		{
			Name:        "create_issue",
			Description: "Open a new issue on Cloudy's tracker. Returns the assigned key (CLD-n).",
			Scope:       ScopeAdmin,
			InputSchema: schema([]string{"title"}, map[string]any{
				"title": str(""), "body": str("Markdown"),
				"issue_type": map[string]any{"type": "string", "enum": []string{"bug", "feature", "task"}},
				"priority":   map[string]any{"type": "string", "enum": []string{"lowest", "low", "medium", "high", "highest"}},
				"status":     map[string]any{"type": "string", "enum": statusEnum},
				"route":      str("App route the issue concerns"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				title := args.Str("title")
				if title == "" {
					return "", errors.New("title is required")
				}
				status := firstNonEmpty(args.Str("status"), "backlog")
				if !validStatus(status) {
					return "", fmt.Errorf("unknown status %q", status)
				}
				n, err := env.Q.NextReportNumber(ctx)
				if err != nil {
					return "", errors.New("could not number the issue")
				}
				r, err := env.Q.InsertReport(ctx, db.InsertReportParams{
					Number: n, Key: fmt.Sprintf("CLD-%d", n), Title: title, Body: optStr(args.Str("body")),
					IssueType: firstNonEmpty(args.Str("issue_type"), "task"), Status: status,
					Priority: firstNonEmpty(args.Str("priority"), "medium"), Route: optStr(args.Str("route")),
				})
				if err != nil {
					return "", errors.New("could not create the issue")
				}
				if status == "done" {
					if err := setReport(ctx, env, r.ID, map[string]any{"state": "closed", "closed_at": time.Now().UTC()}); err != nil {
						return "", err
					}
				}
				return fmt.Sprintf("Created %s — %s", r.Key, r.Title), nil
			},
		},
		{
			Name:        "update_issue",
			Description: "Change an issue's status, priority, type, title, body or resolution. status=done closes it; moving out of done reopens it.",
			Scope:       ScopeAdmin,
			InputSchema: schema([]string{"key"}, map[string]any{
				"key":        str(""),
				"status":     map[string]any{"type": "string", "enum": statusEnum},
				"priority":   map[string]any{"type": "string", "enum": []string{"lowest", "low", "medium", "high", "highest"}},
				"issue_type": map[string]any{"type": "string", "enum": []string{"bug", "feature", "task"}},
				"resolution": str(""), "title": str(""), "body": str(""),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				r, err := findReport(ctx, env, args.Str("key"))
				if err != nil {
					return "", err
				}
				fields := map[string]any{}
				if v := args.Str("status"); v != "" {
					if !validStatus(v) {
						return "", fmt.Errorf("unknown status %q", v)
					}
					fields["status"] = v
					if v == "done" {
						fields["state"], fields["closed_at"] = "closed", time.Now().UTC()
					} else if r.Status == "done" {
						fields["state"], fields["closed_at"], fields["resolution"] = "open", nil, nil
					}
				}
				for _, f := range []string{"priority", "issue_type", "resolution", "title", "body"} {
					if v := args.Str(f); v != "" {
						fields[f] = v
					}
				}
				if len(fields) == 0 {
					return "Nothing to change.", nil
				}
				if err := setReport(ctx, env, r.ID, fields); err != nil {
					return "", fmt.Errorf("could not update: %w", err)
				}
				return "Updated " + r.Key + ".", nil
			},
		},
		{
			Name:        "comment_issue",
			Description: "Reply on an issue, recorded as coming from the agent.",
			Scope:       ScopeAdmin,
			InputSchema: schema([]string{"key", "body"}, map[string]any{"key": str(""), "body": str("")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				r, err := findReport(ctx, env, args.Str("key"))
				if err != nil {
					return "", err
				}
				body := args.Str("body")
				if body == "" {
					return "", errors.New("body is required")
				}
				if _, err := env.Q.InsertReportComment(ctx, db.InsertReportCommentParams{ReportID: r.ID, AuthorKind: "agent", Body: body}); err != nil {
					return "", errors.New("could not comment")
				}
				_ = setReport(ctx, env, r.ID, map[string]any{})
				return "Commented on " + r.Key + ".", nil
			},
		},
		{
			Name:        "start_issue",
			Description: "Mark an issue in_progress and stamp started_at. Refuses issues marked human_only.",
			Scope:       ScopeAdmin,
			InputSchema: schema([]string{"key"}, map[string]any{"key": str("")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				r, err := findReport(ctx, env, args.Str("key"))
				if err != nil {
					return "", err
				}
				if r.HumanOnly {
					return "", fmt.Errorf("%s is marked human_only — an agent may not start it", r.Key)
				}
				fields := map[string]any{"status": "in_progress", "state": "open"}
				if r.StartedAt == nil {
					fields["started_at"] = time.Now().UTC()
				}
				if err := setReport(ctx, env, r.ID, fields); err != nil {
					return "", err
				}
				return "Started " + r.Key + ".", nil
			},
		},
		{
			Name:        "finish_issue",
			Description: "Mark an issue done, stamp finished_at/closed_at and accumulate agent_seconds. Optionally set a resolution.",
			Scope:       ScopeAdmin,
			InputSchema: schema([]string{"key"}, map[string]any{"key": str(""), "resolution": str("")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				r, err := findReport(ctx, env, args.Str("key"))
				if err != nil {
					return "", err
				}
				now := time.Now().UTC()
				fields := map[string]any{"status": "done", "state": "closed", "closed_at": now, "finished_at": now}
				if r.StartedAt != nil {
					fields["agent_seconds"] = r.AgentSeconds + int64(now.Sub(*r.StartedAt).Seconds())
				}
				if v := args.Str("resolution"); v != "" {
					fields["resolution"] = v
				}
				if err := setReport(ctx, env, r.ID, fields); err != nil {
					return "", err
				}
				return "Finished " + r.Key + ".", nil
			},
		},
		{
			Name:        "list_teams",
			Description: "Every team on the platform with its member count.",
			Scope:       ScopeAdmin,
			InputSchema: schema(nil, map[string]any{"limit": map[string]any{"type": "integer", "default": 100, "maximum": 500}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				rows, err := env.Tx.QueryContext(ctx, `SELECT t.id, t.name, t.slug, t.created_at,
					(SELECT count(*) FROM team_members m WHERE m.team_id = t.id)
					FROM teams t ORDER BY t.created_at DESC LIMIT $1`, args.Int("limit", 100, 1, 500))
				if err != nil {
					return "", errors.New("could not list teams")
				}
				defer rows.Close()
				var b strings.Builder
				n := 0
				for rows.Next() {
					var id, name, slug string
					var created time.Time
					var members int64
					if rows.Scan(&id, &name, &slug, &created, &members) == nil {
						n++
						fmt.Fprintf(&b, "  %s  %s  (%d member(s), id %s)\n", slug, name, members, id)
					}
				}
				return fmt.Sprintf("%d team(s):\n%s", n, b.String()), nil
			},
		},
		{
			Name:        "get_team",
			Description: "One team with its members and roles.",
			Scope:       ScopeAdmin,
			InputSchema: schema([]string{"team_id"}, map[string]any{"team_id": str("")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				t, err := env.Q.GetTeam(ctx, args.Str("team_id"))
				if err != nil {
					return "", errors.New("team not found")
				}
				members, _ := env.Q.ListMembersOfTeam(ctx, t.ID)
				var b strings.Builder
				fmt.Fprintf(&b, "%s  (slug %s, id %s, created %s)\n%d member(s):\n", t.Name, t.Slug, t.ID, t.CreatedAt.Format(time.RFC3339), len(members))
				for _, m := range members {
					fmt.Fprintf(&b, "  %-9s %s\n", m.Role, m.Email)
				}
				return b.String(), nil
			},
		},
		{
			Name:        "list_users",
			Description: "Accounts on the platform, optionally filtered by an email substring.",
			Scope:       ScopeAdmin,
			InputSchema: schema(nil, map[string]any{"q": str("Email contains"), "limit": map[string]any{"type": "integer", "default": 100, "maximum": 500}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				rows, err := env.Tx.QueryContext(ctx, `SELECT u.id, u.email, u.roles, u.created_at,
					(SELECT count(*) FROM team_members m WHERE m.user_id = u.id)
					FROM users u WHERE ($1 = '' OR u.email ILIKE '%' || $1 || '%')
					ORDER BY u.created_at DESC LIMIT $2`, args.Str("q"), args.Int("limit", 100, 1, 500))
				if err != nil {
					return "", errors.New("could not list users")
				}
				defer rows.Close()
				var b strings.Builder
				n := 0
				for rows.Next() {
					var id, email, roles, created string
					var teams int64
					if rows.Scan(&id, &email, &roles, &created, &teams) == nil {
						n++
						flag := ""
						if hasRole(roles, "admin") {
							flag = "  [platform admin]"
						}
						fmt.Fprintf(&b, "  %s  %d team(s)%s  (id %s)\n", email, teams, flag, id)
					}
				}
				return fmt.Sprintf("%d user(s):\n%s", n, b.String()), nil
			},
		},
		{
			Name:        "get_user",
			Description: "One account by email or id, with its team memberships.",
			Scope:       ScopeAdmin,
			InputSchema: schema([]string{"user"}, map[string]any{"user": str("Email or id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				ref := args.Str("user")
				u, err := env.Q.UserByEmail(ctx, ref)
				if err != nil {
					row, err2 := env.Q.UserByID(ctx, ref)
					if err2 != nil {
						return "", errors.New("user not found")
					}
					u = db.UserByEmailRow(row)
				}
				teams, _ := env.Q.ListTeamsForUser(ctx, u.ID)
				var b strings.Builder
				fmt.Fprintf(&b, "%s  (id %s, since %s)\nplatform admin: %v\n%d team(s):\n", u.Email, u.ID, u.CreatedAt, hasRole(u.Roles, "admin"), len(teams))
				for _, t := range teams {
					fmt.Fprintf(&b, "  %-9s %s (%s)\n", t.Role, t.Name, t.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name:        "set_platform_admin",
			Description: "Grant or remove platform administration for an account. Destructive: confirm must equal the account's email. The change applies to that person's next sign-in.",
			Scope:       ScopeAdmin,
			Destructive: true,
			InputSchema: schema([]string{"email", "admin", "confirm"}, map[string]any{
				"email": str(""), "admin": map[string]any{"type": "boolean"}, "confirm": str("The account's email, exactly"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				u, err := env.Q.UserByEmail(ctx, args.Str("email"))
				if err != nil {
					return "", errors.New("user not found")
				}
				if err := RequireConfirm(args, u.Email); err != nil {
					return "", err
				}
				want := args.Bool("admin")
				var roles []string
				for _, r := range strings.Split(u.Roles, ",") {
					if r = strings.TrimSpace(r); r != "" && r != "admin" {
						roles = append(roles, r)
					}
				}
				if want {
					roles = append(roles, "admin")
				} else {
					var admins int
					if err := env.Tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE roles ~ '(^|,)\s*admin\s*(,|$)'`).Scan(&admins); err == nil && admins <= 1 && hasRole(u.Roles, "admin") {
						return "", errors.New("that is the last platform admin — grant someone else first")
					}
				}
				if err := env.Q.SetUserRoles(ctx, db.SetUserRolesParams{ID: u.ID, Roles: strings.Join(roles, ",")}); err != nil {
					return "", errors.New("could not update roles")
				}
				return fmt.Sprintf("%s platform admin: %v (takes effect at their next sign-in).", u.Email, want), nil
			},
		},
	}
}
