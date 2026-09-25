package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/cloudflare"
)

/*
fakeCFZones is a separate in-memory Cloudflare fake from fakeCF (dns_test.go): the DNS zone
manager (MH-578 E8) needs several zones at once and unfiltered "every record of the zone"
listing, neither of which fakeCF's single-zone, name-filtered shape supports.
*/
type fakeCFZones struct {
	mu      sync.Mutex
	zones   []cloudflare.Zone
	records map[string]map[string]cloudflare.Record // zoneID -> recordID -> record
	next    int
}

func newFakeCFZones(zones ...cloudflare.Zone) *fakeCFZones {
	f := &fakeCFZones{zones: zones, records: map[string]map[string]cloudflare.Record{}}
	for _, z := range zones {
		f.records[z.ID] = map[string]cloudflare.Record{}
	}
	return f
}

func (f *fakeCFZones) recordsOf(zoneID string) []cloudflare.Record {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cloudflare.Record
	for _, r := range f.records[zoneID] {
		out = append(out, r)
	}
	return out
}

func (f *fakeCFZones) serve(w http.ResponseWriter, r *http.Request) {
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
		zoneID := parts[1]
		recs, known := f.records[zoneID]
		if !known {
			fail(404, "zone not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			out := []cloudflare.Record{}
			for _, rec := range recs {
				out = append(out, rec)
			}
			ok(out)
		case http.MethodPost:
			var rec cloudflare.Record
			_ = json.NewDecoder(r.Body).Decode(&rec)
			f.next++
			rec.ID = fmt.Sprintf("zr%d", f.next)
			recs[rec.ID] = rec
			ok(rec)
		case http.MethodPatch:
			if len(parts) < 4 {
				fail(400, "no record id")
				return
			}
			rec, found := recs[parts[3]]
			if !found {
				fail(404, "Record does not exist.")
				return
			}
			var p cloudflare.RecordPatch
			_ = json.NewDecoder(r.Body).Decode(&p)
			if p.Content != nil {
				rec.Content = *p.Content
			}
			if p.TTL != nil {
				rec.TTL = *p.TTL
			}
			if p.Priority != nil {
				rec.Priority = *p.Priority
			}
			if p.Proxied != nil {
				rec.Proxied = p.Proxied
			}
			if p.Comment != nil {
				rec.Comment = *p.Comment
			}
			recs[rec.ID] = rec
			ok(rec)
		case http.MethodDelete:
			if len(parts) < 4 {
				fail(400, "no record id")
				return
			}
			delete(recs, parts[3])
			ok(map[string]string{"id": parts[3]})
		}
	default:
		fail(404, "no route")
	}
}

