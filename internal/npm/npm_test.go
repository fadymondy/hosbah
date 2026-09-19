package npm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type fake struct {
	mu    sync.Mutex
	hosts map[int]ProxyHost
	next  int
}

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/api/tokens" {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["secret"] != "pw" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid password"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"token":"t1"}`))
		return
	}
	if r.Header.Get("Authorization") != "Bearer t1" {
		w.WriteHeader(401)
		return
	}
	switch {
	case r.Method == http.MethodGet:
		out := []ProxyHost{}
		for _, h := range f.hosts {
			out = append(out, h)
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodPost:
		var h ProxyHost
		_ = json.NewDecoder(r.Body).Decode(&h)
		f.next++
		h.ID = f.next
		f.hosts[h.ID] = h
		_ = json.NewEncoder(w).Encode(h)
	case r.Method == http.MethodPut || r.Method == http.MethodDelete:
		id, _ := strconv.Atoi(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		if r.Method == http.MethodDelete {
			delete(f.hosts, id)
		} else {
			var h ProxyHost
			_ = json.NewDecoder(r.Body).Decode(&h)
			h.ID = id
			f.hosts[id] = h
		}
		_, _ = w.Write([]byte(`true`))
	}
}

func TestRouteUnrouteOnlyTouchesItsOwn(t *testing.T) {
	f := &fake{hosts: map[int]ProxyHost{1: {ID: 1, DomainNames: []string{"app.example.com"}, ForwardHost: "10.0.0.1", ForwardPort: 80}}, next: 1}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	defer srv.Close()
	ctx := context.Background()
	c := New(srv.URL, "a@b.c", "pw")

	if err := c.Route(ctx, "demo.example.com", "10.10.10.1", 47001, 2); err != nil {
		t.Fatal(err)
	}
	if err := c.Route(ctx, "demo.example.com", "10.10.10.1", 47002, 2); err != nil { // update, not a second host
		t.Fatal(err)
	}
	if len(f.hosts) != 2 || f.hosts[2].ForwardPort != 47002 || !f.hosts[2].SSLForced || f.hosts[2].AdvancedConfig != Marker {
		t.Fatalf("hosts = %+v", f.hosts)
	}
	if err := c.Route(ctx, "app.example.com", "10.10.10.1", 47003, 2); err == nil {
		t.Fatal("must not take over a proxy host Hosbah did not create")
	}
	if err := c.Unroute(ctx, "app.example.com"); err != nil || len(f.hosts) != 2 {
		t.Fatal("must not delete a proxy host Hosbah did not create")
	}
	if err := c.Unroute(ctx, "demo.example.com"); err != nil || len(f.hosts) != 1 {
		t.Fatalf("unroute: %v %v", err, f.hosts)
	}
	if err := New(srv.URL, "a@b.c", "bad").Login(ctx); err == nil || !strings.Contains(err.Error(), "Invalid password") {
		t.Fatalf("bad login: %v", err)
	}
}
