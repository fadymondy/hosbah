package server

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// raw sends a body as it is, with the given Content-Type.
func (c *client) raw(method, path, contentType string, body []byte) (*http.Response, []byte) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-CSRF-Token", c.csrf())
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// A server's logo is an uploaded PNG, JPEG or WebP: checked, served safely, and gone with the server.
func TestServerLogo(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	ua, ub, uv := newClient(t, ts.URL), newClient(t, ts.URL), newClient(t, ts.URL)
	ua.register("logo-a-" + suffix + "@example.test")
	ub.register("logo-b-" + suffix + "@example.test")
	viewerEmail := "logo-v-" + suffix + "@example.test"
	uv.register(viewerEmail)
	var teamA, teamB struct{ ID string }
	ua.mustJSON("POST", "/api/teams", map[string]any{"name": "LogoA " + suffix}, 201, &teamA)
	ub.mustJSON("POST", "/api/teams", map[string]any{"name": "LogoB " + suffix}, 201, &teamB)
	srv, _ := enrolServer(t, ua, ts.URL, teamA.ID, "logo-"+short)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	uv.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	base := "/api/teams/" + teamA.ID + "/servers/" + srv
	logo := base + "/logo"
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 200)...)
	jpeg := append([]byte("\xff\xd8\xff\xe0"), bytes.Repeat([]byte{9}, 100)...)

	if resp, _ := ua.raw("GET", logo, "", nil); resp.StatusCode != 404 {
		t.Fatalf("GET without a logo -> %d (want 404)", resp.StatusCode)
	}
	ua.mustJSON("PATCH", base, map[string]any{"logo_url": "https://example.test/ext.png"}, 200, nil)

	// Refusals.
	for name, c := range map[string]struct {
		ct   string
		body []byte
		want int
	}{
		"svg":         {"image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 415},
		"text":        {"text/plain", []byte("hello"), 415},
		"json":        {"application/json", []byte(`{}`), 415},
		"svg as png":  {"image/png", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 422},
		"jpeg as png": {"image/png", jpeg, 422},
		"png as webp": {"image/webp", png, 422},
		"empty":       {"image/png", nil, 422},
		"oversize":    {"image/png", append(append([]byte{}, png...), make([]byte, 512<<10)...), 413},
	} {
		if resp, b := ua.raw("PUT", logo, c.ct, c.body); resp.StatusCode != c.want {
			t.Errorf("PUT %s -> %d (want %d): %s", name, resp.StatusCode, c.want, b)
		}
	}
	if resp, _ := uv.raw("PUT", logo, "image/png", png); resp.StatusCode != 403 {
		t.Errorf("viewer PUT -> %d (want 403)", resp.StatusCode)
	}
	if resp, _ := ub.raw("PUT", logo, "image/png", png); resp.StatusCode != 404 {
		t.Errorf("team B PUT -> %d (want 404)", resp.StatusCode)
	}
	if resp, _ := ub.raw("PUT", "/api/teams/"+teamB.ID+"/servers/"+srv+"/logo", "image/png", png); resp.StatusCode != 404 {
		t.Errorf("team B PUT through its own team -> %d (want 404)", resp.StatusCode)
	}
	var out map[string]any
	ua.mustJSON("GET", base, nil, 200, &out)
	if out["logo_url"] != "https://example.test/ext.png" {
		t.Fatalf("refused uploads changed logo_url: %v", out["logo_url"])
	}

	// Upload.
	resp, body := ua.raw("PUT", logo, "image/png", png)
	if resp.StatusCode != 200 {
		t.Fatalf("PUT png -> %d: %s", resp.StatusCode, body)
	}
	ua.mustJSON("GET", base, nil, 200, &out)
	url, _ := out["logo_url"].(string)
	if !strings.HasPrefix(url, logo+"?v=") || !strings.Contains(string(body), url) {
		t.Fatalf("logo_url after upload: %q (PUT answered %s)", url, body)
	}
	var list struct {
		Servers []struct {
			ID      string
			LogoURL string `json:"logo_url"`
		} `json:"servers"`
	}
	ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/servers", nil, 200, &list)
	if len(list.Servers) != 1 || list.Servers[0].LogoURL != url {
		t.Fatalf("list logo_url: %+v (want %s)", list, url)
	}

	// Served safely, to any member, not to another team.
	resp, got := uv.raw("GET", url, "", nil)
	if resp.StatusCode != 200 || !bytes.Equal(got, png) {
		t.Fatalf("viewer GET -> %d, %d bytes", resp.StatusCode, len(got))
	}
	for h, want := range map[string]string{"Content-Type": "image/png", "X-Content-Type-Options": "nosniff",
		"Content-Security-Policy": "default-src 'none'; sandbox", "Cache-Control": "private, max-age=31536000, immutable"} {
		if resp.Header.Get(h) != want {
			t.Errorf("GET %s: %q (want %q)", h, resp.Header.Get(h), want)
		}
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Error("GET has no ETag")
	}
	req, _ := http.NewRequest("GET", ts.URL+url, nil)
	req.Header.Set("If-None-Match", etag)
	if r2, err := ua.http.Do(req); err != nil || r2.StatusCode != 304 {
		t.Errorf("If-None-Match -> %v %v (want 304)", r2, err)
	}
	if resp, _ := ub.raw("GET", url, "", nil); resp.StatusCode != 404 {
		t.Errorf("team B GET -> %d (want 404)", resp.StatusCode)
	}

	// Replace: a new version.
	if resp, b := ua.raw("PUT", logo, "image/jpeg", jpeg); resp.StatusCode != 200 {
		t.Fatalf("PUT jpeg -> %d: %s", resp.StatusCode, b)
	}
	ua.mustJSON("GET", base, nil, 200, &out)
	if out["logo_url"] == url {
		t.Errorf("a replaced logo kept its version: %v", out["logo_url"])
	}
	if resp, got := ua.raw("GET", logo, "", nil); resp.Header.Get("Content-Type") != "image/jpeg" || !bytes.Equal(got, jpeg) {
		t.Errorf("after replacing: %s, %d bytes", resp.Header.Get("Content-Type"), len(got))
	}

	// Remove: owner or admin; back to the external URL.
	if resp, _ := uv.raw("DELETE", logo, "", nil); resp.StatusCode != 403 {
		t.Errorf("viewer DELETE -> %d (want 403)", resp.StatusCode)
	}
	if resp, _ := ub.raw("DELETE", logo, "", nil); resp.StatusCode != 404 {
		t.Errorf("team B DELETE -> %d (want 404)", resp.StatusCode)
	}
	ua.mustJSON("DELETE", logo, nil, 200, &out)
	if out["logo_url"] != "https://example.test/ext.png" {
		t.Errorf("logo_url after DELETE: %v", out["logo_url"])
	}
	if resp, _ := ua.raw("GET", logo, "", nil); resp.StatusCode != 404 {
		t.Errorf("GET after DELETE -> %d (want 404)", resp.StatusCode)
	}
	var audit struct {
		Entries []struct{ Action string } `json:"entries"`
	}
	ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/audit", nil, 200, &audit)
	seen := map[string]int{}
	for _, e := range audit.Entries {
		seen[e.Action]++
	}
	if seen["server.logo_updated"] != 2 || seen["server.logo_removed"] != 1 {
		t.Errorf("audit: %v", seen)
	}

	// Deleting the server takes the logo with it.
	if resp, b := ua.raw("PUT", logo, "image/png", png); resp.StatusCode != 200 {
		t.Fatalf("PUT again -> %d: %s", resp.StatusCode, b)
	}
	count := func() (n int) {
		if err := tenancy.Tx(context.Background(), sqlDB, tenancy.Scope{TeamID: teamA.ID}, func(_ *db.Queries, tx *sql.Tx) error {
			return tx.QueryRow(`SELECT count(*) FROM server_logos WHERE server_id = $1`, srv).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	if count() != 1 {
		t.Fatal("the logo row is missing")
	}
	ua.mustJSON("DELETE", base, nil, 204, nil)
	if count() != 0 {
		t.Error("the logo outlived its server")
	}
}