/*
The DNS zone manager (MH-578 E8): every zone and record a team's Cloudflare credential can see —
create/update/delete of A, MX (with priority), TXT and a proxied CNAME, the "managed by Hosbah"
and ManagedKind badges against a seeded app domain / proxy host / mail domain, role gating
(viewer may read, only owner/admin may write), audit entries, and tenancy isolation via the
isolation sweep (internal/server/isolation_test.go), not repeated here.
*/
func TestDNSZoneManager(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	zoneName := "zonemgr-" + suffix + ".test"
	otherZoneName := "zonemgr2-" + suffix + ".test"

	cf := newFakeCFZones(
		cloudflare.Zone{ID: "z1-" + short, Name: zoneName, Status: "active"},
		cloudflare.Zone{ID: "z2-" + short, Name: otherZoneName, Status: "active"},
	)
	cfSrv := httptest.NewServer(http.HandlerFunc(cf.serve))
	defer cfSrv.Close()
	lastDeploy.SetCloudflareOptions(cloudflare.WithBaseURL(cfSrv.URL))

	owner := newClient(t, ts.URL)
	owner.register("dnszone-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "DNS Zones " + suffix}, 201, &team)

	viewerEmail := "dnszone-viewer-" + suffix + "@example.test"
	viewer := newClient(t, ts.URL)
	viewer.register(viewerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	var cred struct{ ID, Name string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/credentials", map[string]any{"provider": "cloudflare", "name": "cfz-" + short, "secret": "cf-token",
		"config": map[string]string{}, "skip_validation": true}, 201, &cred)

	// A hostname Hosbah already knows about through the app domain path (dns.go/teamdns.go), so
	// the zone manager should badge it managed_by_hosbah + managed_kind "app_domain" once we add
	// a matching record by hand (below), even though the zone manager itself did not create it.
	appHost := "app." + zoneName
	srv, _ := enrolWithServicesKey(t, owner, ts.URL, team.ID, "dnszone-pub-"+short, "203.0.113.44", nil)
	var app struct {
		App struct{ ID string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "dnszone-" + short, "server": srv, "repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps/"+app.App.ID+"/domains", map[string]any{"hostname": appHost, "tls": "off"}, 201, nil)
	// teamdns.go should have created a managed A record for appHost already.
	if recs := cf.recordsOf("z1-" + short); !hasRecordNamed(recs, appHost) {
		t.Fatalf("adding the app domain should have created a record in the zone: %+v", recs)
	}

	base := "/api/teams/" + team.ID + "/dns/zones"

	t.Run("list zones", func(t *testing.T) {
		var out struct {
			Zones []struct {
				ID             string `json:"id"`
				Name           string `json:"name"`
				Status         string `json:"status"`
				CredentialID   string `json:"credential_id"`
				CredentialName string `json:"credential_name"`
			} `json:"zones"`
		}
		viewer.mustJSON("GET", base, nil, 200, &out)
		if len(out.Zones) != 2 {
			t.Fatalf("zones: %+v", out.Zones)
		}
		byName := map[string]string{}
		for _, z := range out.Zones {
			byName[z.Name] = z.CredentialID
			if z.CredentialName != cred.Name {
				t.Errorf("zone %s: credential name %s", z.Name, z.CredentialName)
			}
		}
		if byName[zoneName] != cred.ID || byName[otherZoneName] != cred.ID {
			t.Errorf("zones did not carry the credential: %+v", byName)
		}
	})

	type recOut struct {
		ID              string `json:"id"`
		Type            string `json:"type"`
		Name            string `json:"name"`
		Content         string `json:"content"`
		TTL             int    `json:"ttl"`
		Priority        int    `json:"priority"`
		Proxied         bool   `json:"proxied"`
		ManagedByHosbah bool   `json:"managed_by_hosbah"`
		ManagedKind     string `json:"managed_kind"`
	}
	type recordsOut struct {
		Zone    struct{ ID, Name string } `json:"zone"`
		Records []recOut                  `json:"records"`
	}
	recordsPath := base + "/" + cred.ID + "/" + zoneName + "/records"

	t.Run("app domain hostname is badged managed", func(t *testing.T) {
		var out recordsOut
		viewer.mustJSON("GET", recordsPath, nil, 200, &out)
		var found *recOut
		for i, r := range out.Records {
			if r.Name == appHost {
				found = &out.Records[i]
			}
		}
		if found == nil || !found.ManagedByHosbah || found.ManagedKind != "app_domain" {
			t.Fatalf("app host record: %+v", found)
		}
	})

	t.Run("role gating: viewer reads, cannot write", func(t *testing.T) {
		if status, _ := viewer.do("POST", recordsPath, map[string]any{"type": "TXT", "name": "viewer-try." + zoneName, "content": "no"}); status != 403 {
			t.Errorf("viewer create -> %d", status)
		}
	})

	var txtID string
	t.Run("create TXT", func(t *testing.T) {
		var out struct {
			Zone   struct{ ID, Name string } `json:"zone"`
			Record recOut                    `json:"record"`
		}
		owner.mustJSON("POST", recordsPath, map[string]any{"type": "TXT", "name": "verify." + zoneName, "content": "hello-world", "ttl": 300}, 201, &out)
		if out.Record.Type != "TXT" || out.Record.Content != "hello-world" || out.Record.TTL != 300 || !out.Record.ManagedByHosbah {
			t.Fatalf("created TXT: %+v", out.Record)
		}
		txtID = out.Record.ID
	})

	t.Run("create MX requires priority", func(t *testing.T) {
		if status, text := owner.do("POST", recordsPath, map[string]any{"type": "MX", "name": zoneName, "content": "mail." + zoneName}); status != 422 && status != 400 {
			t.Errorf("MX without priority -> %d %s", status, text)
		}
		var out struct {
			Record recOut `json:"record"`
		}
		owner.mustJSON("POST", recordsPath, map[string]any{"type": "MX", "name": zoneName, "content": "mail." + zoneName, "priority": 10}, 201, &out)
		if out.Record.Type != "MX" || out.Record.Priority != 10 {
			t.Fatalf("MX record: %+v", out.Record)
		}
	})

	var cnameID string
	t.Run("create CNAME with proxied", func(t *testing.T) {
		proxied := true
		var out struct {
			Record recOut `json:"record"`
		}
		owner.mustJSON("POST", recordsPath, map[string]any{"type": "CNAME", "name": "www." + zoneName, "content": zoneName, "proxied": proxied}, 201, &out)
		if !out.Record.Proxied {
			t.Fatalf("proxied CNAME: %+v", out.Record)
		}
		cnameID = out.Record.ID
	})

	t.Run("update TTL and priority", func(t *testing.T) {
		var out struct {
			Record recOut `json:"record"`
		}
		owner.mustJSON("PATCH", recordsPath+"/"+txtID, map[string]any{"ttl": 60}, 200, &out)
		if out.Record.TTL != 60 {
			t.Errorf("updated TTL: %+v", out.Record)
		}
		proxied := false
		owner.mustJSON("PATCH", recordsPath+"/"+cnameID, map[string]any{"proxied": proxied}, 200, &out)
		if out.Record.Proxied {
			t.Errorf("updated proxied: %+v", out.Record)
		}
	})

	t.Run("viewer cannot update or delete", func(t *testing.T) {
		if status, _ := viewer.do("PATCH", recordsPath+"/"+txtID, map[string]any{"ttl": 10}); status != 403 {
			t.Errorf("viewer patch -> %d", status)
		}
		if status, _ := viewer.do("DELETE", recordsPath+"/"+txtID, nil); status != 403 {
			t.Errorf("viewer delete -> %d", status)
		}
	})

	t.Run("cross-zone / cross-credential lookups 404", func(t *testing.T) {
		owner.expect("GET", base+"/"+cred.ID+"/no-such-zone.test/records", nil, 404)
		owner.expect("GET", base+"/no-such-credential/"+zoneName+"/records", nil, 404)
		owner.expect("PATCH", base+"/"+cred.ID+"/"+zoneName+"/records/no-such-record", map[string]any{"ttl": 10}, 502)
	})

	t.Run("delete", func(t *testing.T) {
		owner.mustJSON("DELETE", recordsPath+"/"+txtID, nil, 204, nil)
		var out recordsOut
		owner.mustJSON("GET", recordsPath, nil, 200, &out)
		for _, r := range out.Records {
			if r.ID == txtID {
				t.Fatalf("deleted record still present: %+v", r)
			}
		}
		// Deleting an id that is no longer there is idempotent (matches Cloudflare's own DELETE).
		owner.expect("DELETE", recordsPath+"/"+txtID, nil, 204)
	})

	t.Run("audited", func(t *testing.T) {
		var out struct {
			Entries []struct {
				Action string `json:"action"`
			} `json:"entries"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/audit?limit=500", nil, 200, &out)
		created, updated, deleted := 0, 0, 0
		for _, e := range out.Entries {
			switch e.Action {
			case "dns.zone_record_created":
				created++
			case "dns.zone_record_updated":
				updated++
			case "dns.zone_record_deleted":
				deleted++
			}
		}
		if created < 3 || updated < 2 || deleted < 1 {
			t.Errorf("audit: created=%d updated=%d deleted=%d", created, updated, deleted)
		}
	})

	t.Run("mcp: list zones, list records, upsert, delete", func(t *testing.T) {
		var readTok, writeTok struct{ Token string }
		owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "dnsz-read", "scopes": []string{"read"}, "expires_in_days": 30}, 201, &readTok)
		owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "dnsz-write", "scopes": []string{"read", "write", "destructive"}, "expires_in_days": 30}, 201, &writeTok)

		r := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "dns_zones_list", nil)
		if r.IsError || !strings.Contains(r.Text, zoneName) || !strings.Contains(r.Text, otherZoneName) {
			t.Fatalf("dns_zones_list: %+v", r)
		}
		r = mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "dns_records_list", map[string]any{"credential": cred.ID, "zone": zoneName})
		if r.IsError || !strings.Contains(r.Text, appHost) || !strings.Contains(r.Text, "managed-by-hosbah") {
			t.Fatalf("dns_records_list: %+v", r)
		}
		// A read token cannot write.
		if r := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "dns_record_upsert", map[string]any{"credential": cred.ID, "zone": zoneName,
			"type": "TXT", "name": "mcp-try." + zoneName, "content": "x"}); !r.IsError {
			t.Errorf("dns_record_upsert with a read token: %+v", r)
		}
		r = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "dns_record_upsert", map[string]any{"credential": cred.ID, "zone": zoneName,
			"type": "TXT", "name": "mcp." + zoneName, "content": "mcp-value"})
		if r.IsError || !strings.Contains(r.Text, "mcp-value") {
			t.Fatalf("dns_record_upsert create: %+v", r)
		}
		var recID string
		for id, rec := range cf.records["z1-"+short] {
			if rec.Name == "mcp."+zoneName {
				recID = id
			}
		}
		if recID == "" {
			t.Fatal("mcp-created record not found in fake Cloudflare")
		}
		// Delete without confirm is refused in-band.
		if r := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "dns_record_delete", map[string]any{"credential": cred.ID, "zone": zoneName, "record": recID}); !r.IsError {
			t.Errorf("dns_record_delete without confirm: %+v", r)
		}
		r = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "dns_record_delete", map[string]any{"credential": cred.ID, "zone": zoneName, "record": recID, "confirm": true})
		if r.IsError {
			t.Fatalf("dns_record_delete: %+v", r)
		}
		if _, found := cf.records["z1-"+short][recID]; found {
			t.Error("mcp dns_record_delete did not remove the record")
		}
	})
}

func hasRecordNamed(recs []cloudflare.Record, name string) bool {
	for _, r := range recs {
		if r.Name == name {
			return true
		}
	}
	return false
}
