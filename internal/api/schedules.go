package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/schedules"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The "Schedules" section of a site: cron jobs that run inside the app's live release (the agent's
app.exec: docker exec in the release's web container, the command as one argv element of the
container's own sh — never a shell on the host).

Read: any member — except what a run PRINTED, which is for whoever may read the app's runtime logs
(deploy). Write: whoever may manage the app (manage_apps). Run now: whoever may deploy or restart
it (deploy). Imported apps have no release to run in: 422.
*/

// SchedulePath addresses one schedule of an app (id or name).
type SchedulePath struct {
	AppPath
	Schedule string `path:"schedule" maxLength:"80" doc:"Schedule id or name."`
}

// ScheduleRunPath addresses one run of a schedule.
type ScheduleRunPath struct {
	SchedulePath
	Run string `path:"run" maxLength:"64" doc:"Run id."`
}

// ScheduleCreateIn is a new schedule.
type ScheduleCreateIn struct {
	Name           string  `json:"name" minLength:"1" maxLength:"80" doc:"Unique in the app."`
	Cron           string  `json:"cron" minLength:"1" maxLength:"200" doc:"5 fields: minute hour day-of-month month day-of-week, e.g. */5 * * * *."`
	Timezone       *string `json:"timezone,omitempty" maxLength:"64" doc:"IANA zone the expression is read in (default UTC)."`
	Command        string  `json:"command" minLength:"1" maxLength:"4000" doc:"A shell line of 1 to 1000 characters, no line breaks. Runs as sh -lc inside the live release's web container, with that container's environment."`
	TimeoutSeconds *int    `json:"timeout_seconds,omitempty" doc:"10 to 3600 (default 300). The command is killed after it."`
	Enabled        *bool   `json:"enabled,omitempty" doc:"Default true."`
}

// SchedulePatchIn changes a schedule. Omit a field to leave it.
type SchedulePatchIn struct {
	Name           *string `json:"name,omitempty" minLength:"1" maxLength:"80"`
	Cron           *string `json:"cron,omitempty" minLength:"1" maxLength:"200"`
	Timezone       *string `json:"timezone,omitempty" maxLength:"64"`
	Command        *string `json:"command,omitempty" minLength:"1" maxLength:"4000"`
	TimeoutSeconds *int    `json:"timeout_seconds,omitempty"`
	Enabled        *bool   `json:"enabled,omitempty" doc:"false clears next_run_at; true computes it from now."`
}

// SchedulePreviewIn is an expression to try.
type SchedulePreviewIn struct {
	Cron     string `json:"cron" maxLength:"200"`
	Timezone string `json:"timezone,omitempty" maxLength:"64" doc:"Default UTC."`
}

func (d *deps) schedules() *schedules.Service {
	if d.a == nil {
		return nil
	}
	s, _ := d.a.Schedules.(*schedules.Service)
	return s
}

