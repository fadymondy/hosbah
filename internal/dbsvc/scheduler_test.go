package dbsvc

import (
	"testing"
	"time"
)

func TestCronNext(t *testing.T) {
	base := time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)
	cases := []struct {
		spec string
		want string
	}{
		{"0 3 * * *", "2026-09-18T03:00:00Z"},   // daily at 03:00, next day
		{"*/15 * * * *", "2026-09-17T10:45:00Z"}, // every 15 min
		{"30 10 * * *", "2026-09-18T10:30:00Z"},  // same minute today already passed (>= after+1)
		{"0 0 1 * *", "2026-10-01T00:00:00Z"},    // first of the month
	}
	for _, c := range cases {
		got, err := cronNext(c.spec, base)
		if err != nil {
			t.Fatalf("%s: %v", c.spec, err)
		}
		if got.Format(time.RFC3339) != c.want {
			t.Errorf("%s: got %s want %s", c.spec, got.Format(time.RFC3339), c.want)
		}
	}
	if _, err := cronNext("bad", base); err == nil {
		t.Fatal("a bad cron should error")
	}
	if _, err := cronNext("60 * * * *", base); err == nil {
		t.Fatal("minute 60 is out of range")
	}
}
