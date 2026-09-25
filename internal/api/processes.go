package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/processes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The "Workers" section of a site: status, restart, stop and start of one or every process of an app's
live release, run through the agent's app.processes operation (see internal/processes,
internal/agentproto). Read and write both need owner, admin or deployer (the same ability as
restarting the whole app): a worker's environment and command can be as sensitive as its logs.

409: nothing is live, or the server is offline. 422: an imported app, a bad process, or web asked to
stop or start. 502: the operation failed. 504: no answer in time.
*/

// processesErr maps the section's errors like queueErr.
func processesErr(err error) error {
	var ae *deploy.ActionError
	if errors.As(err, &ae) {
		switch ae.Status {
		case http.StatusUnprocessableEntity:
			return huma.Error422UnprocessableEntity(ae.Msg)
		case http.StatusBadGateway:
			return huma.Error502BadGateway(ae.Msg)
		case http.StatusGatewayTimeout:
			return huma.Error504GatewayTimeout(ae.Msg)
		}
	}
	return actionErr(err, "could not read the app's processes")
}

// processesRounds is the caller's side of internal/processes: every round is a transaction of the
// member's team, after the role check, with the app resolved in it.
func (d *deps) processesRounds(p *AppPath, ability string) processes.InTeam {
	return func(ctx context.Context, fn processes.Round) error {
		return d.inTeam(ctx, p.Team, ability, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, p.App)
			if err != nil {
				return err
			}
			return fn(q, m.TeamID, webActor(id), a)
		})
	}
}

// ProcessStatusOut is one process of an app's live release.
type ProcessStatusOut struct {
	Process       string  `json:"process"`
	Container     string  `json:"container"`
	State         string  `json:"state"`
	Status        string  `json:"status,omitempty"`
	StartedAt     string  `json:"started_at,omitempty"`
	RestartCount  int     `json:"restart_count"`
	CPUPercent    float64 `json:"cpu_percent,omitempty"`
	MemBytes      int64   `json:"mem_bytes,omitempty"`
	MemLimitBytes int64   `json:"mem_limit_bytes,omitempty"`
	Image         string  `json:"image,omitempty"`
	Deployment    string  `json:"deployment,omitempty"`
}

// ProcessesOut is the app's processes, as the API shows them.
type ProcessesOut struct {
	Processes []ProcessStatusOut `json:"processes"`
	Operation string             `json:"operation"`
}

func toProcessesOut(r processes.Result) ProcessesOut {
	out := ProcessesOut{Processes: []ProcessStatusOut{}, Operation: r.Operation}
	for _, p := range r.Processes.Processes {
		out.Processes = append(out.Processes, ProcessStatusOut{Process: p.Process, Container: p.Container, State: p.State, Status: p.Status,
			StartedAt: p.StartedAt, RestartCount: p.RestartCount, CPUPercent: p.CPUPercent, MemBytes: p.MemBytes,
			MemLimitBytes: p.MemLimitBytes, Image: p.Image, Deployment: p.Deployment})
	}
	return out
}

// ProcessActionIn is what a process action asks: process empty means every process (restart only).
type ProcessActionIn struct {
	AppPath
	Body struct {
		Process string `json:"process,omitempty" maxLength:"40" doc:"web or worker:<name>; empty means every process (restart only)."`
		Action  string `json:"action" enum:"restart,stop,start" doc:"stop and start are refused for web — only a worker process may be stopped or started here."`
	}
}

func (d *deps) registerProcesses(api huma.API) {
	tags := []string{"Processes"}
	base := "/api/teams/{team}/apps/{app}/processes"
	svc := func() (*processes.Service, error) {
		if s := processes.From(d.a); s != nil {
			return s, nil
		}
		return nil, huma.Error503ServiceUnavailable("operations are not available on this control plane")
	}

	type out struct{ Body ProcessesOut }
	huma.Register(api, huma.Operation{OperationID: "get-app-processes", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The app's processes — web and every worker of its live release: each container's state, uptime, restart count and, " +
			"while running, CPU and memory use. Waits up to 60s for the server. Owner, admin or deployer. 409: nothing live or the " +
			"server is offline; 422: an imported app.",
	}, func(ctx context.Context, in *AppPath) (*out, error) {
		s, err := svc()
		if err != nil {
			return nil, err
		}
		res, err := s.Status(ctx, d.processesRounds(in, tenancy.AbilityDeploy), "")
		if err != nil {
			return nil, processesErr(err)
		}
		return &out{Body: toProcessesOut(res)}, nil
	})

	huma.Register(api, huma.Operation{OperationID: "act-on-app-process", Method: http.MethodPost, Path: base + "/action", Tags: tags,
		Summary: "Restart, stop or start one process — or, for restart alone, every process at once (process left empty). Stop and start " +
			"are refused for web: stopping it takes the site down; only a worker may be stopped or started here. stop does not remove " +
			"the container, so start reuses it. Waits for the command (up to 60 s). Owner, admin or deployer. Audited. 409: nothing " +
			"live or the server is offline; 422: an imported app, a bad process, or web asked to stop or start; 502; 504.",
	}, func(ctx context.Context, in *ProcessActionIn) (*out, error) {
		if in.Body.Process != "" && !agentproto.ValidProcess(in.Body.Process) {
			return nil, huma.Error422UnprocessableEntity("bad process")
		}
		s, err := svc()
		if err != nil {
			return nil, err
		}
		res, err := s.Act(ctx, d.processesRounds(&in.AppPath, tenancy.AbilityDeploy), in.Body.Action, in.Body.Process)
		if err != nil {
			return nil, processesErr(err)
		}
		return &out{Body: toProcessesOut(res)}, nil
	})
}
