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
	"github.com/fadymondy/hosbah/internal/monitors"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The "Alerts & monitors" section of a site: HTTP checks of the app's own hostnames and CPU / memory /
disk rules over its server's metrics, and the incidents they opened (read: any member; write:
whoever may manage the app). An http monitor's target is always on one of the app's own hostnames —
the control plane fetches it, so anything else is refused (422). Imported apps have monitors too.
*/

// MonitorPath addresses one monitor of an app (id or name).
type MonitorPath struct {
	AppPath
	Monitor string `path:"monitor" maxLength:"80" doc:"Monitor id or name."`
}

// MonitorCreateIn is a new monitor.
type MonitorCreateIn struct {
	Name                     string   `json:"name" minLength:"1" maxLength:"80" doc:"Unique in the app."`
	Kind                     string   `json:"kind" enum:"http,cpu,memory,disk,heartbeat"`
	Target                   *string  `json:"target,omitempty" maxLength:"500" doc:"http: an http(s):// URL on one of the app's own hostnames. Not for cpu, memory, disk, heartbeat."`
	ExpectedStatus           *int     `json:"expected_status,omitempty" doc:"http: the status a check must answer (default 200); 0 = any 2xx or 3xx."`
	Threshold                *float64 `json:"threshold,omitempty" doc:"http: the slowest acceptable response in ms, 0 to 60000 (default 0 = no limit). cpu, memory, disk: percent, 1 to 100 (required)."`
	WindowMinutes            *int     `json:"window_minutes,omitempty" doc:"cpu, memory, disk: minutes the average is taken over, 1 to 60 (default 5)."`
	IntervalSeconds          *int     `json:"interval_seconds,omitempty" doc:"How often it is checked, 30 to 3600 (default 60). Unused by heartbeat."`
	Enabled                  *bool    `json:"enabled,omitempty" doc:"Default true."`
	NotifyEmails             []string `json:"notify_emails,omitempty" maxItems:"10" doc:"Who is emailed when an incident opens or resolves."`
	NotifyWebhook            *string  `json:"notify_webhook,omitempty" maxLength:"500" doc:"An https:// URL that is POSTed {event, monitor, app, reason, value, at}; \"\" = none."`
	NotifyChannelIDs         []string `json:"notify_channel_ids,omitempty" maxItems:"10" doc:"Team notification channels (internal/notifications) this monitor's incidents also reach."`
	HeartbeatIntervalSeconds *int     `json:"heartbeat_interval_seconds,omitempty" doc:"heartbeat: how often a ping is expected, 30 to 86400 (default 300)."`
	HeartbeatGraceSeconds    *int     `json:"heartbeat_grace_seconds,omitempty" doc:"heartbeat: how late a ping may be before an incident opens, 0 to 86400 (default 120)."`
}

// MonitorPatchIn changes a monitor. Omit a field to leave it. The kind never changes.
type MonitorPatchIn struct {
	Name                     *string  `json:"name,omitempty" minLength:"1" maxLength:"80"`
	Target                   *string  `json:"target,omitempty" maxLength:"500"`
	ExpectedStatus           *int     `json:"expected_status,omitempty"`
	Threshold                *float64 `json:"threshold,omitempty"`
	WindowMinutes            *int     `json:"window_minutes,omitempty"`
	IntervalSeconds          *int     `json:"interval_seconds,omitempty"`
	Enabled                  *bool    `json:"enabled,omitempty" doc:"false also closes the monitor's open incident, quietly."`
	NotifyEmails             []string `json:"notify_emails,omitempty" maxItems:"10"`
	NotifyWebhook            *string  `json:"notify_webhook,omitempty" maxLength:"500"`
	NotifyChannelIDs         []string `json:"notify_channel_ids,omitempty" maxItems:"10"`
	HeartbeatIntervalSeconds *int     `json:"heartbeat_interval_seconds,omitempty"`
	HeartbeatGraceSeconds    *int     `json:"heartbeat_grace_seconds,omitempty"`
}

func (d *deps) monitors() *monitors.Service {
	if d.a == nil {
		return nil
	}
	s, _ := d.a.Monitors.(*monitors.Service)
	return s
}

