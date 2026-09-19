package deploy

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
)

// FeedRef names an app or a server in the feed.
type FeedRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// FeedUser is who asked for a deployment.
type FeedUser struct {
	Email string `json:"email"`
}

// FeedItem is one deployment in the team's feed.
type FeedItem struct {
	ID            string     `json:"id"`
	Number        int64      `json:"number"`
	App           FeedRef    `json:"app"`
	Server        FeedRef    `json:"server" doc:"name is empty if the server is gone."`
	Status        string     `json:"status" enum:"queued,running,succeeded,failed,cancelled"`
	Kind          string     `json:"kind" enum:"deploy,rollback,restart"`
	Branch        string     `json:"branch" doc:"The ref deployed: a branch, tag or commit."`
	CommitSHA     *string    `json:"commit_sha"`
	CommitMessage *string    `json:"commit_message" doc:"First line of the commit message, when the build knew it."`
	Trigger       string     `json:"trigger" enum:"manual,hook,mcp,github,rollback,restart" doc:"What started it: rollback/restart for those kinds, else how the deploy was asked for."`
	TriggeredBy   *FeedUser  `json:"triggered_by" doc:"The account that asked; null for a deploy hook or a GitHub push."`
	CreatedAt     time.Time  `json:"created_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}

// Feed is a page of the team's deployments.
type Feed struct {
	Deployments []FeedItem `json:"deployments"`
	NextCursor  *string    `json:"next_cursor" doc:"Pass as before= for the next page; null on the last page."`
}

// ErrBadCursor: the before= cursor is not one this API issued.
var ErrBadCursor = errors.New("that cursor is not valid")

// encodeCursor / decodeCursor: an opaque (created_at, id) position.
func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", ErrBadCursor
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	at, err := time.Parse(time.RFC3339Nano, ts)
	if !ok || err != nil || id == "" {
		return time.Time{}, "", ErrBadCursor
	}
	return at, id, nil
}

// TeamFeed is the team's deployments across every app, newest first (team scope).
func TeamFeed(ctx context.Context, q *db.Queries, teamID string, limit int, before string) (Feed, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	p := db.TeamDeploymentFeedParams{TeamID: teamID, LimitRows: int32(limit + 1)} //nolint:gosec // bounded above
	if before != "" {
		at, id, err := decodeCursor(before)
		if err != nil {
			return Feed{}, err
		}
		p.BeforeAt, p.BeforeID = &at, id
	}
	rows, err := q.TeamDeploymentFeed(ctx, p)
	if err != nil {
		return Feed{}, err
	}
	out := Feed{Deployments: []FeedItem{}}
	for i, r := range rows {
		if i == limit {
			last := rows[limit-1]
			c := encodeCursor(last.CreatedAt, last.ID)
			out.NextCursor = &c
			break
		}
		it := FeedItem{ID: r.ID, Number: r.Number, App: FeedRef{ID: r.AppID, Name: r.AppName}, Server: FeedRef{ID: r.ServerID},
			Status: r.Status, Kind: r.Kind, Branch: r.Ref, CommitSHA: r.CommitSha, CommitMessage: r.CommitMessage, Trigger: r.Trigger,
			CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt}
		if r.ServerName != nil {
			it.Server.Name = *r.ServerName
		}
		if r.Kind == "rollback" || r.Kind == "restart" {
			it.Trigger = r.Kind
		}
		if r.TriggeredByEmail != nil {
			it.TriggeredBy = &FeedUser{Email: *r.TriggeredByEmail}
		}
		out.Deployments = append(out.Deployments, it)
	}
	return out, nil
}
