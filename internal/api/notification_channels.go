package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/notifications"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Team notification channels (MH-575 H2): email, Slack, Discord and generic webhook destinations a
team's alerts can reach — monitors route to them by id (notify_channel_ids), and later, once wired,
deploy failures / server offline / backup failures route to them by event subscription. Every member
reads; only owner or admin write (AbilityManageTeam), matching how team settings are guarded
elsewhere. A channel's URL/secret is never returned in full — see internal/notifications.Channel.
*/

// ChannelPath addresses one notification channel of a team.
type ChannelPath struct {
	TeamPath
	Channel string `path:"channel" maxLength:"64" doc:"Notification channel id."`
}

// ChannelCreateIn is a new notification channel.
type ChannelCreateIn struct {
	Kind    string   `json:"kind" enum:"email,slack,discord,webhook"`
	Name    string   `json:"name" minLength:"1" maxLength:"80"`
	Emails  []string `json:"emails,omitempty" maxItems:"10" doc:"kind email: who is notified."`
	URL     *string  `json:"url,omitempty" maxLength:"500" doc:"kind slack, discord, webhook: an https:// URL."`
	Secret  *string  `json:"secret,omitempty" maxLength:"200" doc:"kind webhook only: an HMAC-SHA256 signing secret, sent as X-Hosbah-Signature."`
	Events  []string `json:"events,omitempty" doc:"Which of deploy.failed, server.offline, backup.failed, cert.expiring this channel receives."`
	Enabled *bool    `json:"enabled,omitempty" doc:"Default true."`
}

// ChannelPatchIn changes a channel. Omit a field to leave it. The kind never changes.
type ChannelPatchIn struct {
	Name        *string  `json:"name,omitempty" minLength:"1" maxLength:"80"`
	Emails      []string `json:"emails,omitempty" maxItems:"10"`
	URL         *string  `json:"url,omitempty" maxLength:"500"`
	Secret      *string  `json:"secret,omitempty" maxLength:"200"`
	ClearSecret *bool    `json:"clear_secret,omitempty"`
	Events      []string `json:"events,omitempty"`
	Enabled     *bool    `json:"enabled,omitempty"`
}

func (d *deps) notify() *notifications.Service {
	if d.a == nil {
		return nil
	}
	s, _ := d.a.Notifications.(*notifications.Service)
	return s
}

func emailsInChan(v []string) *[]string {
	if v == nil {
		return nil
	}
	return &v
}

func eventsIn(v []string) *[]string {
	if v == nil {
		return nil
	}
	return &v
}

func (d *deps) registerNotificationChannels(api huma.API) {
	tags := []string{"Alerts & channels"}
	base := "/api/teams/{team}/notification-channels"

	type channelsBody struct {
		Channels []notifications.Channel `json:"channels"`
	}
	type listOut struct{ Body channelsBody }
	huma.Register(api, huma.Operation{OperationID: "list-notification-channels", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The team's notification channels. Any member.",
	}, func(ctx context.Context, in *TeamPath) (*listOut, error) {
		out := &listOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			dek, err := d.ops.TeamDEK(ctx, q, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not read the team's key")
			}
			out.Body.Channels, err = notifications.List(ctx, q, m.TeamID, dek)
			return actionErr(err, "could not list the notification channels")
		})
		return out, err
	})

	type channelOut struct{ Body notifications.Channel }
	huma.Register(api, huma.Operation{OperationID: "create-notification-channel", Method: http.MethodPost, Path: base,
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Add a notification channel. kind email: emails (1 to 10). kind slack, discord, webhook: an https:// url; " +
			"kind webhook may also carry a signing secret. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body ChannelCreateIn
	}) (*channelOut, error) {
		out := &channelOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTeam, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			dek, err := d.ops.TeamDEK(ctx, q, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not read the team's key")
			}
			b := in.Body
			out.Body, err = notifications.Create(ctx, q, m.TeamID, id.ID, dek, notifications.Input{Name: &b.Name, Kind: b.Kind,
				Emails: emailsInChan(b.Emails), URL: b.URL, Secret: b.Secret, Events: eventsIn(b.Events), Enabled: b.Enabled})
			if err != nil {
				return actionErr(err, "could not create the notification channel")
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "notification_channel.created", "team", m.TeamID,
				map[string]any{"channel": out.Body.ID, "kind": out.Body.Kind, "name": out.Body.Name})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-notification-channel", Method: http.MethodPatch, Path: base + "/{channel}", Tags: tags,
		Summary: "Change a notification channel's settings (never its kind). Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		ChannelPath
		Body ChannelPatchIn
	}) (*channelOut, error) {
		out := &channelOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTeam, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			c, err := notifications.Find(ctx, q, m.TeamID, in.Channel)
			if err != nil {
				return errNotFound
			}
			dek, err := d.ops.TeamDEK(ctx, q, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not read the team's key")
			}
			b := in.Body
			clear := b.ClearSecret != nil && *b.ClearSecret
			out.Body, err = notifications.Update(ctx, q, m.TeamID, dek, c, notifications.Input{Name: b.Name, Emails: emailsInChan(b.Emails),
				URL: b.URL, Secret: b.Secret, ClearSecret: clear, Events: eventsIn(b.Events), Enabled: b.Enabled})
			if err != nil {
				return actionErr(err, "could not update the notification channel")
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "notification_channel.updated", "team", m.TeamID,
				map[string]any{"channel": c.ID, "name": out.Body.Name})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-notification-channel", Method: http.MethodDelete, Path: base + "/{channel}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Remove a notification channel. Owner or admin. Audited.",
	}, func(ctx context.Context, in *ChannelPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTeam, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			c, err := notifications.Find(ctx, q, m.TeamID, in.Channel)
			if err != nil {
				return errNotFound
			}
			if err := actionErr(notifications.Delete(ctx, q, m.TeamID, c.ID), "could not delete the notification channel"); err != nil {
				return err
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "notification_channel.deleted", "team", m.TeamID,
				map[string]any{"channel": c.ID, "name": c.Name})
		})
		return nil, err
	})

	type testOut struct {
		Body struct {
			Sent bool `json:"sent"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "test-notification-channel", Method: http.MethodPost, Path: base + "/{channel}/test", Tags: tags,
		Summary: "Send a small test message through the channel now. Owner or admin.",
	}, func(ctx context.Context, in *ChannelPath) (*testOut, error) {
		out := &testOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTeam, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			c, err := notifications.Find(ctx, q, m.TeamID, in.Channel)
			if err != nil {
				return errNotFound
			}
			dek, err := d.ops.TeamDEK(ctx, q, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not read the team's key")
			}
			n := d.notify()
			if n == nil {
				return huma.Error503ServiceUnavailable("notifications are not configured")
			}
			if err := n.Test(ctx, m.TeamID, dek, c); err != nil {
				return huma.Error502BadGateway("could not reach the channel: " + err.Error())
			}
			out.Body.Sent = true
			return nil
		})
		return out, err
	})
}