func (d *deps) registerMonitors(api huma.API) {
	tags := []string{"Alerts & monitors"}
	base := "/api/teams/{team}/apps/{app}"

	type monitorListOut struct {
		Body struct {
			Monitors []monitors.Monitor `json:"monitors"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-app-monitors", Method: http.MethodGet, Path: base + "/monitors", Tags: tags,
		Summary: "The app's monitors, each with its state and its open incident (null when none). Any member.",
	}, func(ctx context.Context, in *AppPath) (*monitorListOut, error) {
		out := &monitorListOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			out.Body.Monitors, err = monitors.List(ctx, q, m.TeamID, a)
			return actionErr(err, "could not list the monitors")
		})
		return out, err
	})

	type monitorOut struct{ Body monitors.Monitor }
	huma.Register(api, huma.Operation{OperationID: "create-app-monitor", Method: http.MethodPost, Path: base + "/monitors",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Add a monitor. kind http: GET target every interval_seconds, expecting expected_status within threshold ms; an incident " +
			"opens on the second failure in a row. kind cpu | memory | disk: the app's server's average over window_minutes against " +
			"threshold percent; no metrics = unknown, not failing. 422: a bad field, or a target that is not on one of the app's own " +
			"hostnames. 409: the name is taken, or the app has 20 monitors. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body MonitorCreateIn
	}) (*monitorOut, error) {
		out := &monitorOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			b := in.Body
			out.Body, err = monitors.Create(ctx, q, m.TeamID, webActor(id), a, monitors.Input{Name: &b.Name, Kind: b.Kind, Target: b.Target,
				ExpectedStatus: b.ExpectedStatus, Threshold: b.Threshold, WindowMinutes: b.WindowMinutes, IntervalSeconds: b.IntervalSeconds,
				Enabled: b.Enabled, NotifyEmails: emailsIn(b.NotifyEmails), NotifyWebhook: b.NotifyWebhook, NotifyChannelIDs: emailsIn(b.NotifyChannelIDs),
				HeartbeatIntervalSeconds: b.HeartbeatIntervalSeconds, HeartbeatGraceSeconds: b.HeartbeatGraceSeconds})
			return actionErr(err, "could not create the monitor")
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "get-app-monitor", Method: http.MethodGet, Path: base + "/monitors/{monitor}", Tags: tags,
		Summary: "One monitor of the app. Any member.",
	}, func(ctx context.Context, in *MonitorPath) (*monitorOut, error) {
		out := &monitorOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			mon, err := monitors.Find(ctx, q, m.TeamID, a, in.Monitor)
			if err != nil {
				return errNotFound
			}
			out.Body, err = monitors.View(ctx, q, mon)
			return actionErr(err, "could not read the monitor")
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-app-monitor", Method: http.MethodPatch, Path: base + "/monitors/{monitor}", Tags: tags,
		Summary: "Change a monitor's settings (never its kind). Changing the rule, or switching it off, forgets its state and closes its " +
			"open incident without an announcement. 422 and 409 as for creating one. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		MonitorPath
		Body MonitorPatchIn
	}) (*monitorOut, error) {
		out := &monitorOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			mon, err := monitors.Find(ctx, q, m.TeamID, a, in.Monitor)
			if err != nil {
				return errNotFound
			}
			b := in.Body
			out.Body, err = monitors.Update(ctx, q, m.TeamID, webActor(id), a, mon, monitors.Input{Name: b.Name, Target: b.Target,
				ExpectedStatus: b.ExpectedStatus, Threshold: b.Threshold, WindowMinutes: b.WindowMinutes, IntervalSeconds: b.IntervalSeconds,
				Enabled: b.Enabled, NotifyEmails: emailsIn(b.NotifyEmails), NotifyWebhook: b.NotifyWebhook, NotifyChannelIDs: emailsIn(b.NotifyChannelIDs),
				HeartbeatIntervalSeconds: b.HeartbeatIntervalSeconds, HeartbeatGraceSeconds: b.HeartbeatGraceSeconds}, d.monitors().Now())
			return actionErr(err, "could not update the monitor")
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-app-monitor", Method: http.MethodDelete, Path: base + "/monitors/{monitor}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Remove a monitor and its incidents. Owner or admin. Audited.",
	}, func(ctx context.Context, in *MonitorPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			mon, err := monitors.Find(ctx, q, m.TeamID, a, in.Monitor)
			if err != nil {
				return errNotFound
			}
			return actionErr(monitors.Delete(ctx, q, m.TeamID, webActor(id), a, mon), "could not delete the monitor")
		})
		return nil, err
	})

	huma.Register(api, huma.Operation{OperationID: "check-app-monitor", Method: http.MethodPost, Path: base + "/monitors/{monitor}/check", Tags: tags,
		Summary: "Check the monitor now (an http check waits up to 10 s) and return it as it is afterwards. It counts like a scheduled " +
			"check: it can open or resolve the incident and announce it. 409: the monitor is disabled. Owner or admin.",
	}, func(ctx context.Context, in *MonitorPath) (*monitorOut, error) {
		out := &monitorOut{}
		var mon db.Monitor
		find := func(ability string, then func(q *db.Queries) error) error {
			return d.inTeam(ctx, in.Team, ability, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
				a, err := appIn(ctx, q, m, in.App)
				if err != nil {
					return err
				}
				if mon, err = monitors.Find(ctx, q, m.TeamID, a, in.Monitor); err != nil {
					return errNotFound
				}
				return then(q)
			})
		}
		if err := find(tenancy.AbilityManageApps, func(*db.Queries) error { return nil }); err != nil {
			return nil, err
		}
		// The check itself runs outside the request's transaction (it can take 10 s).
		if _, err := d.monitors().CheckNow(ctx, mon); err != nil {
			return nil, actionErr(err, "could not check the monitor")
		}
		err := find(tenancy.AbilityManageApps, func(q *db.Queries) error {
			var err error
			out.Body, err = monitors.View(ctx, q, mon)
			return actionErr(err, "could not read the monitor")
		})
		return out, err
	})

	type incidentsIn struct {
		AppPath
		Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
		Before string `query:"before" maxLength:"200" doc:"next_cursor of the previous page."`
	}
	type incidentsOut struct{ Body monitors.IncidentFeed }
	huma.Register(api, huma.Operation{OperationID: "list-app-incidents", Method: http.MethodGet, Path: base + "/incidents", Tags: tags,
		Summary: "The incidents of the app's monitors, newest first. Any member.",
	}, func(ctx context.Context, in *incidentsIn) (*incidentsOut, error) {
		out := &incidentsOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			f, err := monitors.Incidents(ctx, q, m.TeamID, a, in.Limit, in.Before)
			if errors.Is(err, deploy.ErrBadCursor) {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			if err != nil {
				return huma.Error500InternalServerError("could not list the incidents")
			}
			out.Body = f
			return nil
		})
		return out, err
	})
}

// emailsIn: an omitted notify_emails is nil (unchanged); [] clears it.
func emailsIn(v []string) *[]string {
	if v == nil {
		return nil
	}
	return &v
}
