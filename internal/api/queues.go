package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/queues"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The "Queues" section of a site: its job queues and its failed jobs, read live from the app's own
release by running the framework's CLI inside the live web container (the agent's app.exec, a fixed
argv — see internal/queues). Nothing is stored.

Read: whoever may read the app's runtime logs (deploy) — a failed job's name and exception can hold
data, like a log line. Write: whoever may manage the app (manage_apps). 422: an imported app, a
driver without a readable failed-job list, a bad job id. 409: nothing is live, or the server is
offline. 502: the operation failed. 504: no answer in time.
*/

// QueueJobPath addresses one failed job of an app.
type QueueJobPath struct {
	AppPath
	Job string `path:"job" maxLength:"200" doc:"The failed job's id: 1 to 120 characters of A-Z a-z 0-9 _ . : - (not starting with a dash)."`
}

// queueErr maps the section's errors: actionErr's, and the gateway statuses of a command that
// failed (502) or was not answered (504).
func queueErr(err error) error {
	var ae *deploy.ActionError
	if errors.As(err, &ae) {
		switch ae.Status {
		case http.StatusUnprocessableEntity:
			return huma.Error422UnprocessableEntity(ae.Msg) // an imported app is this section's own 422
		case http.StatusBadGateway:
			return huma.Error502BadGateway(ae.Msg)
		case http.StatusGatewayTimeout:
			return huma.Error504GatewayTimeout(ae.Msg)
		}
	}
	return actionErr(err, "could not read the app's queues")
}

// queueRounds is the caller's side of internal/queues: every round is a transaction of the member's
// team, after the role check, with the app resolved in it.
func (d *deps) queueRounds(p *AppPath, ability string) queues.InTeam {
	return func(ctx context.Context, fn queues.Round) error {
		return d.inTeam(ctx, p.Team, ability, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, p.App)
			if err != nil {
				return err
			}
			return fn(q, m.TeamID, webActor(id), a)
		})
	}
}

func (d *deps) registerQueues(api huma.API) {
	tags := []string{"Queues"}
	base := "/api/teams/{team}/apps/{app}/queues"
	svc := func() (*queues.Service, error) {
		if s := queues.From(d.a); s != nil {
			return s, nil
		}
		return nil, huma.Error503ServiceUnavailable("operations are not available on this control plane")
	}

	type overviewOut struct{ Body queues.QueueOverview }
	huma.Register(api, huma.Operation{OperationID: "get-app-queues", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The app's job queues, read live inside its live release. driver is decided from the app's runtime, the processes of its " +
			"live release and the names of its environment variables; confidence and evidence say how sure that is. laravel is read " +
			"through artisan, celery through celery inspect (when a worker process names its app); bullmq and asynq are reported with " +
			"readable false — nothing is guessed. Waits up to 50 s for the server. Owner, admin or deployer. 422: imported app; " +
			"409: nothing live or the server is offline; 502: the operation failed; 504: no answer.",
	}, func(ctx context.Context, in *AppPath) (*overviewOut, error) {
		s, err := svc()
		if err != nil {
			return nil, err
		}
		ov, err := s.Overview(ctx, d.queueRounds(in, tenancy.AbilityDeploy))
		if err != nil {
			return nil, queueErr(err)
		}
		return &overviewOut{Body: ov}, nil
	})

	type failedOut struct{ Body queues.FailedJobsPage }
	huma.Register(api, huma.Operation{OperationID: "list-app-failed-jobs", Method: http.MethodGet, Path: base + "/failed", Tags: tags,
		Summary: "The app's failed jobs (laravel: php artisan queue:failed): id, queue, connection, name, failed_at and the first line of " +
			"the exception — never a job's payload. Owner, admin or deployer. 422: imported app, or a driver without a readable " +
			"failed-job list; 409; 502 also when the output could not be read; 504.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Limit int `query:"limit" minimum:"1" maximum:"200" default:"50"`
	}) (*failedOut, error) {
		s, err := svc()
		if err != nil {
			return nil, err
		}
		page, err := s.Failed(ctx, d.queueRounds(&in.AppPath, tenancy.AbilityDeploy), in.Limit)
		if err != nil {
			return nil, queueErr(err)
		}
		return &failedOut{Body: page}, nil
	})

	type actionOut struct{ Body queues.QueueActionResult }
	act := func(ctx context.Context, p *AppPath, action, job string) (*actionOut, error) {
		s, err := svc()
		if err != nil {
			return nil, err
		}
		res, err := s.Act(ctx, d.queueRounds(p, tenancy.AbilityManageApps), action, job)
		if err != nil {
			return nil, queueErr(err)
		}
		return &actionOut{Body: res}, nil
	}
	const actionNote = " Waits for the command (up to 150 s). Owner or admin. Audited. 422: imported app, a driver without failed jobs, " +
		"or a bad id; 409: nothing live or the server is offline; 502: the command failed; 504: no answer."

	// The fixed paths are registered before the {job} ones.
	huma.Register(api, huma.Operation{OperationID: "retry-all-app-failed-jobs", Method: http.MethodPost, Path: base + "/failed/retry-all", Tags: tags,
		Summary: "Put every failed job back on its queue (php artisan queue:retry all)." + actionNote,
	}, func(ctx context.Context, in *AppPath) (*actionOut, error) {
		return act(ctx, in, queues.ActionRetryAll, "")
	})

	huma.Register(api, huma.Operation{OperationID: "flush-app-failed-jobs", Method: http.MethodDelete, Path: base + "/failed", Tags: tags,
		Summary: "Delete every failed job (php artisan queue:flush). They cannot be retried afterwards." + actionNote,
	}, func(ctx context.Context, in *AppPath) (*actionOut, error) {
		return act(ctx, in, queues.ActionFlush, "")
	})

	huma.Register(api, huma.Operation{OperationID: "retry-app-failed-job", Method: http.MethodPost, Path: base + "/failed/{job}/retry", Tags: tags,
		Summary: "Put one failed job back on its queue (php artisan queue:retry <id>)." + actionNote,
	}, func(ctx context.Context, in *QueueJobPath) (*actionOut, error) {
		return act(ctx, &in.AppPath, queues.ActionRetry, in.Job)
	})

	huma.Register(api, huma.Operation{OperationID: "forget-app-failed-job", Method: http.MethodDelete, Path: base + "/failed/{job}", Tags: tags,
		Summary: "Delete one failed job (php artisan queue:forget <id>)." + actionNote,
	}, func(ctx context.Context, in *QueueJobPath) (*actionOut, error) {
		return act(ctx, &in.AppPath, queues.ActionForget, in.Job)
	})
}
