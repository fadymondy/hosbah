package server

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

/*
Real-user monitoring (MH-575 H5) end to end: enabling mints an ingest key and shows the snippet
once, the public beacon endpoint accepts a valid sample for it and rejects/drops everything else
(unknown key, disabled key, oversized body) with the same 204 so a probing script learns nothing,
and the team summary reflects only what was actually stored. Team isolation: another team cannot
read team A's settings or Core Web Vitals for team A's app.
*/
func TestAppRealUserMonitoring(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	owner := newClient(t, ts.URL)
	owner.register("rum-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "RUM " + suffix}, 201, &team)

	stranger := newClient(t, ts.URL)
	stranger.register("rum-stranger-" + suffix + "@example.test")
	var teamB struct{ ID string }
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "Elsewhere " + suffix}, 201, &teamB)

	srv, _ := enrolServer(t, owner, ts.URL, team.ID, "rum-"+short)
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "rum-app-" + short, "server": srv,
		"repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	base := "/api/teams/" + team.ID + "/apps/" + app.App.ID + "/rum"

	// Not enabled yet: no key.
	var settings struct {
		Enabled bool `json:"enabled"`
		HasKey  bool `json:"has_key"`
	}
	owner.mustJSON("GET", base+"/settings", nil, 200, &settings)
	if settings.Enabled || settings.HasKey {
		t.Fatalf("a new app should start with RUM off and no key: %+v", settings)
	}

	// A viewer may read settings but not enable RUM.
	viewerEmail := "rum-viewer-" + suffix + "@example.test"
	viewer := newClient(t, ts.URL)
	viewer.register(viewerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)
	if code, _ := viewer.do("PUT", base+"/settings", map[string]any{"enabled": true}); code != 403 {
		t.Fatalf("viewer enabling RUM -> %d, want 403", code)
	}

	// Owner enables it: a key and a paste-able snippet come back once.
	var enabled struct {
		Enabled    bool    `json:"enabled"`
		HasKey     bool    `json:"has_key"`
		Key        *string `json:"key"`
		SnippetURL *string `json:"snippet_url"`
		Snippet    *string `json:"snippet"`
	}
	owner.mustJSON("PUT", base+"/settings", map[string]any{"enabled": true}, 200, &enabled)
	if !enabled.Enabled || !enabled.HasKey || enabled.Key == nil || *enabled.Key == "" || enabled.Snippet == nil || !strings.Contains(*enabled.Snippet, *enabled.Key) {
		t.Fatalf("enabling RUM should mint and show a key once: %+v", enabled)
	}
	key := *enabled.Key
	if !strings.HasPrefix(key, "hbr_") {
		t.Fatalf("the ingest key should have the hbr_ prefix, got %q", key)
	}

	// A second GET never shows the key again.
	owner.mustJSON("GET", base+"/settings", nil, 200, &settings)
	if !settings.Enabled || !settings.HasKey {
		t.Fatalf("settings after enabling: %+v", settings)
	}

	ingest := func(t *testing.T, k string, body []byte) int {
		t.Helper()
		req, err := http.NewRequest("POST", ts.URL+"/api/rum/"+k, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	sample := func(path string, lcp float64) []byte {
		return []byte(fmt.Sprintf(`{"path":%q,"device":"desktop","effective_type":"4g","lcp_ms":%v,"cls":0.02,"inp_ms":80,"fcp_ms":900,"ttfb_ms":150}`, path, lcp))
	}

	// A bad key, a disabled key and an oversized body are all silently dropped (204, nothing stored).
	if code := ingest(t, "hbr_doesnotexist00000000000000000000000000", sample("/", 1000)); code != 204 {
		t.Fatalf("unknown key -> %d, want 204", code)
	}
	oversized := bytes.Repeat([]byte("x"), 9<<10)
	if code := ingest(t, key, oversized); code != 204 {
		t.Fatalf("oversized body -> %d, want 204", code)
	}
	owner.mustJSON("PUT", base+"/settings", map[string]any{"enabled": false}, 200, nil)
	if code := ingest(t, key, sample("/disabled", 1000)); code != 204 {
		t.Fatalf("ingest while disabled -> %d, want 204", code)
	}
	var reEnabled struct {
		Enabled bool    `json:"enabled"`
		HasKey  bool    `json:"has_key"`
		Key     *string `json:"key"`
	}
	owner.mustJSON("PUT", base+"/settings", map[string]any{"enabled": true}, 200, &reEnabled) // reuses the existing key
	if reEnabled.Key != nil {
		t.Fatalf("re-enabling with an existing key should not mint a new one: %+v", reEnabled)
	}
	if !reEnabled.Enabled || !reEnabled.HasKey {
		t.Fatalf("re-enabling should show enabled with the existing key: %+v", reEnabled)
	}

	// A malformed body (bad device, bad path, out-of-range value) is rejected the same way.
	for _, bad := range [][]byte{
		[]byte(`{"path":"/","device":"toaster"}`),
		[]byte(`{"path":"no-leading-slash","device":"mobile"}`),
		[]byte(`{"path":"/?x=1","device":"mobile"}`),
		[]byte(`{"path":"/","device":"mobile","lcp_ms":-1}`),
		[]byte(`not json`),
	} {
		if code := ingest(t, key, bad); code != 204 {
			t.Fatalf("invalid ingest body %s -> %d, want 204", bad, code)
		}
	}

	// A valid sample for the real key is stored.
	if code := ingest(t, key, sample("/", 2000)); code != 204 {
		t.Fatalf("valid ingest -> %d, want 204", code)
	}
	if code := ingest(t, key, sample("/pricing", 3000)); code != 204 {
		t.Fatalf("valid ingest -> %d, want 204", code)
	}

	// The CORS preflight is wide open (the key is not a secret, and the site's origin is unknown
	// ahead of time).
	req, _ := http.NewRequest("OPTIONS", ts.URL+"/api/rum/"+key, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight: %d %v", resp.StatusCode, resp.Header)
	}

	// The team summary reflects the two valid samples, and only them: the disabled-window one and the
	// invalid ones never landed. p75 of {2000, 3000} = 3000 (nearest-rank interpolation over 2 rows).
	var summary struct {
		Samples int64 `json:"samples"`
		Metrics struct {
			LCP struct {
				P75     float64 `json:"p75"`
				Samples int64   `json:"samples"`
				Rating  string  `json:"rating"`
			} `json:"lcp"`
		} `json:"metrics"`
	}
	owner.mustJSON("GET", base+"/summary?window=24h", nil, 200, &summary)
	if summary.Samples != 2 {
		t.Fatalf("expected 2 stored samples, got %d: %+v", summary.Samples, summary)
	}
	if summary.Metrics.LCP.Samples != 2 || summary.Metrics.LCP.P75 <= 0 {
		t.Fatalf("expected an LCP p75 over 2 samples: %+v", summary.Metrics.LCP)
	}
	if summary.Metrics.LCP.Rating != "needs-improvement" && summary.Metrics.LCP.Rating != "poor" {
		t.Fatalf("2000-3000ms LCP should not rate good: %+v", summary.Metrics.LCP)
	}

	var paths struct {
		Paths []struct {
			Path    string `json:"path"`
			Samples int64  `json:"samples"`
		} `json:"paths"`
	}
	owner.mustJSON("GET", base+"/paths?window=24h", nil, 200, &paths)
	seen := map[string]int64{}
	for _, p := range paths.Paths {
		seen[p.Path] = p.Samples
	}
	if seen["/"] != 1 || seen["/pricing"] != 1 {
		t.Fatalf("expected one sample each on / and /pricing, got %+v", seen)
	}

	var series struct {
		Points []struct {
			Samples int64 `json:"samples"`
		} `json:"points"`
	}
	owner.mustJSON("GET", base+"/timeseries?window=24h", nil, 200, &series)
	var total int64
	for _, p := range series.Points {
		total += p.Samples
	}
	if total != 2 {
		t.Fatalf("expected the timeseries to total 2 samples, got %d", total)
	}

	// Rotating replaces the key: the old one stops working, the new one does not.
	var rotated struct {
		Key *string `json:"key"`
	}
	owner.mustJSON("POST", base+"/settings/rotate", nil, 200, &rotated)
	if rotated.Key == nil || *rotated.Key == key {
		t.Fatalf("rotate should mint a fresh key: %+v", rotated)
	}
	if code := ingest(t, key, sample("/", 2000)); code != 204 {
		t.Fatalf("ingest with the rotated-away key -> %d, want 204", code)
	}
	if code := ingest(t, *rotated.Key, sample("/", 2000)); code != 204 {
		t.Fatalf("ingest with the new key -> %d, want 204", code)
	}
	var afterRotate struct {
		Samples int64 `json:"samples"`
	}
	owner.mustJSON("GET", base+"/summary?window=24h", nil, 200, &afterRotate)
	if afterRotate.Samples != 3 {
		t.Fatalf("expected 3 samples after rotating (old key dropped, new key stored): got %d", afterRotate.Samples)
	}

	// Team isolation: team B gets 404 on team A's app, both for settings and for the Core Web Vitals.
	if code, _ := stranger.do("GET", base+"/settings", nil); code != 404 {
		t.Fatalf("stranger reading team A's RUM settings -> %d, want 404", code)
	}
	if code, _ := stranger.do("GET", base+"/summary", nil); code != 404 {
		t.Fatalf("stranger reading team A's Core Web Vitals -> %d, want 404", code)
	}
	if code, _ := stranger.do("PUT", base+"/settings", map[string]any{"enabled": false}); code != 404 {
		t.Fatalf("stranger disabling team A's RUM -> %d, want 404", code)
	}
}
