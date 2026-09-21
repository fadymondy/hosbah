package schedules

import (
	"errors"
	"strings"
	"time"
	_ "time/tzdata" // IANA zones whatever the control plane's image ships

	"github.com/robfig/cron/v3"
)

// Only the parser of robfig/cron is used: five fields (minute hour day-of-month month day-of-week),
// no seconds, no @descriptors. Firing is the scheduler's own claim loop (service.go).
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Spec is a validated cron expression in its timezone.
type Spec struct {
	Cron     string // normalised: single spaces
	Timezone string
	sched    cron.Schedule
	loc      *time.Location
}

// ParseSpec validates a 5-field cron expression and an IANA timezone ("" = UTC). The errors are
// meant for the person typing the expression.
func ParseSpec(expr, timezone string) (Spec, error) {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		timezone = "UTC"
	}
	// "Local" is whatever the control plane's host is set to — not a zone a team can mean.
	if len(timezone) > 64 || timezone == "Local" {
		return Spec{}, errors.New("timezone must be an IANA name such as UTC or Europe/Berlin")
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return Spec{}, errors.New("timezone must be an IANA name such as UTC or Europe/Berlin")
	}
	fields := strings.Fields(expr)
	if len(expr) > 200 {
		return Spec{}, errors.New("cron is longer than 200 characters")
	}
	if len(fields) != 5 {
		return Spec{}, errors.New("cron must have 5 fields: minute hour day-of-month month day-of-week")
	}
	norm := strings.Join(fields, " ")
	// The parser would read a TZ= / CRON_TZ= prefix as a zone of its own; the zone is a field here.
	if strings.ContainsAny(norm, "=@") {
		return Spec{}, errors.New("cron must have 5 fields: minute hour day-of-month month day-of-week")
	}
	sched, err := cronParser.Parse(norm)
	if err != nil {
		return Spec{}, errors.New("cron: " + strings.TrimPrefix(err.Error(), "failed to parse "))
	}
	if ss, ok := sched.(*cron.SpecSchedule); ok {
		ss.Location = loc
	}
	s := Spec{Cron: norm, Timezone: timezone, sched: sched, loc: loc}
	if s.Next(time.Now()).IsZero() {
		return Spec{}, errors.New("cron never fires (a date like 30 February?)")
	}
	return s, nil
}

// Next is the first fire time after t (UTC), or the zero time when there is none.
func (s Spec) Next(t time.Time) time.Time {
	n := s.sched.Next(t.In(s.loc))
	if n.IsZero() {
		return n
	}
	return n.UTC()
}

// NextN is the next n fire times after t.
func (s Spec) NextN(t time.Time, n int) []time.Time {
	out := make([]time.Time, 0, n)
	for i := 0; i < n; i++ {
		if t = s.Next(t); t.IsZero() {
			break
		}
		out = append(out, t)
	}
	return out
}
