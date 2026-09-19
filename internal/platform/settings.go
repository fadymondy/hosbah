package platform

import (
	"context"
	"database/sql"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

const (
	SignupOpen       = "open"
	SignupInviteOnly = "invite_only"
	SignupClosed     = "closed"

	MaxAnnouncement = 500
)

type Settings struct {
	SignupMode        string     `json:"signup_mode" enum:"open,invite_only,closed"`
	Announcement      string     `json:"announcement"`
	AnnouncementLevel string     `json:"announcement_level" enum:"info,warning"`
	SupportEmail      string     `json:"support_email"`
	MaxTeamsPerUser   int64      `json:"max_teams_per_user" doc:"0 = unlimited"`
	MaxServersPerTeam int64      `json:"max_servers_per_team" doc:"0 = unlimited"`
	UpdatedBy         *string    `json:"updated_by"`
	UpdatedAt         *time.Time `json:"updated_at"`
}

// PublicSettings is what GET /api/settings/public shows anyone.
type PublicSettings struct {
	SignupMode        string `json:"signup_mode" enum:"open,invite_only,closed"`
	Announcement      string `json:"announcement"`
	AnnouncementLevel string `json:"announcement_level" enum:"info,warning"`
	SupportEmail      string `json:"support_email"`
}

func (s Settings) Public() PublicSettings {
	return PublicSettings{SignupMode: s.SignupMode, Announcement: s.Announcement, AnnouncementLevel: s.AnnouncementLevel, SupportEmail: s.SupportEmail}
}

func defaults() Settings {
	return Settings{SignupMode: SignupOpen, AnnouncementLevel: "info"}
}

// GetSettings reads the settings row in any scope (its SELECT policy is open). A missing row —
// only possible if someone deleted it by hand — reads as the defaults.
func GetSettings(ctx context.Context, q *db.Queries) (Settings, error) {
	r, err := q.PlatformSettings(ctx)
	if isNoRows(err) {
		return defaults(), nil
	}
	if err != nil {
		return Settings{}, internal("read the platform settings")
	}
	at := r.UpdatedAt
	return Settings{SignupMode: r.SignupMode, Announcement: r.Announcement, AnnouncementLevel: r.AnnouncementLevel,
		SupportEmail: r.SupportEmail, MaxTeamsPerUser: r.MaxTeamsPerUser, MaxServersPerTeam: r.MaxServersPerTeam,
		UpdatedBy: r.UpdatedBy, UpdatedAt: &at}, nil
}

// ReadSettings reads the settings outside any caller transaction.
func ReadSettings(ctx context.Context, sqlDB *sql.DB) (Settings, error) {
	var s Settings
	err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		s, err = GetSettings(ctx, q)
		return err
	})
	return s, err
}

// SettingsPatch changes the settings. A nil field is left as it is.
type SettingsPatch struct {
	SignupMode        *string
	Announcement      *string
	AnnouncementLevel *string
	SupportEmail      *string
	MaxTeamsPerUser   *int64
	MaxServersPerTeam *int64
}

