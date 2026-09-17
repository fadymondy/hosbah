package dbsvc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/tenancy"
)

// The backup scheduler: a single loop that wakes every 30s, claims due schedules (next_run_at in the
// past, not paused) under the system scope, and for each runs a scheduled backup and — when the
// schedule asks — a restore-test, then advances next_run_at from the cron expression.

// ScheduleInput is what the Backups view sends.
type ScheduleInput struct {
	Cron        string
	Retention   int
	Target      string
	RestoreTest bool
	Paused      bool
}

// SetSchedule creates or updates a service's single backup schedule.
func (s *Service) SetSchedule(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, in ScheduleInput) (db.BackupSchedule, error) {
	in.Cron = strings.TrimSpace(in.Cron)
	if _, err := cronNext(in.Cron, time.Now().UTC()); err != nil {
		return db.BackupSchedule{}, invalid("that is not a valid cron expression (five fields, e.g. 0 3 * * *)")
	}
	if in.Target == "" {
		in.Target = "local"
	}
	if in.Target != "local" && !s.remoteConfigured(in.Target) {
		return db.BackupSchedule{}, conflict("the " + in.Target + " backup target is not configured")
	}
	if in.Retention <= 0 {
		in.Retention = 7
	}
	next, _ := cronNext(in.Cron, time.Now().UTC())
	existing, err := q.BackupScheduleOfService(ctx, db.BackupScheduleOfServiceParams{DataServiceID: svc.ID, TeamID: teamID})
	if err != nil {
		row, err := q.InsertBackupSchedule(ctx, db.InsertBackupScheduleParams{
			ID: newUUID(), TeamID: teamID, DataServiceID: svc.ID, Cron: in.Cron, Retention: int64(in.Retention),
			Target: in.Target, RestoreTest: in.RestoreTest, Paused: in.Paused, NextRunAt: timeptr(next), CreatedBy: actor.UserID,
		})
		if err != nil {
			return db.BackupSchedule{}, err
		}
		_ = tenancy.Record(ctx, q, teamID, actor.UserID, "backup_schedule.set", "data_service", svc.ID, auditExtra(actor, map[string]any{"cron": in.Cron}))
		return row, nil
	}
	if err := q.UpdateBackupSchedule(ctx, db.UpdateBackupScheduleParams{
		ID: existing.ID, TeamID: teamID, Cron: in.Cron, Retention: int64(in.Retention), Target: in.Target,
		RestoreTest: in.RestoreTest, Paused: in.Paused, NextRunAt: timeptr(next),
	}); err != nil {
		return db.BackupSchedule{}, err
	}
	_ = tenancy.Record(ctx, q, teamID, actor.UserID, "backup_schedule.set", "data_service", svc.ID, auditExtra(actor, map[string]any{"cron": in.Cron}))
	existing.Cron, existing.Retention, existing.Target, existing.RestoreTest, existing.Paused, existing.NextRunAt = in.Cron, int64(in.Retention), in.Target, in.RestoreTest, in.Paused, timeptr(next)
	return existing, nil
}

// StartScheduler launches the background loop.
func (s *Service) StartScheduler() {
	ctx, cancel := context.WithCancel(context.Background())
	s.schedCancel = cancel
	go s.scheduleLoop(ctx)
}

// StopScheduler ends it.
func (s *Service) StopScheduler() {
	if s.schedCancel != nil {
		s.schedCancel()
	}
}

func (s *Service) scheduleLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.runDue(ctx)
		}
	}
}

// runDue claims and runs the schedules whose time has come.
func (s *Service) runDue(ctx context.Context) {
	var due []db.BackupSchedule
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		due, err = q.DueBackupSchedules(ctx, 20)
		return err
	})
	if err != nil || len(due) == 0 {
		return
	}
	for _, sch := range due {
		// Advance next_run_at first (under system scope) so a slow backup does not double-fire.
		next, cerr := cronNext(sch.Cron, time.Now().UTC())
		_ = tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
			if cerr != nil {
				return nil
			}
			return q.MarkBackupScheduleRun(ctx, db.MarkBackupScheduleRunParams{ID: sch.ID, NextRunAt: timeptr(next)})
		})
		s.runScheduledBackup(ctx, sch)
	}
}

