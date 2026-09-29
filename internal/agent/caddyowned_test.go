package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A guest that already runs its own Caddy must keep its sites: Hosbah loads a config only into a
// Caddy that is empty or already serves hosbah_* (or pre-rename cloudy_*) servers.
func TestCaddyLoadRefusesAForeignCaddy(t *testing.T) {
	cases := []struct {
		name, servers string
		status        int
		wantLoad      bool
	}{
		{"foreign Caddyfile", `{"srv0":{"listen":[":443"]}}`, 200, false},
		{"hosbah", `{"hosbah_http":{"listen":[":80"]},"hosbah_https":{}}`, 200, true},
		{"pre-rename", `{"cloudy_http":{"listen":[":80"]}}`, 200, true},
		{"empty servers", `{}`, 200, true},
		{"no http app", `null`, 404, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			loaded := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/config/apps/http/servers":
					w.WriteHeader(c.status)
					_, _ = w.Write([]byte(c.servers))
				case "/load":
					loaded = true
				}
			}))
			defer srv.Close()
			old := caddyAdmin
			caddyAdmin = srv.URL
			defer func() { caddyAdmin = old }()

			err := caddyLoad(t.Context(), map[string]any{"apps": map[string]any{}})
			if loaded != c.wantLoad {
				t.Fatalf("loaded = %v, want %v (err %v)", loaded, c.wantLoad, err)
			}
			if !c.wantLoad && (err == nil || !strings.Contains(err.Error(), "own sites")) {
				t.Fatalf("err = %v, want the foreign-Caddy refusal", err)
			}
		})
	}
}
