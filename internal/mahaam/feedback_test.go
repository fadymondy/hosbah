package mahaam

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type intake struct {
	mu      sync.Mutex
	reports []map[string]string
	origin  string
	got     chan struct{}
}

func (in *intake) handler(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseMultipartForm(1 << 20)
	f := map[string]string{}
	for k, v := range r.MultipartForm.Value {
		f[k] = v[0]
	}
	in.mu.Lock()
	in.reports = append(in.reports, f)
	in.origin = r.Header.Get("Origin")
	in.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(w, `{"key":"MH-7"}`)
	in.got <- struct{}{}
}

func TestSendAndOnError(t *testing.T) {
	in := &intake{got: make(chan struct{}, 10)}
	srv := httptest.NewServer(http.HandlerFunc(in.handler))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Key: "pfk_test", AppURL: "https://app.hosbah.com/x"})

	key, err := c.Send(t.Context(), Report{Title: "hello", Type: "task"})
	if err != nil || key != "MH-7" {
		t.Fatalf("send: %q %v", key, err)
	}
	<-in.got
	if in.origin != "https://app.hosbah.com" || in.reports[0]["public_key"] != "pfk_test" || in.reports[0]["page_url"] != "https://app.hosbah.com/x" {
		t.Fatalf("origin %q report %v", in.origin, in.reports[0])
	}

	// The same error three times (a crash loop) files one issue; a non-error payload files none.
	for i := 0; i < 3; i++ {
		_ = c.OnError(t.Context(), errors.New("boom"))
	}
	_ = c.OnError(t.Context(), "not an error")
	select {
	case <-in.got:
	case <-time.After(5 * time.Second):
		t.Fatal("error not filed")
	}
	time.Sleep(200 * time.Millisecond)
	in.mu.Lock()
	defer in.mu.Unlock()
	if len(in.reports) != 2 {
		t.Fatalf("want 1 error report after 3 errors, got %d reports total", len(in.reports))
	}
	if p := in.reports[1]; p["title"] != "API error: boom" || p["priority"] != "high" || p["issue_type"] != "bug" {
		t.Fatalf("error report %v", p)
	}
}

func TestDisabledIsSafe(t *testing.T) {
	var c *Client
	if c.Enabled() || c.OnError(t.Context(), errors.New("x")) != nil {
		t.Fatal("nil client must be a disabled no-op")
	}
	if _, err := New(Config{}).Send(t.Context(), Report{Title: "x"}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("unconfigured send: %v", err)
	}
}