func (s *Service) runScheduledBackup(ctx context.Context, sch db.BackupSchedule) {
	var svc db.DataService
	var b db.Backup
	err := s.sys(ctx, sch.TeamID, func(q *db.Queries) error {
		var err error
		if svc, err = q.DataServiceInTeam(ctx, db.DataServiceInTeamParams{ID: sch.DataServiceID, TeamID: sch.TeamID}); err != nil {
			return err
		}
		if svc.Status != "running" {
			return errors.New("service not running")
		}
		b, err = q.InsertBackup(ctx, db.InsertBackupParams{
			ID: newUUID(), TeamID: sch.TeamID, DataServiceID: svc.ID, ScheduleID: strptr(sch.ID), Kind: "scheduled",
			Status: "pending", Target: sch.Target, RestoreStatus: strptr("untested"), CreatedBy: svc.CreatedBy,
		})
		return err
	})
	if err != nil {
		return
	}
	s.runBackup(ctx, sch.TeamID, svc, b, int(sch.Retention))
	if !sch.RestoreTest {
		return
	}
	// Re-read the backup to confirm it succeeded, then restore-test it.
	_ = s.sys(ctx, sch.TeamID, func(q *db.Queries) error {
		cur, err := q.BackupInTeam(ctx, db.BackupInTeamParams{ID: b.ID, TeamID: sch.TeamID})
		if err == nil && cur.Status == "succeeded" {
			b = cur
		} else {
			b.Status = "failed"
		}
		return nil
	})
	if b.Status == "succeeded" {
		_ = s.sys(ctx, sch.TeamID, func(q *db.Queries) error {
			return q.SetBackupRestoreTesting(ctx, db.SetBackupRestoreTestingParams{ID: b.ID, TeamID: sch.TeamID})
		})
		s.runRestoreTest(ctx, sch.TeamID, svc, b)
	}
}

// ---- a small 5-field cron evaluator ------------------------------------------------------------

// cronNext returns the next minute at or after `after`+1min that matches a standard 5-field cron
// (minute hour day-of-month month day-of-week), supporting *, ranges a-b, lists a,b and steps */n.
func cronNext(spec string, after time.Time) (time.Time, error) {
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return time.Time{}, errors.New("cron needs five fields")
	}
	mins, err := cronField(fields[0], 0, 59)
	if err != nil {
		return time.Time{}, err
	}
	hours, err := cronField(fields[1], 0, 23)
	if err != nil {
		return time.Time{}, err
	}
	doms, err := cronField(fields[2], 1, 31)
	if err != nil {
		return time.Time{}, err
	}
	months, err := cronField(fields[3], 1, 12)
	if err != nil {
		return time.Time{}, err
	}
	dows, err := cronField(fields[4], 0, 6)
	if err != nil {
		return time.Time{}, err
	}
	t := after.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ {
		if mins[t.Minute()] && hours[t.Hour()] && months[int(t.Month())] &&
			doms[t.Day()] && dows[int(t.Weekday())] {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, errors.New("no matching time within a year")
}

func cronField(f string, min, max int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, part := range strings.Split(f, ",") {
		step := 1
		rng := part
		if i := strings.Index(part, "/"); i >= 0 {
			var err error
			if step, err = strconv.Atoi(part[i+1:]); err != nil || step < 1 {
				return nil, fmt.Errorf("bad step in %q", part)
			}
			rng = part[:i]
		}
		lo, hi := min, max
		if rng != "*" {
			if i := strings.Index(rng, "-"); i >= 0 {
				var err1, err2 error
				lo, err1 = strconv.Atoi(rng[:i])
				hi, err2 = strconv.Atoi(rng[i+1:])
				if err1 != nil || err2 != nil {
					return nil, fmt.Errorf("bad range %q", rng)
				}
			} else {
				v, err := strconv.Atoi(rng)
				if err != nil {
					return nil, fmt.Errorf("bad value %q", rng)
				}
				lo, hi = v, v
			}
		}
		if lo < min || hi > max || lo > hi {
			return nil, fmt.Errorf("%q out of range %d-%d", part, min, max)
		}
		for v := lo; v <= hi; v += step {
			out[v] = true
		}
	}
	return out, nil
}
