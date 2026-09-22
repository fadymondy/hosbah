package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/cloudflare"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// fakeCF is an in-memory Cloudflare for one account: zones, and records by id. Reading the
// records of a name that starts with "broken." fails, as a Cloudflare hiccup would.
type fakeCF struct {
	mu      sync.Mutex
	zones   []cloudflare.Zone
	records map[string]cloudflare.Record
	next    int
	writes  int
}

func (f *fakeCF) put(r cloudflare.Record) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.ID == "" {
		f.next++
		r.ID = fmt.Sprintf("seed%d", f.next)
	}
	f.records[r.ID] = r
}

func (f *fakeCF) at(name string) []cloudflare.Record {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cloudflare.Record
	for _, r := range f.records {
		if r.Name == name {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeCF) drop(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, r := range f.records {
		if r.Name == name {
			delete(f.records, id)
		}
	}
}

func (f *fakeCF) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func (f *fakeCF) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ok := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, `{"success":true,"errors":[],"result":%s}`, b)
	}
	fail := func(status int, msg string) {
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"success":false,"errors":[{"code":1000,"message":%q}]}`, msg)
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.URL.Path == "/zones":
		out := []cloudflare.Zone{}
		for _, z := range f.zones {
			if n := r.URL.Query().Get("name"); n == "" || n == z.Name {
				out = append(out, z)
			}
		}
		ok(out)
	case len(parts) >= 3 && parts[0] == "zones" && parts[2] == "dns_records":
		switch r.Method {
		case http.MethodGet:
			name := r.URL.Query().Get("name")
			if strings.HasPrefix(name, "broken.") {
				fail(500, "Cloudflare is having a moment")
				return
			}
			out := []cloudflare.Record{}
			for _, rec := range f.records {
				if rec.Name == name && (r.URL.Query().Get("type") == "" || rec.Type == r.URL.Query().Get("type")) {
					out = append(out, rec)
				}
			}
			ok(out)
		case http.MethodPost:
			var rec cloudflare.Record
			_ = json.NewDecoder(r.Body).Decode(&rec)
			f.next++
			f.writes++
			rec.ID = fmt.Sprintf("r%d", f.next)
			f.records[rec.ID] = rec
			ok(rec)
		case http.MethodPut:
			var rec cloudflare.Record
			_ = json.NewDecoder(r.Body).Decode(&rec)
			rec.ID = parts[3]
			f.writes++
			f.records[rec.ID] = rec
			ok(rec)
		case http.MethodPatch:
			rec, found := f.records[parts[3]]
			if !found {
				fail(404, "Record does not exist.")
				return
			}
			var p cloudflare.RecordPatch
			_ = json.NewDecoder(r.Body).Decode(&p)
			if p.Content != nil {
				rec.Content = *p.Content
			}
			if p.Proxied != nil {
				rec.Proxied = p.Proxied
			}
			if p.Comment != nil {
				rec.Comment = *p.Comment
			}
			f.writes++
			f.records[rec.ID] = rec
			ok(rec)
		case http.MethodDelete:
			f.writes++
			delete(f.records, parts[3])
			ok(map[string]string{"id": parts[3]})
		}
	default:
		fail(404, "no route")
	}
}

/*
The "DNS & SSL" section of a site: each hostname of an app against the team's Cloudflare (every
status), the takeover and managed-only rules, hostnames that are not the app's, roles, other teams,
imported apps, and the certificates read through a typed, validated caddy.certs on a fake agent.
*/
func TestAppDNSAndCertificates(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	zone := "dns-" + suffix + ".test"
	const publicIP = "203.0.113.10"

	cf := &fakeCF{records: map[string]cloudflare.Record{}, zones: []cloudflare.Zone{{ID: "zone1", Name: zone, Status: "active"}}}
	cfSrv := httptest.NewServer(http.HandlerFunc(cf.serve))
	defer cfSrv.Close()
	lastDeploy.SetCloudflareOptions(cloudflare.WithBaseURL(cfSrv.URL))

	owner := newClient(t, ts.URL)
	owner.register("dns-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "DNS " + suffix}, 201, &team)
	var readTok, writeTok struct{ Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "dns-read", "scopes": []string{"read"}, "expires_in_days": 30}, 201, &readTok)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "dns-write", "scopes": []string{"read", "write"}, "expires_in_days": 30}, 201, &writeTok)

	viewerEmail := "dns-viewer-" + suffix + "@example.test"
	viewer := newClient(t, ts.URL)
	viewer.register(viewerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	stranger := newClient(t, ts.URL)
	stranger.register("dns-stranger-" + suffix + "@example.test")
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "Elsewhere " + suffix}, 201, nil)

	var cred struct{ ID, Name string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/credentials", map[string]any{"provider": "cloudflare", "name": "cf-" + short, "secret": "cf-token", "config": map[string]string{},
		"skip_validation": true}, 201, &cred)

	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "dns-pub-"+short, publicIP, []agentproto.Service{
		{Kind: "container", Name: "imp-dns-" + short, Image: "nginx:1.27", Status: "running", Ports: []string{"0.0.0.0:8081->80/tcp"}}})
	privSrv, _ := enrolWithServicesKey(t, owner, ts.URL, team.ID, "dns-lan-"+short, "10.9.8.5", nil)

	notAfter := time.Now().Add(60*24*time.Hour + time.Hour).UTC()
	notBefore := notAfter.Add(-90 * 24 * time.Hour)
	agent := startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		if op.Kind != agentproto.KindCaddyCerts {
			return map[string]any{"ok": true}, ""
		}
		var p agentproto.CaddyCertsParams
		if json.Unmarshal(op.Params, &p) != nil || p.Validate() != nil {
			return nil, "bad params"
		}
		res := agentproto.CaddyCertsResult{}
		for _, h := range p.Hostnames {
			c := agentproto.CaddyCert{Hostname: h}
			switch {
			case strings.HasPrefix(h, "ok."):
				c.Found, c.Issuer, c.NotBefore, c.NotAfter, c.Managed = true, "Let's Encrypt R11", &notBefore, &notAfter, true
			case strings.HasPrefix(h, "keep."):
				c.Issuer, c.Managed = "a stale issuer of a certificate that was not found", true // must be dropped
			}
			res.Certificates = append(res.Certificates, c)
		}
		// An agent is not trusted to name hostnames nobody asked about.
		res.Certificates = append(res.Certificates, agentproto.CaddyCert{Hostname: "evil.example.com", Found: true, Issuer: "x"})
		return res, ""
	})

	var app, otherApp, lanApp struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "dns-" + short, "server": srv, "repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "dns2-" + short, "server": srv, "repo_url": "https://github.com/octocat/api.git"}, 201, &otherApp)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "lan-" + short, "server": privSrv, "repo_url": "https://github.com/octocat/api.git"}, 201, &lanApp)
	appBase := func(id string) string { return "/api/teams/" + team.ID + "/apps/" + id }
	base := appBase(app.App.ID)

	host := func(label string) string { return label + "." + zone }
	elsewhere := "site-" + suffix + ".elsewhere.test"
	for _, h := range []string{host("ok"), host("missing"), host("foreign"), host("cname"), host("keep"), host("drift"), host("broken"), elsewhere} {
		owner.mustJSON("POST", base+"/domains", map[string]any{"hostname": h, "tls": "off"}, 201, nil)
	}
	owner.mustJSON("POST", appBase(otherApp.App.ID)+"/domains", map[string]any{"hostname": host("sibling"), "tls": "off"}, 201, nil)
	owner.mustJSON("POST", appBase(lanApp.App.ID)+"/domains", map[string]any{"hostname": host("lan"), "tls": "off"}, 201, nil)

	// Adding a domain in a zone the team's Cloudflare holds already created its record (teamdns).
	if recs := cf.at(host("ok")); len(recs) != 1 || recs[0].Content != publicIP || !cloudflare.Managed(recs[0]) {
		t.Fatalf("adding a domain should have created a managed record: %+v", recs)
	}
	if recs := cf.at(host("lan")); len(recs) != 0 {
		t.Fatalf("a private server gets no public record: %+v", recs)
	}
	// Arrange the rest of the zone: no record; someone else's A record elsewhere; someone else's
	// CNAME; someone else's record that already points here; Hosbah's own record gone stale; and a
	// record of the zone that is no hostname of this app.
	cf.drop(host("missing"))
	cf.drop(host("foreign"))
	cf.put(cloudflare.Record{Type: "A", Name: host("foreign"), Content: "198.51.100.7", TTL: 300, Comment: "the marketing site"})
	cf.drop(host("cname"))
	cf.put(cloudflare.Record{Type: "CNAME", Name: host("cname"), Content: "pages.example.net", TTL: 1})
	cf.drop(host("keep"))
	cf.put(cloudflare.Record{Type: "A", Name: host("keep"), Content: publicIP, TTL: 1})
	cf.drop(host("drift"))
	cf.put(cloudflare.Record{Type: "A", Name: host("drift"), Content: "198.51.100.99", TTL: 1, Comment: "managed by Cloudy"})
	cf.put(cloudflare.Record{ID: "mx-apex", Type: "A", Name: host("mail"), Content: "198.51.100.25", TTL: 1, Comment: cloudflare.ManagedComment})

	type rowOut struct {
		Hostname   string  `json:"hostname"`
		Zone       *string `json:"zone"`
		Credential *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"credential"`
		Managed bool `json:"managed"`
		Record  *struct {
			ID              string `json:"id"`
			Type            string `json:"type"`
			Content         string `json:"content"`
			Proxied         bool   `json:"proxied"`
			TTL             int    `json:"ttl"`
			ManagedByHosbah bool   `json:"managed_by_hosbah"`
		} `json:"record"`
		Expected struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		} `json:"expected"`
		Status string  `json:"status"`
		Error  *string `json:"error"`
	}
	type dnsOut struct {
		Server struct {
			ID      string  `json:"id"`
			Name    string  `json:"name"`
			Address *string `json:"address"`
		} `json:"server"`
		Records []rowOut `json:"records"`
	}
	rowsOf := func(c *client, path string) (dnsOut, map[string]rowOut) {
		t.Helper()
		var out dnsOut
		c.mustJSON("GET", path+"/dns", nil, 200, &out)
		by := map[string]rowOut{}
		for _, r := range out.Records {
			by[r.Hostname] = r
		}
		return out, by
	}
	esc := url.PathEscape

	t.Run("every status, one row per hostname, and a Cloudflare failure stays in its row", func(t *testing.T) {
		out, by := rowsOf(viewer, base)
		if out.Server.ID != srv || out.Server.Address == nil || *out.Server.Address != publicIP || len(out.Records) != 8 {
			t.Fatalf("dns: %+v", out)
		}
		for h, want := range map[string]string{host("ok"): "ok", host("missing"): "missing", host("foreign"): "mismatch", host("cname"): "mismatch",
			host("keep"): "ok", host("drift"): "mismatch", elsewhere: "unmanaged"} {
			r := by[h]
			if r.Status != want || r.Error != nil || r.Expected.Type != "A" || r.Expected.Content != publicIP {
				t.Errorf("%s: status %q (want %q) error %v expected %+v", h, r.Status, want, r.Error, r.Expected)
			}
			if inZone := h != elsewhere; r.Managed != inZone || (r.Zone != nil) != inZone || (r.Credential != nil) != inZone {
				t.Errorf("%s: managed=%v zone=%v credential=%v", h, r.Managed, r.Zone, r.Credential)
			}
		}
		if r := by[host("ok")]; r.Record == nil || r.Record.Type != "A" || r.Record.Content != publicIP || !r.Record.ManagedByHosbah || r.Record.Proxied ||
			*r.Zone != zone || r.Credential.ID != cred.ID || r.Credential.Name != cred.Name {
			t.Errorf("ok row: %+v %+v", r, r.Record)
		}
		if r := by[host("missing")]; r.Record != nil {
			t.Errorf("missing row has a record: %+v", r.Record)
		}
		if r := by[host("foreign")]; r.Record == nil || r.Record.Content != "198.51.100.7" || r.Record.ManagedByHosbah || r.Record.TTL != 300 {
			t.Errorf("foreign row: %+v", r.Record)
		}
		if r := by[host("cname")]; r.Record == nil || r.Record.Type != "CNAME" || r.Record.ManagedByHosbah {
			t.Errorf("cname row: %+v", r.Record)
		}
		if r := by[host("keep")]; r.Record == nil || r.Record.ManagedByHosbah {
			t.Errorf("a matching record of someone else's is ok but not Hosbah's: %+v", r.Record)
		}
		if r := by[host("drift")]; r.Record == nil || !r.Record.ManagedByHosbah {
			t.Errorf("the legacy marker is Hosbah's too: %+v", r.Record)
		}
		if r := by[elsewhere]; r.Record != nil {
			t.Errorf("unmanaged row has a record: %+v", r.Record)
		}
		if r := by[host("broken")]; r.Error == nil || !strings.Contains(*r.Error, "having a moment") || r.Status == "missing" || r.Status == "ok" || !r.Managed {
			t.Errorf("broken row: %+v", r)
		}
		if _, text := viewer.do("GET", base+"/dns", nil); strings.Contains(text, "cf-token") {
			t.Error("the DNS list carries the Cloudflare token")
		}
		// By name too.
		owner.mustJSON("GET", appBase(app.App.Name)+"/dns", nil, 200, nil)
	})

	t.Run("a server with only a private address", func(t *testing.T) {
		out, by := rowsOf(viewer, appBase(lanApp.App.ID))
		r := by[host("lan")]
		if out.Server.ID != privSrv || r.Status != "private" || r.Expected.Content != "" || !r.Managed || r.Record != nil {
			t.Errorf("private: %+v", r)
		}
		before := cf.writeCount()
		if status, text := owner.do("PUT", appBase(lanApp.App.ID)+"/dns/"+esc(host("lan")), map[string]any{}); status != 409 || !strings.Contains(text, "private") {
			t.Errorf("PUT for a private server -> %d %s", status, text)
		}
		if cf.writeCount() != before {
			t.Error("a refused PUT wrote to Cloudflare")
		}
	})

	t.Run("who may write, and which hostnames", func(t *testing.T) {
		before := cf.writeCount()
		viewer.expect("PUT", base+"/dns/"+esc(host("missing")), map[string]any{}, 403)
		viewer.expect("DELETE", base+"/dns/"+esc(host("ok")), nil, 403)
		stranger.expect("GET", base+"/dns", nil, 404)
		stranger.expect("GET", base+"/certificates", nil, 404)
		stranger.expect("PUT", base+"/dns/"+esc(host("missing")), map[string]any{"takeover": true}, 404)
		stranger.expect("DELETE", base+"/dns/"+esc(host("ok")), nil, 404)
		owner.expect("GET", appBase("no-such-app")+"/dns", nil, 404)
		// Not this app's hostnames: a managed record of the zone, a sibling app's domain, the zone
		// apex, and things that are not hostnames at all.
		for _, h := range []string{host("mail"), host("sibling"), zone, "nope", "a b." + zone, "*." + zone, "_cloudy-verify." + host("ok"), "_hosbah-verify." + host("ok")} {
			owner.expect("PUT", base+"/dns/"+esc(h), map[string]any{"takeover": true}, 404)
			owner.expect("DELETE", base+"/dns/"+esc(h), nil, 404)
		}
		if cf.writeCount() != before {
			t.Errorf("refused calls wrote to Cloudflare %d time(s)", cf.writeCount()-before)
		}
		if recs := cf.at(host("mail")); len(recs) != 1 {
			t.Errorf("a record that is not the app's was touched: %+v", recs)
		}
		if status, text := owner.do("PUT", base+"/dns/"+esc(elsewhere), map[string]any{}); status != 422 || !strings.Contains(text, "Cloudflare") {
			t.Errorf("PUT for a zone the team does not hold -> %d %s", status, text)
		}
		if status, text := owner.do("PUT", base+"/dns/"+esc(host("broken")), map[string]any{}); status != 502 {
			t.Errorf("PUT when Cloudflare fails -> %d %s", status, text)
		}
	})

	t.Run("create, repair, proxy", func(t *testing.T) {
		var r rowOut
		owner.mustJSON("PUT", base+"/dns/"+esc(host("missing")), map[string]any{"proxied": true}, 200, &r)
		if r.Status != "ok" || r.Record == nil || r.Record.Content != publicIP || !r.Record.Proxied || !r.Record.ManagedByHosbah {
			t.Errorf("created: %+v %+v", r, r.Record)
		}
		if recs := cf.at(host("missing")); len(recs) != 1 || recs[0].Type != "A" || recs[0].Content != publicIP || !cloudflare.Managed(recs[0]) {
			t.Errorf("Cloudflare after create: %+v", recs)
		}
		// Hosbah's own stale record is repaired without takeover, keeping its proxy setting.
		owner.mustJSON("PUT", base+"/dns/"+esc(strings.ToUpper(host("drift"))+"."), map[string]any{}, 200, &r)
		if r.Status != "ok" || r.Record.Content != publicIP || !r.Record.ManagedByHosbah || r.Record.Proxied {
			t.Errorf("repaired: %+v %+v", r, r.Record)
		}
		if recs := cf.at(host("drift")); len(recs) != 1 || recs[0].Comment != cloudflare.ManagedComment {
			t.Errorf("a repaired record carries today's marker: %+v", recs)
		}
		// Proxying someone else's record that already points here does not make it Hosbah's.
		owner.mustJSON("PUT", base+"/dns/"+esc(host("keep")), map[string]any{"proxied": true}, 200, &r)
		if r.Status != "ok" || !r.Record.Proxied || r.Record.ManagedByHosbah {
			t.Errorf("proxied someone else's record: %+v %+v", r, r.Record)
		}
		// Nothing to do is not a write.
		before := cf.writeCount()
		owner.mustJSON("PUT", base+"/dns/"+esc(host("ok")), map[string]any{}, 200, &r)
		if r.Status != "ok" || cf.writeCount() != before {
			t.Errorf("an unchanged record was written: %+v", r)
		}
	})

	t.Run("takeover", func(t *testing.T) {
		before := cf.writeCount()
		for _, h := range []string{host("foreign"), host("cname")} {
			for _, body := range []map[string]any{{}, {"proxied": true}, {"takeover": false}} {
				if status, text := owner.do("PUT", base+"/dns/"+esc(h), body); status != 409 || !strings.Contains(text, "takeover") {
					t.Errorf("PUT %s %v without takeover -> %d %s", h, body, status, text)
				}
			}
		}
		if r := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "set_app_dns_record", map[string]any{"app": app.App.ID, "hostname": host("foreign")}); !r.IsError || !strings.Contains(r.Text, "takeover") {
			t.Errorf("set_app_dns_record without takeover: %+v", r)
		}
		if cf.writeCount() != before || cf.at(host("foreign"))[0].Content != "198.51.100.7" || cf.at(host("cname"))[0].Type != "CNAME" {
			t.Fatalf("a refused takeover changed Cloudflare: %+v %+v", cf.at(host("foreign")), cf.at(host("cname")))
		}
		var r rowOut
		owner.mustJSON("PUT", base+"/dns/"+esc(host("foreign")), map[string]any{"takeover": true}, 200, &r)
		if r.Status != "ok" || r.Record.Content != publicIP || !r.Record.ManagedByHosbah {
			t.Errorf("taken over: %+v %+v", r, r.Record)
		}
		owner.mustJSON("PUT", base+"/dns/"+esc(host("cname")), map[string]any{"takeover": true}, 200, &r)
		if recs := cf.at(host("cname")); r.Status != "ok" || r.Record.Type != "A" || len(recs) != 1 || recs[0].Type != "A" || recs[0].Content != publicIP || !cloudflare.Managed(recs[0]) {
			t.Errorf("a CNAME is replaced by the A record: %+v %+v", r.Record, recs)
		}
	})

	t.Run("delete only what Hosbah created", func(t *testing.T) {
		if status, text := owner.do("DELETE", base+"/dns/"+esc(host("keep")), nil); status != 409 || !strings.Contains(text, "did not create") {
			t.Errorf("DELETE of someone else's record -> %d %s", status, text)
		}
		if len(cf.at(host("keep"))) != 1 {
			t.Fatal("someone else's record was deleted")
		}
		owner.mustJSON("DELETE", base+"/dns/"+esc(host("ok")), nil, 204, nil)
		if len(cf.at(host("ok"))) != 0 {
			t.Error("the managed record is still there")
		}
		owner.expect("DELETE", base+"/dns/"+esc(host("ok")), nil, 404)
		_, by := rowsOf(owner, base)
		if by[host("ok")].Status != "missing" {
			t.Errorf("after the delete: %+v", by[host("ok")])
		}
	})

	t.Run("audited", func(t *testing.T) {
		var out struct {
			Entries []struct {
				Action string          `json:"action"`
			} `json:"entries"`
		}
		_, text := owner.do("GET", "/api/teams/"+team.ID+"/audit?limit=500", nil)
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/audit?limit=500", nil, 200, &out)
		set, removed := 0, 0
		for _, e := range out.Entries {
			switch e.Action {
			case "dns.record_set":
				set++
			case "dns.record_removed":
				removed++
			}
		}
		if set < 5 || removed != 1 || !strings.Contains(text, host("foreign")) || !strings.Contains(text, "takeover") {
			t.Errorf("audit: %d set, %d removed", set, removed)
		}
	})

	var imp importedApp
	t.Run("an imported app", func(t *testing.T) {
		apps, _ := listImported(t, owner, team.ID, srv)
		imp = apps["imp-dns-"+short]
		if imp.ID == "" {
			t.Fatalf("imports: %v", keys(apps))
		}
		owner.mustJSON("POST", appBase(imp.ID)+"/domains", map[string]any{"hostname": host("imported"), "tls": "off"}, 201, nil)
		_, by := rowsOf(viewer, appBase(imp.ID))
		if r := by[host("imported")]; r.Status != "ok" || r.Record == nil || !r.Record.ManagedByHosbah {
			t.Fatalf("imported app's hostname: %+v", r)
		}
		var r rowOut
		owner.mustJSON("PUT", appBase(imp.ID)+"/dns/"+esc(host("imported")), map[string]any{"proxied": true}, 200, &r)
		if !r.Record.Proxied {
			t.Errorf("imported app's record: %+v", r.Record)
		}
		owner.expect("PUT", appBase(imp.ID)+"/dns/"+esc(host("keep")), map[string]any{}, 404) // the git app's, not this one's
		owner.mustJSON("DELETE", appBase(imp.ID)+"/dns/"+esc(host("imported")), nil, 204, nil)
	})

	type certOut struct {
		Hostname  string     `json:"hostname"`
		Found     bool       `json:"found"`
		Issuer    *string    `json:"issuer"`
		NotBefore *time.Time `json:"not_before"`
		NotAfter  *time.Time `json:"not_after"`
		DaysLeft  *int       `json:"days_left"`
		Managed   bool       `json:"managed"`
		Error     *string    `json:"error"`
	}
	type certsOut struct {
		Certificates []certOut `json:"certificates"`
		Operation    *string   `json:"operation"`
	}

	t.Run("certificates", func(t *testing.T) {
		var out certsOut
		viewer.mustJSON("GET", base+"/certificates", nil, 200, &out)
		if out.Operation == nil || len(out.Certificates) != 8 {
			t.Fatalf("certificates: %+v", out)
		}
		ops := agentOps(agent, agentproto.KindCaddyCerts)
		var sent agentproto.CaddyCertsParams
		if len(ops) != 1 || json.Unmarshal(ops[0].Params, &sent) != nil || len(sent.Hostnames) != 8 || sent.Validate() != nil {
			t.Fatalf("caddy.certs params: %+v", ops)
		}
		by := map[string]certOut{}
		for i, c := range out.Certificates {
			by[c.Hostname] = c
			if c.Hostname != sent.Hostnames[i] {
				t.Errorf("certificate %d is %s, asked %s", i, c.Hostname, sent.Hostnames[i])
			}
		}
		if c := by[host("ok")]; !c.Found || c.Issuer == nil || *c.Issuer != "Let's Encrypt R11" || !c.Managed || c.DaysLeft == nil || *c.DaysLeft != 60 ||
			c.NotAfter == nil || !c.NotAfter.Equal(notAfter.Truncate(time.Nanosecond)) || c.NotBefore == nil {
			t.Errorf("found certificate: %+v", c)
		}
		if c := by[host("keep")]; c.Found || c.Issuer != nil || c.Managed || c.DaysLeft != nil || c.NotAfter != nil {
			t.Errorf("a certificate that was not found carries nothing: %+v", c)
		}
		if _, leaked := by["evil.example.com"]; leaked {
			t.Error("a hostname nobody asked about was reported")
		}

		// An imported app's hostnames too; an app without hostnames asks nobody.
		owner.mustJSON("POST", appBase(imp.ID)+"/domains", map[string]any{"hostname": host("ok.imp"), "tls": "off"}, 201, nil)
		viewer.mustJSON("GET", appBase(imp.ID)+"/certificates", nil, 200, &out)
		if len(out.Certificates) != 2 || out.Certificates[0].Hostname != host("imported") || out.Certificates[1].Hostname != host("ok.imp") || !out.Certificates[1].Found {
			t.Errorf("imported app's certificates: %+v", out)
		}
		var bare struct {
			App struct{ ID string } `json:"app"`
		}
		owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "bare-" + short, "server": srv, "repo_url": "https://github.com/octocat/api.git"}, 201, &bare)
		before := len(agentOps(agent, agentproto.KindCaddyCerts))
		out = certsOut{}
		viewer.mustJSON("GET", appBase(bare.App.ID)+"/certificates", nil, 200, &out)
		if out.Operation != nil || len(out.Certificates) != 0 || len(agentOps(agent, agentproto.KindCaddyCerts)) != before {
			t.Errorf("an app without hostnames: %+v", out)
		}
	})

	t.Run("a hostname that is not one never reaches the agent", func(t *testing.T) {
		before := len(agentOps(agent, agentproto.KindCaddyCerts))
		many := make([]string, agentproto.MaxCertHostnames+1)
		for i := range many {
			many[i] = fmt.Sprintf("h%d.example.com", i)
		}
		err := tenancy.Tx(context.Background(), sqlDB, tenancy.Scope{System: true, TeamID: team.ID}, func(q *db.Queries, _ *sql.Tx) error {
			for _, bad := range [][]string{nil, {"../../etc/passwd"}, {host("ok"), "bad host"}, {"a/b.example.com"}, {"*.example.com"}, {"x.example.com:443"}, {"-x.example.com"}, many} {
				if _, err := lastDeploy.ServerCertificates(context.Background(), q, team.ID, deploy.Actor{Via: "hook"}, srv, bad); err == nil {
					t.Errorf("hostnames %.50v were queued", bad)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		if n := len(agentOps(agent, agentproto.KindCaddyCerts)); n != before {
			t.Errorf("refused hostnames reached the agent %d time(s)", n-before)
		}
		for _, bad := range []agentproto.CaddyCertsParams{{}, {Hostnames: []string{"UPPER.example.com"}}, {Hostnames: []string{"a..b"}}, {Hostnames: many}} {
			if bad.Validate() == nil {
				t.Errorf("params %.50v should not validate", bad.Hostnames)
			}
		}
	})

	t.Run("mcp", func(t *testing.T) {
		r := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "get_app_dns", map[string]any{"app": app.App.Name})
		if r.IsError || !strings.Contains(r.Text, host("keep")) || !strings.Contains(r.Text, "unmanaged") || !strings.Contains(r.Text, "not Hosbah's") ||
			!strings.Contains(r.Text, "having a moment") || strings.Contains(r.Text, "cf-token") {
			t.Errorf("get_app_dns: %+v", r)
		}
		r = mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "get_app_certificates", map[string]any{"app": app.App.ID})
		if r.IsError || !strings.Contains(r.Text, "Let's Encrypt R11") || !strings.Contains(r.Text, "60 days") || !strings.Contains(r.Text, "no certificate") ||
			strings.Contains(r.Text, "evil.example.com") {
			t.Errorf("get_app_certificates: %+v", r)
		}
		// A read token cannot write; a write token can.
		if r := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "set_app_dns_record", map[string]any{"app": app.App.ID, "hostname": host("ok")}); !r.IsError {
			t.Errorf("set_app_dns_record with a read token: %+v", r)
		}
		if len(cf.at(host("ok"))) != 0 {
			t.Fatal("a read token wrote a record")
		}
		r = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "set_app_dns_record", map[string]any{"app": app.App.ID, "hostname": host("ok"), "proxied": true})
		if recs := cf.at(host("ok")); r.IsError || !strings.Contains(r.Text, "ok") || len(recs) != 1 || recs[0].Proxied == nil || !*recs[0].Proxied {
			t.Errorf("set_app_dns_record: %+v %+v", r, recs)
		}
		if r := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "set_app_dns_record", map[string]any{"app": app.App.ID, "hostname": host("mail")}); !r.IsError || !strings.Contains(r.Text, "not found") {
			t.Errorf("set_app_dns_record for a hostname that is not the app's: %+v", r)
		}
	})

	t.Run("the server does not answer", func(t *testing.T) {
		seen := func(when string) {
			t.Helper()
			err := tenancy.Tx(context.Background(), sqlDB, tenancy.Scope{System: true, TeamID: team.ID}, func(_ *db.Queries, tx *sql.Tx) error {
				res, err := tx.Exec(`UPDATE servers SET last_seen_at = `+when+` WHERE id = $1`, privSrv)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n != 1 {
					return fmt.Errorf("updated %d servers", n)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("setting last_seen_at: %v", err)
			}
		}
		// A heartbeat a moment ago, yet nobody answers (its agent just dropped): 504 after the wait.
		seen("now()")
		viewer.http.Timeout = 45 * time.Second // the route waits 20 s for the agent
		if status, text := viewer.do("GET", appBase(lanApp.App.ID)+"/certificates", nil); status != 504 || !strings.Contains(text, "did not answer") {
			t.Errorf("certificates of a server that does not answer -> %d %s", status, text)
		}
		// No heartbeat for an hour: offline, refused at once.
		seen("now() - interval '1 hour'")
		if status, text := viewer.do("GET", appBase(lanApp.App.ID)+"/certificates", nil); status != 409 || !strings.Contains(text, "offline") {
			t.Errorf("certificates of an offline server -> %d %s", status, text)
		}
		if r := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "get_app_certificates", map[string]any{"app": lanApp.App.ID}); !r.IsError || !strings.Contains(r.Text, "offline") {
			t.Errorf("get_app_certificates of an offline server: %+v", r)
		}
	})

	if rej := agent.rejectedList(); len(rej) > 0 {
		t.Errorf("the agent refused operations: %v", rej)
	}
}
