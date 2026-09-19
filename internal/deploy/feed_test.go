package deploy

import (
	"errors"
	"testing"
	"time"
)

func TestFeedCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 19, 12, 0, 0, 123456000, time.UTC)
	gotAt, gotID, err := decodeCursor(encodeCursor(at, "abc-123"))
	if err != nil || !gotAt.Equal(at) || gotID != "abc-123" {
		t.Fatalf("round trip: %v %q %v", gotAt, gotID, err)
	}
	for _, bad := range []string{"bogus!", "bm8tcGlwZQ", encodeCursor(at, "")} {
		if _, _, err := decodeCursor(bad); !errors.Is(err, ErrBadCursor) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
