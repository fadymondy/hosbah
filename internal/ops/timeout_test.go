package ops

import (
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// A fast build compiles too (fadymondy-hb's `go build ./cmd/api`, 6-8 minutes under the build caps);
// the 10-minute default cut it off (#715, #716).
func TestFastBuildTimeout(t *testing.T) {
	if got := Timeout(agentproto.KindAppFastBuild); got < 20*time.Minute {
		t.Errorf("fast build timeout %s, want >= 20m", got)
	}
	if Timeout(agentproto.KindAppFastBuild) > Timeout(agentproto.KindAppBuild) {
		t.Error("a fast build must not outlast a full one")
	}
}
