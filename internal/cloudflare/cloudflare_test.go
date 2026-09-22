package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fake is an in-memory Cloudflare: zones by name, records by id.
type fake struct {
	mu      sync.Mutex
	zones   []Zone
	records map[string]Record // id -> record
	zoneOf  map[string]string // record id -> zone id
	next    int
}

func newFake(zones ...string) (*fake, *Client) {
	f := &fake{records: map[string]Record{}, zoneOf: map[string]string{}}
	for i, z := range zones {
		f.zones = append(f.zones, Zone{ID: fmt.Sprintf("z%d", i), Name: z, Status: "active"})
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	return f, New("tok", WithBaseURL(srv.URL))
}

func write(w http.ResponseWriter, status int, result any) {
	w.WriteHeader(status)
	b, _ := json.Marshal(result)
	fmt.Fprintf(w, `{"success":%t,"errors":[],"result":%s}`, status < 300, b)
}

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"success":false,"errors":[{"code":9109,"message":"Invalid access token"}]}`)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.URL.Path == "/user/tokens/verify":
		write(w, 200, map[string]string{"status": "active"})
	case r.URL.Path == "/zones":
		out := []Zone{}
		for _, z := range f.zones {
			if n := r.URL.Query().Get("name"); n == "" || n == z.Name {
				out = append(out, z)
			}
		}
		write(w, 200, out)
	case len(parts) >= 3 && parts[0] == "zones" && parts[2] == "dns_records":
		zone := parts[1]
		switch {
		case r.Method == http.MethodGet:
			out := []Record{}
			for id, rec := range f.records {
				if f.zoneOf[id] == zone && rec.Name == r.URL.Query().Get("name") && (r.URL.Query().Get("type") == "" || rec.Type == r.URL.Query().Get("type")) {
					out = append(out, rec)
				}
			}
			write(w, 200, out)
		case r.Method == http.MethodPost:
			var rec Record
			_ = json.NewDecoder(r.Body).Decode(&rec)
			f.next++
			rec.ID = fmt.Sprintf("r%d", f.next)
			f.records[rec.ID], f.zoneOf[rec.ID] = rec, zone
			write(w, 200, rec)
		case r.Method == http.MethodPut:
			var rec Record
			_ = json.NewDecoder(r.Body).Decode(&rec)
			rec.ID = parts[3]
			f.records[rec.ID] = rec
			write(w, 200, rec)
		case r.Method == http.MethodPatch:
			rec, ok := f.records[parts[3]]
			if !ok {
				w.WriteHeader(404)
				fmt.Fprint(w, `{"success":false,"errors":[{"code":81044,"message":"Record does not exist."}]}`)
				return
			}
			var p RecordPatch
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
			f.records[rec.ID] = rec
			write(w, 200, rec)
		case r.Method == http.MethodDelete:
			delete(f.records, parts[3])
			write(w, 200, map[string]string{"id": parts[3]})
		}
	default:
		w.WriteHeader(404)
		fmt.Fprint(w, `{"success":false,"errors":[{"code":7003,"message":"no route"}]}`)
	}
}

func TestVerifyAndBadToken(t *testing.T) {
	_, c := newFake("example.com")
	if err := c.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	bad := New("nope", WithBaseURL(c.baseURL))
	err := bad.Verify(context.Background())
	var ce *Error
	if !errors.As(err, &ce) || ce.Status != 403 || !strings.Contains(ce.Msg, "Invalid access token") || strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestZoneForPicksTheDeepestZone(t *testing.T) {
	_, c := newFake("fadymondy.com", "apps.fadymondy.com")
	z, err := c.ZoneFor(context.Background(), "hosbah-app-x.apps.fadymondy.com")
	if err != nil || z.Name != "apps.fadymondy.com" {
		t.Fatalf("zone = %+v err = %v", z, err)
	}
	z, err = c.ZoneFor(context.Background(), "hosbah-app-healthdebug.fadymondy.com")
	if err != nil || z.Name != "fadymondy.com" {
		t.Fatalf("zone = %+v err = %v", z, err)
	}
	if _, err := c.ZoneFor(context.Background(), "other.org"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestPointHostIsIdempotentAndUpdates(t *testing.T) {
	f, c := newFake("fadymondy.com")
	ctx := context.Background()
	host := "hosbah-app-healthdebug.fadymondy.com"
	if _, _, err := c.PointHost(ctx, host, "203.0.113.10", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.PointHost(ctx, host, "203.0.113.10", false); err != nil {
		t.Fatal(err)
	}
	if len(f.records) != 1 {
		t.Fatalf("records = %v", f.records)
	}
	if _, r, err := c.PointHost(ctx, host, "203.0.113.20", false); err != nil || r.Content != "203.0.113.20" {
		t.Fatalf("r = %+v err = %v", r, err)
	}
	if _, r, _ := c.PointHost(ctx, "v6.fadymondy.com", "2001:db8::1", true); r.Type != "AAAA" {
		t.Fatalf("type = %s", r.Type)
	}
	if err := c.Delete(ctx, "z0", host, ""); err != nil || len(f.records) != 1 {
		t.Fatalf("after delete: %v %v", f.records, err)
	}
}

func TestUpsertCollapsesDuplicates(t *testing.T) {
	f, c := newFake("example.com")
	f.records["a"], f.zoneOf["a"] = Record{ID: "a", Type: "TXT", Name: "_v.example.com", Content: "old"}, "z0"
	f.records["b"], f.zoneOf["b"] = Record{ID: "b", Type: "TXT", Name: "_v.example.com", Content: "old2"}, "z0"
	if _, err := c.Upsert(context.Background(), "z0", Record{Type: "TXT", Name: "_v.example.com", Content: "new"}); err != nil {
		t.Fatal(err)
	}
	if len(f.records) != 1 {
		t.Fatalf("records = %v", f.records)
	}
	for _, r := range f.records {
		if r.Content != "new" {
			t.Fatalf("content = %q", r.Content)
		}
	}
}

func TestCreatePatchDeleteOneRecord(t *testing.T) {
	f, c := newFake("example.com")
	ctx := context.Background()
	f.records["own"], f.zoneOf["own"] = Record{ID: "own", Type: "A", Name: "app.example.com", Content: "198.51.100.1", Comment: "the user's"}, "z0"

	rec, err := c.CreateRecord(ctx, "z0", Record{Type: "A", Name: "new.example.com", Content: "203.0.113.10", Comment: "ignored"})
	if err != nil || rec.ID == "" || !Managed(rec) || rec.TTL != 1 {
		t.Fatalf("created %+v err = %v", rec, err)
	}
	if !Managed(Record{Comment: "managed by Cloudy"}) || Managed(f.records["own"]) {
		t.Fatal("the legacy marker is ours; a user's comment is not")
	}

	// A partial update leaves what it does not name alone.
	on := true
	got, err := c.PatchRecord(ctx, "z0", "own", RecordPatch{Proxied: &on})
	if err != nil || got.Content != "198.51.100.1" || got.Comment != "the user's" || got.Proxied == nil || !*got.Proxied {
		t.Fatalf("patched %+v err = %v", got, err)
	}
	if _, err := c.PatchRecord(ctx, "z0", "nope", RecordPatch{Proxied: &on}); err == nil {
		t.Fatal("patching a record that does not exist must fail")
	}
	if err := c.DeleteRecord(ctx, "z0", rec.ID); err != nil || len(f.records) != 1 {
		t.Fatalf("after delete: %v %v", f.records, err)
	}
}
