package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/push"
)

// bearer calls the API the way the phone and desktop apps do: no cookies, a Bearer session token.
func bearer(t *testing.T, base, token, method, path string, body any) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

type refreshOut struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	DeviceID     string `json:"device_id"`
	Code         string `json:"code"`
}

func refresh(t *testing.T, base, token string, want int) refreshOut {
	t.Helper()
	status, text := rawPost(t, base+"/api/auth/device/refresh", map[string]any{"refresh_token": token}, nil)
	if status != want {
		t.Fatalf("refresh -> %d (want %d): %s", status, want, text)
	}
	var out refreshOut
	_ = json.Unmarshal([]byte(text), &out)
	return out
}

type device struct {
	Device struct {
		ID       string `json:"id"`
		Platform string `json:"platform"`
	} `json:"device"`
	RefreshToken string `json:"refresh_token"`
}

func TestDeviceSessionsRefreshAndRevocation(t *testing.T) {
	sqlDB := testDB(t)
	ts, cleanup := bootTestServer(t)
	defer cleanup()

	email := uniq("phone")
	c := newClient(t, ts.URL)
	c.register(email)

	var d device
	c.mustJSON("POST", "/api/me/devices", map[string]any{"name": "Pixel", "platform": "android", "app": "mobile", "locale": "en"}, 201, &d)
	if d.Device.ID == "" || len(d.RefreshToken) < 20 {
		t.Fatalf("device = %+v", d)
	}
	c.expect("POST", "/api/me/devices", map[string]any{"name": "Toaster", "platform": "tizen", "app": "mobile"}, 422)

	// Refresh: a working Bearer session and a rotated refresh token.
	r1 := refresh(t, ts.URL, d.RefreshToken, 200)
	if r1.DeviceID != d.Device.ID || r1.RefreshToken == d.RefreshToken {
		t.Fatalf("refresh = %+v", r1)
	}
	if status, text := bearer(t, ts.URL, r1.Token, "GET", "/api/me", nil); status != 200 {
		t.Fatalf("GET /api/me with the refreshed session -> %d: %s", status, text)
	}
	// The old refresh token is spent.
	refresh(t, ts.URL, d.RefreshToken, 401)
	refresh(t, ts.URL, "hbr_not-a-real-token-at-all-0000000000000", 401)

	// Push token and the test send: push is not configured in tests, so 503 (but only for my own device).
	if status, text := bearer(t, ts.URL, r1.Token, "PUT", "/api/me/devices/"+d.Device.ID+"/push", map[string]any{"token": "fcm-token-1"}); status != 204 {
		t.Fatalf("set push token -> %d: %s", status, text)
	}
	if status, _ := bearer(t, ts.URL, r1.Token, "POST", "/api/me/devices/"+d.Device.ID+"/test", nil); status != 503 {
		t.Fatalf("test push without FCM -> %d, want 503", status)
	}
	if status, _ := bearer(t, ts.URL, r1.Token, "POST", "/api/me/devices/00000000-0000-4000-8000-000000000000/test", nil); status != 404 {
		t.Fatalf("test push to an unknown device -> %d, want 404", status)
	}

	// Prefs: everything on by default; a partial update mutes one event; unknown events refused.
	var prefs struct {
		Events        map[string]bool `json:"events"`
		PushAvailable bool            `json:"push_available"`
	}
	c.mustJSON("GET", "/api/me/notification-prefs", nil, 200, &prefs)
	if len(prefs.Events) != len(push.Events) || !prefs.Events[push.EventDeploySucceeded] || prefs.PushAvailable {
		t.Fatalf("default prefs = %+v", prefs)
	}
	c.mustJSON("PUT", "/api/me/notification-prefs", map[string]any{"events": map[string]bool{push.EventDeploySucceeded: false}}, 200, &prefs)
	c.mustJSON("PUT", "/api/me/notification-prefs", map[string]any{"events": map[string]bool{push.EventAppDown: true}}, 200, &prefs)
	if prefs.Events[push.EventDeploySucceeded] || !prefs.Events[push.EventAppDown] {
		t.Fatalf("prefs after updates = %+v", prefs.Events)
	}
	c.expect("PUT", "/api/me/notification-prefs", map[string]any{"events": map[string]bool{"party.started": true}}, 422)

	// Fan-out: a team event reaches my device unless I muted it.
	var team struct{ ID string }
	c.mustJSON("POST", "/api/teams", map[string]any{"name": "Phone team"}, 201, &team)
	fake := &recordingSender{}
	ps := push.New(sqlDB, fake, nil)
	n, err := ps.Deliver(context.Background(), push.Event{TeamID: team.ID, Kind: push.EventDeployFailed, Title: "shop failed", TitleAR: "فشل"})
	if err != nil || n != 1 || fake.sent() != "fcm-token-1:shop failed" {
		t.Fatalf("deliver deploy.failed = %d, %v, %q", n, err, fake.sent())
	}
	if n, _ := ps.Deliver(context.Background(), push.Event{TeamID: team.ID, Kind: push.EventDeploySucceeded, Title: "shipped"}); n != 0 {
		t.Fatalf("a muted event reached %d devices", n)
	}
	// FCM says the token is dead: it is forgotten.
	fake.gone = true
	if n, _ := ps.Deliver(context.Background(), push.Event{TeamID: team.ID, Kind: push.EventAppDown, Title: "down"}); n != 0 {
		t.Fatalf("a dead token counted as delivered")
	}
	fake.gone = false
	if n, _ := ps.Deliver(context.Background(), push.Event{TeamID: team.ID, Kind: push.EventAppDown, Title: "down"}); n != 0 {
		t.Fatalf("a dead token was used again")
	}

	// Another user cannot touch my device.
	o := newClient(t, ts.URL)
	o.register(uniq("other-phone"))
	o.expect("DELETE", "/api/me/devices/"+d.Device.ID, nil, 404)
	o.expect("PUT", "/api/me/devices/"+d.Device.ID+"/push", map[string]any{"token": "stolen"}, 404)

	// "Sign out everywhere" (sessions revoked after the device signed in) ends the device too.
	r2 := refresh(t, ts.URL, r1.RefreshToken, 200)
	if _, err := sqlDB.Exec(`UPDATE users SET sessions_revoked_at = now() WHERE email = $1`, email); err != nil {
		t.Fatal(err)
	}
	if out := refresh(t, ts.URL, r2.RefreshToken, 401); out.Code != "session_revoked" {
		t.Fatalf("revoked refresh code = %q", out.Code)
	}
	refresh(t, ts.URL, r2.RefreshToken, 401) // and it is gone

	// A device signed in after the revocation works; signing it out kills its refresh token. (Session
	// times have one-second resolution: a sign-in in the same second as the revocation counts as revoked.)
	time.Sleep(1100 * time.Millisecond)
	c2 := newClient(t, ts.URL)
	c2.login(email)
	var d2 device
	c2.mustJSON("POST", "/api/me/devices", map[string]any{"name": "Laptop", "platform": "windows", "app": "desktop"}, 201, &d2)
	var list struct {
		Devices []struct{ ID string } `json:"devices"`
	}
	c2.mustJSON("GET", "/api/me/devices", nil, 200, &list)
	if len(list.Devices) != 1 || list.Devices[0].ID != d2.Device.ID {
		t.Fatalf("devices = %+v", list)
	}
	c2.expect("DELETE", "/api/me/devices/"+d2.Device.ID, nil, 204)
	refresh(t, ts.URL, d2.RefreshToken, 401)

	// A disabled account's device is refused (403, like every other session path).
	var d3 device
	c2.mustJSON("POST", "/api/me/devices", map[string]any{"name": "Mac", "platform": "macos", "app": "desktop"}, 201, &d3)
	if _, err := sqlDB.Exec(`UPDATE users SET disabled_at = now() WHERE email = $1`, email); err != nil {
		t.Fatal(err)
	}
	if out := refresh(t, ts.URL, d3.RefreshToken, 403); out.Code != "account_disabled" {
		t.Fatalf("disabled refresh code = %q", out.Code)
	}
}

type recordingSender struct {
	mu   sync.Mutex
	gone bool
	log  []string
}

func (r *recordingSender) Send(_ context.Context, token string, m push.Message) (push.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gone {
		return push.SentGone, nil
	}
	r.log = append(r.log, token+":"+m.Title)
	return push.SentOK, nil
}

func (r *recordingSender) sent() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.log) == 0 {
		return ""
	}
	return r.log[len(r.log)-1]
}
