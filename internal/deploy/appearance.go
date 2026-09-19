package deploy

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/provisioning"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Limits of an app's appearance (keep in step with apps_title_check / apps_description_check).
const (
	MaxAppTitle       = 80
	MaxAppDescription = 280
)

var appColorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// AppIcons is the icon allowlist of an app: the servers' list, not a copy of it.
func AppIcons() []string { return provisioning.ServerIcons }

// AppLogoURL is where an app's uploaded logo is served (GET /api/teams/{team}/apps/{app}/logo; {app}
// takes an id or a name — the id cannot collide with another app's name), versioned by its etag so
// the response can be cached forever.
func AppLogoURL(teamID, appID, etag string) *string {
	v := etag
	if len(v) > 16 {
		v = v[:16]
	}
	u := "/api/teams/" + teamID + "/apps/" + appID + "/logo?v=" + v
	return &u
}

// AppearancePatch restyles an app. A nil field is left as it is; "" clears it. The app's name is
// not here on purpose: it is the slug of URLs, containers and hostnames.
type AppearancePatch struct {
	Title       *string
	Description *string
	Color       *string
	Icon        *string
}

// Empty says the patch changes nothing.
func (p AppearancePatch) Empty() bool {
	return p.Title == nil && p.Description == nil && p.Color == nil && p.Icon == nil
}

// UpdateAppearance validates and applies a patch in the caller's team transaction and audits it
// as app.appearance_updated with the old and new value of every field that changed. Git and
// imported apps alike. The caller checked the ability (manage_apps: owner or admin).
func (s *Service) UpdateAppearance(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, p AppearancePatch) (db.App, error) {
	title, desc, color, icon := a.Title, a.Description, a.Color, a.Icon
	if p.Title != nil {
		t := strings.TrimSpace(*p.Title)
		if utf8.RuneCountInString(t) > MaxAppTitle || strings.ContainsAny(t, "\r\n") {
			return db.App{}, invalid(fmt.Sprintf("title is one line of at most %d characters", MaxAppTitle))
		}
		title = strOrNil(t)
	}
	if p.Description != nil {
		d := strings.TrimSpace(*p.Description)
		if utf8.RuneCountInString(d) > MaxAppDescription {
			return db.App{}, invalid(fmt.Sprintf("description is at most %d characters", MaxAppDescription))
		}
		desc = strOrNil(d)
	}
	if p.Color != nil {
		c := strings.TrimSpace(*p.Color)
		if c != "" && !appColorRe.MatchString(c) {
			return db.App{}, invalid("color must be a hex color like #3B82F6")
		}
		color = strOrNil(strings.ToUpper(c))
	}
	if p.Icon != nil {
		i := strings.TrimSpace(*p.Icon)
		if i != "" && !slices.Contains(provisioning.ServerIcons, i) {
			return db.App{}, invalid("icon must be one of: " + strings.Join(provisioning.ServerIcons, ", "))
		}
		icon = strOrNil(i)
	}
	row, err := q.UpdateAppAppearance(ctx, db.UpdateAppAppearanceParams{Title: title, Description: desc, Color: color, Icon: icon, ID: a.ID, TeamID: teamID})
	if err != nil {
		return db.App{}, fmt.Errorf("could not update the app: %w", err)
	}
	oldV, newV := map[string]any{}, map[string]any{}
	diff := func(k string, o, n *string) {
		if strOf(o) != strOf(n) {
			oldV[k], newV[k] = o, n
		}
	}
	diff("title", a.Title, row.Title)
	diff("description", a.Description, row.Description)
	diff("color", a.Color, row.Color)
	diff("icon", a.Icon, row.Icon)
	if len(newV) == 0 {
		return row, nil
	}
	return row, tenancy.Record(ctx, q, teamID, actor.UserID, "app.appearance_updated", "app", a.ID,
		actor.audit(map[string]any{"name": row.Name, "old": oldV, "new": newV}))
}

func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func strOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
