package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ReportKeyPrefix is the human key prefix of Hosbah's own issues: CLD-1, CLD-2…
const ReportKeyPrefix = "CLD"

// FeedbackIn is what the "Report a problem" widget sends. The reporter is the session's user,
// never a field. The screenshot/console/network fields are the Feedback SDK's capture payload
// (packages/feedback-sdk, when it ships); the P0 widget sends only page context.
type FeedbackIn struct {
	Title      string `json:"title" minLength:"1" maxLength:"200"`
	Body       string `json:"body,omitempty" maxLength:"20000"`
	IssueType  string `json:"issue_type,omitempty" enum:"bug,feature,task" default:"bug"`
	PageURL    string `json:"page_url,omitempty" maxLength:"2048"`
	Route      string `json:"route,omitempty" maxLength:"512"`
	Selector   string `json:"selector,omitempty" maxLength:"2048"`
	UserAgent  string `json:"user_agent,omitempty" maxLength:"1024"`
	Viewport   string `json:"viewport,omitempty" maxLength:"64"`
	Screenshot string `json:"screenshot,omitempty" maxLength:"4000000"`
	ConsoleLog string `json:"console_log,omitempty" maxLength:"200000"`
	NetworkLog string `json:"network_log,omitempty" maxLength:"200000"`
	TeamID     string `json:"team_id,omitempty" maxLength:"64" doc:"The team the reporter was working in, for context"`
}

func (d *deps) registerFeedback(api huma.API) {
	type feedbackCreatedOut struct {
		Body struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "file-feedback", Method: http.MethodPost, Path: "/api/feedback",
		DefaultStatus: http.StatusCreated, Summary: "Report a problem — lands in Hosbah's own issue tracker",
		Tags: []string{"Feedback"}, MaxBodyBytes: 5 << 20,
	}, func(ctx context.Context, in *struct{ Body FeedbackIn }) (*feedbackCreatedOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		b := in.Body
		issueType := b.IssueType
		if issueType == "" {
			issueType = "bug"
		}
		out := &feedbackCreatedOut{}
		err = d.tx(ctx, tenancy.Scope{UserID: id.ID}, func(q *db.Queries, _ *sql.Tx) error {
			var teamID *string
			if t := strings.TrimSpace(b.TeamID); t != "" {
				// Context only — but never let a report claim a team the reporter is not in.
				m, err := q.MembershipForRef(ctx, db.MembershipForRefParams{UserID: id.ID, Ref: t})
				if err != nil {
					return errNotFound
				}
				teamID = &m.TeamID
			}
			n, err := q.NextReportNumber(ctx)
			if err != nil {
				return huma.Error500InternalServerError("could not number the report")
			}
			row, err := q.InsertReport(ctx, db.InsertReportParams{
				TeamID: teamID, Number: n, Key: fmt.Sprintf("%s-%d", ReportKeyPrefix, n),
				Title: strings.TrimSpace(b.Title), Body: optional(b.Body), IssueType: issueType,
				Status: "backlog", Priority: "medium", ReporterID: ptr(id.ID),
				PageURL: optional(b.PageURL), Route: optional(b.Route), Selector: optional(b.Selector),
				Screenshot: optional(b.Screenshot), ConsoleLog: optional(b.ConsoleLog), NetworkLog: optional(b.NetworkLog),
				UserAgent: optional(b.UserAgent), Viewport: optional(b.Viewport),
			})
			if err != nil {
				return huma.Error500InternalServerError("could not file the report")
			}
			out.Body.ID, out.Body.Key = row.ID, row.Key
			return nil
		})
		return out, err
	})
}