func UpdateSettings(ctx context.Context, q *db.Queries, actorID string, p SettingsPatch) (Settings, error) {
	cur, err := GetSettings(ctx, q)
	if err != nil {
		return Settings{}, err
	}
	next := cur
	changed := map[string]any{}
	if p.SignupMode != nil {
		v := strings.TrimSpace(*p.SignupMode)
		if v != SignupOpen && v != SignupInviteOnly && v != SignupClosed {
			return Settings{}, refuse(http.StatusUnprocessableEntity, "signup_mode must be open, invite_only or closed")
		}
		next.SignupMode = v
	}
	if p.Announcement != nil {
		v := strings.TrimSpace(*p.Announcement)
		if utf8.RuneCountInString(v) > MaxAnnouncement {
			return Settings{}, refuse(http.StatusUnprocessableEntity, "the announcement is limited to %d characters", MaxAnnouncement)
		}
		next.Announcement = v
	}
	if p.AnnouncementLevel != nil {
		v := strings.TrimSpace(*p.AnnouncementLevel)
		if v != "info" && v != "warning" {
			return Settings{}, refuse(http.StatusUnprocessableEntity, "announcement_level must be info or warning")
		}
		next.AnnouncementLevel = v
	}
	if p.SupportEmail != nil {
		v := strings.TrimSpace(*p.SupportEmail)
		if v != "" {
			if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
				return Settings{}, refuse(http.StatusUnprocessableEntity, "support_email must be a plain email address, or empty")
			}
		}
		next.SupportEmail = v
	}
	for _, lim := range []struct {
		name string
		in   *int64
		out  *int64
	}{{"max_teams_per_user", p.MaxTeamsPerUser, &next.MaxTeamsPerUser}, {"max_servers_per_team", p.MaxServersPerTeam, &next.MaxServersPerTeam}} {
		if lim.in == nil {
			continue
		}
		if *lim.in < 0 || *lim.in > 100000 {
			return Settings{}, refuse(http.StatusUnprocessableEntity, "%s must be between 0 (unlimited) and 100000", lim.name)
		}
		*lim.out = *lim.in
	}
	diff := func(name string, a, b any) {
		if a != b {
			changed[name] = map[string]any{"from": a, "to": b}
		}
	}
	diff("signup_mode", cur.SignupMode, next.SignupMode)
	diff("announcement", cur.Announcement, next.Announcement)
	diff("announcement_level", cur.AnnouncementLevel, next.AnnouncementLevel)
	diff("support_email", cur.SupportEmail, next.SupportEmail)
	diff("max_teams_per_user", cur.MaxTeamsPerUser, next.MaxTeamsPerUser)
	diff("max_servers_per_team", cur.MaxServersPerTeam, next.MaxServersPerTeam)
	if len(changed) == 0 {
		return cur, nil
	}
	actor := actorID
	if _, err := q.UpsertPlatformSettings(ctx, db.UpsertPlatformSettingsParams{SignupMode: next.SignupMode, Announcement: next.Announcement,
		AnnouncementLevel: next.AnnouncementLevel, SupportEmail: next.SupportEmail, MaxTeamsPerUser: next.MaxTeamsPerUser,
		MaxServersPerTeam: next.MaxServersPerTeam, UpdatedBy: &actor}); err != nil {
		return Settings{}, internal("save the platform settings")
	}
	if err := Record(ctx, q, actorID, "settings.updated", "platform_settings", "platform", changed); err != nil {
		return Settings{}, internal("write the audit entry")
	}
	return GetSettings(ctx, q)
}

// ---- enforcement ---------------------------------------------------------------------------

// CheckTeamLimit refuses creating a team when userID already owns max_teams_per_user. q is the
// creator's own transaction (team_members_select shows a user their own memberships).
func CheckTeamLimit(ctx context.Context, q *db.Queries, userID string) error {
	s, err := GetSettings(ctx, q)
	if err != nil {
		return err
	}
	if s.MaxTeamsPerUser <= 0 {
		return nil
	}
	n, err := q.CountTeamsOwnedBy(ctx, userID)
	if err != nil {
		return internal("count your teams")
	}
	if n >= s.MaxTeamsPerUser {
		return refuse(http.StatusForbidden, "you already own %d team(s), the most this platform allows per account", n)
	}
	return nil
}

// CheckServerLimit refuses adding a server when the team has max_servers_per_team. q is the
// team's transaction.
func CheckServerLimit(ctx context.Context, q *db.Queries, teamID string) error {
	s, err := GetSettings(ctx, q)
	if err != nil {
		return err
	}
	if s.MaxServersPerTeam <= 0 {
		return nil
	}
	n, err := q.CountServersOfTeam(ctx, teamID)
	if err != nil {
		return internal("count the team's servers")
	}
	if n >= s.MaxServersPerTeam {
		return refuse(http.StatusForbidden, "this team already has %d server(s), the most this platform allows per team — remove one first", n)
	}
	return nil
}

/*
CheckSignup decides whether POST /api/auth/register may go ahead.

  - open: yes.
  - closed: no.
  - invite_only: only with the token of a pending, unexpired invitation addressed to the same
    email. The auth plugin's register body ignores unknown fields, so the invitation travels in
    it as `invitation_token` (the token from the /invite/<token> link); the sign-up page then
    accepts the invitation as usual.
*/
func CheckSignup(ctx context.Context, sqlDB *sql.DB, email, invitationToken string) error {
	s, err := ReadSettings(ctx, sqlDB)
	if err != nil {
		return err
	}
	switch s.SignupMode {
	case SignupClosed:
		return refuse(http.StatusForbidden, "sign-up is closed on this platform")
	case SignupInviteOnly:
		invitationToken = strings.TrimSpace(invitationToken)
		if invitationToken == "" {
			return refuse(http.StatusForbidden, "sign-up is by invitation only — use the link in your invitation")
		}
		hash := tokens.Hash(invitationToken)
		ok := false
		_ = tenancy.Tx(ctx, sqlDB, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, _ *sql.Tx) error {
			inv, err := q.InvitationByTokenHash(ctx, hash)
			if err == nil && inv.Status == "pending" && time.Now().Before(inv.ExpiresAt) &&
				strings.EqualFold(strings.TrimSpace(inv.Email), strings.TrimSpace(email)) {
				ok = true
			}
			return nil
		})
		if !ok {
			return refuse(http.StatusForbidden, "sign-up is by invitation only, and this invitation is not valid for that email")
		}
	}
	return nil
}
