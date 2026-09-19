package provisioning

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ServerIcons is the allowlist of server icon keys (the web maps each to a glyph). Keep it in
// step with the servers_icon_check constraint in internal/db/schema/server.sql.
var ServerIcons = []string{"server", "database", "globe", "cloud", "cpu", "box", "rocket", "shield", "code", "terminal",
	"layers", "zap", "flame", "leaf", "star", "heart", "briefcase", "home", "store", "graduation-cap", "building",
	"gamepad", "music", "camera"}

var (
	serverNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	colorRe      = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// MaxLogoURL is the longest logo_url accepted.
const MaxLogoURL = 500

// ServerPatch is a rename / restyle. A nil field is left as it is; "" clears color, icon or
// logo_url (a name cannot be cleared).
type ServerPatch struct {
	Name    *string
	Color   *string
	Icon    *string
	LogoURL *string
	// Audit is merged into the server.updated audit details (e.g. via: mcp).
	Audit map[string]any
}

// UpdateServer validates and applies a patch in the caller's team transaction and audits it as
// server.updated with the old and new value of every field that changed. The caller checked
// the ability (manage_servers: owner or admin).
func UpdateServer(ctx context.Context, q *db.Queries, teamID, actorID string, s db.Server, p ServerPatch) (db.Server, error) {
	name, color, icon, logo := s.Name, s.Color, s.Icon, s.LogoUrl
	if p.Name != nil {
		n := strings.TrimSpace(*p.Name)
		if n == "" || len(n) > 63 || !serverNameRe.MatchString(n) {
			return db.Server{}, refuse("a server name is 1-63 letters, digits, dots, dashes or underscores, starting with a letter or digit")
		}
		name = n
	}
	if p.Color != nil {
		c := strings.TrimSpace(*p.Color)
		if c != "" && !colorRe.MatchString(c) {
			return db.Server{}, refuse("color must be a hex color like #3B82F6")
		}
		color = orNil(strings.ToUpper(c))
	}
	if p.Icon != nil {
		i := strings.TrimSpace(*p.Icon)
		if i != "" && !slices.Contains(ServerIcons, i) {
			return db.Server{}, refuse("icon must be one of: " + strings.Join(ServerIcons, ", "))
		}
		icon = orNil(i)
	}
	if p.LogoURL != nil {
		u := strings.TrimSpace(*p.LogoURL)
		if u != "" && (!strings.HasPrefix(u, "https://") || len(u) > MaxLogoURL || len(u) <= len("https://") || strings.ContainsAny(u, " \t\r\n\"'<>")) {
			return db.Server{}, refuse(fmt.Sprintf("logo_url must be an https:// URL of at most %d characters", MaxLogoURL))
		}
		logo = orNil(u)
	}
	row, err := q.UpdateServerAppearance(ctx, db.UpdateServerAppearanceParams{Name: name, Color: color, Icon: icon, LogoUrl: logo, ID: s.ID, TeamID: teamID})
	if err != nil {
		if strings.Contains(err.Error(), "servers_team_name_key") {
			return db.Server{}, &UserError{Msg: "a server with that name already exists in this team", Conflict: true}
		}
		return db.Server{}, fmt.Errorf("could not update the server: %w", err)
	}
	oldV, newV := map[string]any{}, map[string]any{}
	diff := func(k string, a, b *string) {
		if deref(a) != deref(b) {
			oldV[k], newV[k] = a, b
		}
	}
	diff("name", &s.Name, &row.Name)
	diff("color", s.Color, row.Color)
	diff("icon", s.Icon, row.Icon)
	diff("logo_url", s.LogoUrl, row.LogoUrl)
	if len(newV) == 0 {
		return row, nil
	}
	details := map[string]any{"name": row.Name, "old": oldV, "new": newV}
	for k, v := range p.Audit {
		details[k] = v
	}
	return row, tenancy.Record(ctx, q, teamID, actorID, "server.updated", "server", s.ID,
		details)
}

func orNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
