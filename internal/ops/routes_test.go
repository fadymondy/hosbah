package ops

import (
	"encoding/json"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
)

func TestMovedRelease(t *testing.T) {
	old := int64(33106)
	rec := `{"web":"demo-web-v3","workers":["demo-jobs-v3"],"volumes":["v"],"realtime":{"process":"reverb","container":"demo-rt-v3","port":8080,"host_port":33107,"path":"/app"}}`
	rel := db.Release{ID: "r", HostPort: &old, Containers: &rec}

	hp, cs, moved := movedRelease(rel, agentproto.RouteReport{Container: "demo-web-v3", Upstream: "127.0.0.1:32771",
		RealtimeContainer: "demo-rt-v3", RealtimeUpstream: "127.0.0.1:32772"})
	if hp == nil || *hp != 32771 || moved["web"] != "33106 -> 32771" || moved["realtime"] != "33107 -> 32772" {
		t.Fatalf("hp=%v moved=%v", hp, moved)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(*cs), &got); err != nil {
		t.Fatal(err)
	}
	rt := got["realtime"].(map[string]any)
	if rt["host_port"].(float64) != 32772 || rt["container"] != "demo-rt-v3" || got["web"] != "demo-web-v3" || len(got["workers"].([]any)) != 1 {
		t.Errorf("containers record: %s", *cs)
	}

	// Another container (an older release, another app) never moves the live release.
	hp, _, moved = movedRelease(rel, agentproto.RouteReport{Container: "demo-web-v2", Upstream: "127.0.0.1:32771"})
	if len(moved) != 0 || *hp != 33106 {
		t.Errorf("foreign container moved the release: %v", moved)
	}
	// Already in step.
	cur := int64(32771)
	rel.HostPort = &cur
	if _, _, moved = movedRelease(rel, agentproto.RouteReport{Container: "demo-web-v3", Upstream: "127.0.0.1:32771"}); len(moved) != 0 {
		t.Errorf("no-op reported a move: %v", moved)
	}
	// A bad upstream is ignored.
	if _, _, moved = movedRelease(rel, agentproto.RouteReport{Container: "demo-web-v3", Upstream: "10.0.0.1:80"}); len(moved) != 0 {
		t.Errorf("non-loopback upstream applied: %v", moved)
	}
}