func (d *deps) registerSchedules(api huma.API) {
	tags := []string{"Schedules"}
	base := "/api/teams/{team}/apps/{app}"

	// found runs then with the app and the schedule of the path, in the member's team.
	found := func(ctx context.Context, p *SchedulePath, ability string,
		then func(q *db.Queries, id *auth.Identity, m *tenancy.Membership, a db.App, s db.Schedule) error) error {
		return d.inTeam(ctx, p.Team, ability, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, p.App)
			if err != nil {
				return err
			}
			s, err := schedules.Find(ctx, q, m.TeamID, a, p.Schedule)
			if err != nil {
				return errNotFound
			}
			return then(q, id, m, a, s)
		})
	}

	type scheduleListOut struct {
		Body struct {
			Schedules []schedules.Schedule `json:"schedules"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-app-schedules", Method: http.MethodGet, Path: base + "/schedules", Tags: tags,
		Summary: "The app's schedules, each with next_run_at, its newest run (last_run, null when it never ran) and next_runs — the next " +
			"3 fire times of its expression. Any member.",
	}, func(ctx context.Context, in *AppPath) (*scheduleListOut, error) {
		out := &scheduleListOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			out.Body.Schedules, err = schedules.List(ctx, q, m.TeamID, a, d.schedules().Now())
			return actionErr(err, "could not list the schedules")
		})
		return out, err
	})

	type previewOut struct {
		Body struct {
			NextRuns []time.Time `json:"next_runs" doc:"The next 5 fire times, UTC."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "preview-app-schedule", Method: http.MethodPost, Path: base + "/schedules/preview", Tags: tags,
		Summary: "Validate a cron expression and timezone without saving anything: its next 5 fire times. 422: not a 5-field " +
			"expression, not an IANA timezone, or an expression that never fires. Any member.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body SchedulePreviewIn
	}) (*previewOut, error) {
		out := &previewOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			if _, err := appIn(ctx, q, m, in.App); err != nil {
				return err
			}
			next, err := schedules.Preview(in.Body.Cron, in.Body.Timezone, d.schedules().Now(), 5)
			out.Body.NextRuns = next
			return actionErr(err, "could not preview the schedule")
		})
		return out, err
	})

	type scheduleOut struct{ Body schedules.Schedule }
	huma.Register(api, huma.Operation{OperationID: "create-app-schedule", Method: http.MethodPost, Path: base + "/schedules",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Add a cron job: command runs as sh -lc inside the live release's web container (that container's environment, no TTY) " +
			"whenever cron fires in timezone, and is killed after timeout_seconds. A fire while the previous run is still going is " +
			"recorded as skipped. 422: a bad field, or an imported app. 409: the name is taken, or the app has 30 schedules. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body ScheduleCreateIn
	}) (*scheduleOut, error) {
		out := &scheduleOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			b := in.Body
			out.Body, err = schedules.Create(ctx, q, m.TeamID, webActor(id), a, schedules.Input{Name: &b.Name, Cron: &b.Cron, Timezone: b.Timezone,
				Command: &b.Command, TimeoutSeconds: b.TimeoutSeconds, Enabled: b.Enabled}, d.schedules().Now())
			return scheduleErr(err, "could not create the schedule")
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "get-app-schedule", Method: http.MethodGet, Path: base + "/schedules/{schedule}", Tags: tags,
		Summary: "One schedule of the app, by id or name. Any member.",
	}, func(ctx context.Context, in *SchedulePath) (*scheduleOut, error) {
		out := &scheduleOut{}
		err := found(ctx, in, tenancy.AbilityView, func(q *db.Queries, _ *auth.Identity, _ *tenancy.Membership, _ db.App, s db.Schedule) error {
			var err error
			out.Body, err = schedules.View(ctx, q, s, d.schedules().Now())
			return actionErr(err, "could not read the schedule")
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-app-schedule", Method: http.MethodPatch, Path: base + "/schedules/{schedule}", Tags: tags,
		Summary: "Change a schedule. A new expression or timezone, or switching it back on, recomputes next_run_at from now; switching " +
			"it off clears it. 422 and 409 as for creating one. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		SchedulePath
		Body SchedulePatchIn
	}) (*scheduleOut, error) {
		out := &scheduleOut{}
		err := found(ctx, &in.SchedulePath, tenancy.AbilityManageApps, func(q *db.Queries, id *auth.Identity, m *tenancy.Membership, a db.App, s db.Schedule) error {
			b := in.Body
			var err error
			out.Body, err = schedules.Update(ctx, q, m.TeamID, webActor(id), a, s, schedules.Input{Name: b.Name, Cron: b.Cron, Timezone: b.Timezone,
				Command: b.Command, TimeoutSeconds: b.TimeoutSeconds, Enabled: b.Enabled}, d.schedules().Now())
			return scheduleErr(err, "could not update the schedule")
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-app-schedule", Method: http.MethodDelete, Path: base + "/schedules/{schedule}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Remove a schedule and its runs. A run that is going is not stopped. Owner or admin. Audited.",
	}, func(ctx context.Context, in *SchedulePath) (*struct{}, error) {
		err := found(ctx, in, tenancy.AbilityManageApps, func(q *db.Queries, id *auth.Identity, m *tenancy.Membership, a db.App, s db.Schedule) error {
			return actionErr(schedules.Delete(ctx, q, m.TeamID, webActor(id), a, s), "could not delete the schedule")
		})
		return nil, err
	})

	type runOut struct{ Body schedules.Run }
	huma.Register(api, huma.Operation{OperationID: "run-app-schedule", Method: http.MethodPost, Path: base + "/schedules/{schedule}/run",
		DefaultStatus: http.StatusAccepted, Tags: tags,
		Summary: "Run the schedule now (also while it is disabled): 202 with the run, status queued — follow it with GET …/runs/{run}. " +
			"next_run_at does not move. 409: a run of it is still going, or nothing is live to run in. 422: an imported app. " +
			"Owner, admin or deployer. Audited.",
	}, func(ctx context.Context, in *SchedulePath) (*runOut, error) {
		out := &runOut{}
		var run db.ScheduleRun
		err := found(ctx, in, tenancy.AbilityDeploy, func(q *db.Queries, id *auth.Identity, _ *tenancy.Membership, a db.App, s db.Schedule) error {
			var err error
			run, err = d.schedules().RunNow(ctx, q, webActor(id), a, s)
			return scheduleErr(err, "could not run the schedule")
		})
		if err != nil {
			return nil, err
		}
		d.schedules().Follow(run) // after the commit: the operation exists for whoever wakes up
		out.Body = schedules.RunView(run)
		return out, nil
	})

	type runsIn struct {
		SchedulePath
		Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
		Before string `query:"before" maxLength:"200" doc:"next_cursor of the previous page."`
	}
	type runsOut struct{ Body schedules.RunFeed }
	huma.Register(api, huma.Operation{OperationID: "list-app-schedule-runs", Method: http.MethodGet, Path: base + "/schedules/{schedule}/runs", Tags: tags,
		Summary: "The schedule's runs, newest first (the newest 200 are kept), without their output. Any member.",
	}, func(ctx context.Context, in *runsIn) (*runsOut, error) {
		out := &runsOut{}
		err := found(ctx, &in.SchedulePath, tenancy.AbilityView, func(q *db.Queries, _ *auth.Identity, _ *tenancy.Membership, _ db.App, s db.Schedule) error {
			f, err := schedules.Runs(ctx, q, s, in.Limit, in.Before)
			if errors.Is(err, deploy.ErrBadCursor) {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			if err != nil {
				return huma.Error500InternalServerError("could not list the runs")
			}
			out.Body = f
			return nil
		})
		return out, err
	})

	type runDetailOut struct{ Body schedules.RunDetail }
	huma.Register(api, huma.Operation{OperationID: "get-app-schedule-run", Method: http.MethodGet, Path: base + "/schedules/{schedule}/runs/{run}", Tags: tags,
		Summary: "One run with its output: the tail (64 KiB) of what the command printed, stored sealed. Like the app's runtime logs it " +
			"may hold secrets: owner, admin or deployer (a viewer gets 403).",
	}, func(ctx context.Context, in *ScheduleRunPath) (*runDetailOut, error) {
		out := &runDetailOut{}
		err := found(ctx, &in.SchedulePath, tenancy.AbilityDeploy, func(q *db.Queries, _ *auth.Identity, _ *tenancy.Membership, _ db.App, s db.Schedule) error {
			run, err := schedules.FindRun(ctx, q, s, in.Run)
			if err != nil {
				return errNotFound
			}
			output, err := d.schedules().Output(ctx, q, run)
			if err != nil {
				return huma.Error500InternalServerError("could not open the run's output")
			}
			out.Body = schedules.RunDetail{Run: schedules.RunView(run), Output: output}
			return nil
		})
		return out, err
	})
}

// scheduleErr is actionErr, except that an imported app is this section's own 422 (there is no
// release to run in) rather than the git-only actions' 409.
func scheduleErr(err error, fallback string) error {
	var ae *deploy.ActionError
	if errors.As(err, &ae) && ae.Status == http.StatusUnprocessableEntity {
		return huma.Error422UnprocessableEntity(ae.Msg)
	}
	return actionErr(err, fallback)
}
