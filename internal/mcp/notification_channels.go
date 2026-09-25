package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/hosbah/internal/notifications"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// NotificationChannelTools: a team's alert channels (MH-575 H2) — email, Slack, Discord, generic
// webhook. Owner or admin write; any member reads and sends a test. Mirrors
// internal/api/notification_channels.go.
func NotificationChannelTools() []Tool {
	channelArg := str("Notification channel id")

	settings := func(extra map[string]any) map[string]any {
		props := map[string]any{
			"name":   str("1 to 80 characters"),
			"emails": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": notifications.MaxEmails, "description": "kind email: who is notified"},
			"url":    str("kind slack, discord, webhook: an https:// URL"),
			"secret": str("kind webhook only: an HMAC-SHA256 signing secret, sent as X-Hosbah-Signature"),
			"events": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": notifications.Events},
				"description": "Which of " + strings.Join(notifications.Events, ", ") + " this channel receives"},
			"enabled": map[string]any{"type": "boolean", "description": "Default true on create"},
		}
		for k, v := range extra {
			props[k] = v
		}
		return props
	}
	input := func(args Args) notifications.Input {
		in := notifications.Input{}
		if v, ok := args["name"].(string); ok {
			in.Name = ptrTo(v)
		}
		if v, ok := args["url"].(string); ok {
			in.URL = ptrTo(v)
		}
		if v, ok := args["secret"].(string); ok {
			in.Secret = ptrTo(v)
		}
		if v, ok := args["clear_secret"].(bool); ok {
			in.ClearSecret = v
		}
		if v, ok := args["enabled"].(bool); ok {
			in.Enabled = ptrTo(v)
		}
		if raw, ok := args["emails"]; ok {
			in.Emails = ptrTo(strList(raw))
		}
		if raw, ok := args["events"]; ok {
			in.Events = ptrTo(strList(raw))
		}
		return in
	}
	line := func(c notifications.Channel) string {
		var b strings.Builder
		fmt.Fprintf(&b, "  %s  %-10s %-24s enabled=%v", c.ID, c.Kind, c.Name, c.Enabled)
		if c.Kind == notifications.KindEmail {
			fmt.Fprintf(&b, "  emails=%s", strings.Join(c.Emails, ","))
		} else {
			fmt.Fprintf(&b, "  url=%s secret_set=%v", c.URLHint, c.SecretSet)
		}
		if len(c.Events) > 0 {
			fmt.Fprintf(&b, "  events=%s", strings.Join(c.Events, ","))
		}
		b.WriteString("\n")
		return b.String()
	}

	return []Tool{
		{
			Name: "notification_channels_list", Scope: scopes.Read,
			Description: "The team's notification channels: kind, name, masked url/emails, subscribed events. Any member.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				dek, err := teamDEK(ctx, env)
				if err != nil {
					return "", err
				}
				list, err := notifications.List(ctx, env.Q, env.Caller.TeamID, dek)
				if err != nil {
					return "", err
				}
				if len(list) == 0 {
					return "No notification channels yet. notification_channel_create adds one.", nil
				}
				var b strings.Builder
				for _, c := range list {
					b.WriteString(line(c))
				}
				return b.String(), nil
			},
		},
		{
			Name: "notification_channel_create", Scope: scopes.Write,
			Description: "Add a notification channel. kind email: emails (1 to 10). kind slack, discord, webhook: an https:// url; " +
				"kind webhook may also carry a signing secret. Owner or admin. Audited.",
			InputSchema: schema([]string{"kind", "name"}, settings(map[string]any{
				"kind": map[string]any{"type": "string", "enum": []string{notifications.KindEmail, notifications.KindSlack, notifications.KindDiscord, notifications.KindWebhook}}})),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageTeam); err != nil {
					return "", err
				}
				dek, err := teamDEK(ctx, env)
				if err != nil {
					return "", err
				}
				in := input(args)
				in.Kind = args.Str("kind")
				c, err := notifications.Create(ctx, env.Q, env.Caller.TeamID, env.Caller.UserID, dek, in)
				if err != nil {
					return "", err
				}
				return "Created.\n" + line(c), nil
			},
		},
		{
			Name: "notification_channel_update", Scope: scopes.Write,
			Description: "Change a notification channel's settings (never its kind); only the fields given change. Owner or admin. Audited.",
			InputSchema: schema([]string{"channel"}, settings(map[string]any{"channel": channelArg,
				"clear_secret": map[string]any{"type": "boolean", "description": "kind webhook: remove the signing secret"}})),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageTeam); err != nil {
					return "", err
				}
				cur, err := notifications.Find(ctx, env.Q, env.Caller.TeamID, args.Str("channel"))
				if err != nil {
					return "", err
				}
				dek, err := teamDEK(ctx, env)
				if err != nil {
					return "", err
				}
				c, err := notifications.Update(ctx, env.Q, env.Caller.TeamID, dek, cur, input(args))
				if err != nil {
					return "", err
				}
				return "Updated.\n" + line(c), nil
			},
		},
		{
			Name: "notification_channel_delete", Scope: scopes.Destructive, Destructive: true, ResourceType: "team",
			Description: "Remove a notification channel. confirm must equal the channel's name. Owner or admin. Audited.",
			InputSchema: schema([]string{"channel", "confirm"}, map[string]any{"channel": channelArg, "confirm": str("The channel's name, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageTeam); err != nil {
					return "", err
				}
				cur, err := notifications.Find(ctx, env.Q, env.Caller.TeamID, args.Str("channel"))
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, cur.Name); err != nil {
					return "", err
				}
				if err := notifications.Delete(ctx, env.Q, env.Caller.TeamID, cur.ID); err != nil {
					return "", err
				}
				return "Deleted " + cur.Name + ".", nil
			},
		},
		{
			Name: "notification_channel_test", Scope: scopes.Write,
			Description: "Send a small test message through the channel now. Owner or admin.",
			InputSchema: schema([]string{"channel"}, map[string]any{"channel": channelArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageTeam); err != nil {
					return "", err
				}
				cur, err := notifications.Find(ctx, env.Q, env.Caller.TeamID, args.Str("channel"))
				if err != nil {
					return "", err
				}
				dek, err := teamDEK(ctx, env)
				if err != nil {
					return "", err
				}
				n, err := notifyOf(env)
				if err != nil {
					return "", err
				}
				if err := n.Test(ctx, env.Caller.TeamID, dek, cur); err != nil {
					return "", fmt.Errorf("could not reach the channel: %w", err)
				}
				return "Sent.", nil
			},
		},
	}
}

func strList(raw any) []string {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := []string{}
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func teamDEK(ctx context.Context, env *Env) ([]byte, error) {
	svc, err := opsOf(env)
	if err != nil {
		return nil, err
	}
	return svc.TeamDEK(ctx, env.Q, env.Caller.TeamID)
}

func notifyOf(env *Env) (*notifications.Service, error) {
	if env.App == nil {
		return nil, errors.New("notifications are not configured")
	}
	s, ok := env.App.Notifications.(*notifications.Service)
	if !ok || s == nil {
		return nil, errors.New("notifications are not configured")
	}
	return s, nil
}
