package push

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
)

func TestEventURLTagAndLocale(t *testing.T) {
	ev := Event{TeamID: "t1", Kind: EventAppDown, Title: "shop is down", Body: "timeout", TitleAR: "المتجر متوقف",
		SubjectType: "app", SubjectID: "a1", Link: "/apps/a1/"}
	if got := ev.URL(); got != "hosbah://t/t1/apps/a1" {
		t.Fatalf("URL = %q", got)
	}
	up := ev
	up.Kind = EventAppUp
	if ev.tag() != "app:a1" || up.tag() != ev.tag() {
		t.Fatalf("down and up should share a tag: %q %q", ev.tag(), up.tag())
	}
	if (Event{TeamID: "t1", Kind: EventCertExpiring}).URL() != "hosbah://t/t1/alerts" {
		t.Fatal("an event without a link should open the alerts list")
	}

	ar := ev.Message("ar")
	if ar.Title != "المتجر متوقف" || ar.Body != "timeout" {
		t.Fatalf("ar message = %+v (want the Arabic title, the English body as fallback)", ar)
	}
	en := ev.Message("en")
	if en.Title != "shop is down" {
		t.Fatalf("en title = %q", en.Title)
	}
	for k, want := range map[string]string{"team": "t1", "kind": EventAppDown, "subject_type": "app", "subject_id": "a1", "url": "hosbah://t/t1/apps/a1"} {
		if en.Data[k] != want {
			t.Errorf("data[%s] = %q, want %q", k, en.Data[k], want)
		}
	}
	if !KnownEvent(EventBackupFailed) || KnownEvent("monitor.opened") {
		t.Fatal("KnownEvent")
	}
	if got := clip(strings.Repeat("é", 10), 5); got != "éééé…" {
		t.Fatalf("clip = %q", got)
	}
}

// fakeFCM is the OAuth token endpoint and the FCM send endpoint in one server.
type fakeFCM struct {
	mu       sync.Mutex
	tokens   int
	messages []map[string]any
	answer   func(token string) (int, string)
}

func (f *fakeFCM) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || r.Form.Get("assertion") == "" {
				http.Error(w, "bad grant", 400)
				return
			}
			f.tokens++
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "ya29.test", "expires_in": 3600})
		case r.URL.Path == "/v1/projects/proj-1/messages:send":
			if r.Header.Get("Authorization") != "Bearer ya29.test" {
				http.Error(w, "no auth", 401)
				return
			}
			b, _ := io.ReadAll(r.Body)
			var body struct {
				Message map[string]any `json:"message"`
			}
			_ = json.Unmarshal(b, &body)
			f.messages = append(f.messages, body.Message)
			code, text := f.answer(body.Message["token"].(string))
			w.WriteHeader(code)
			_, _ = io.WriteString(w, text)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

func testAccount(t *testing.T, tokenURI string) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	b, _ := json.Marshal(serviceAccount{ClientEmail: "push@proj-1.iam.gserviceaccount.com", PrivateKey: string(pemKey), TokenURI: tokenURI})
	return b
}

func TestFCMSenderClassifiesAnswers(t *testing.T) {
	f := &fakeFCM{answer: func(token string) (int, string) {
		switch token {
		case "gone":
			return 404, `{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`
		case "bogus":
			return 400, `{"error":{"message":"The registration token is not a valid FCM registration token"}}`
		case "flaky":
			return 503, `unavailable`
		}
		return 200, `{"name":"projects/proj-1/messages/1"}`
	}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	s, err := newFCM("proj-1", srv.URL, testAccount(t, srv.URL+"/token"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	m := Event{TeamID: "t1", Kind: EventDeployFailed, Title: "x", SubjectID: "d1", Tag: strings.Repeat("k", 80)}.Message("en")
	for token, want := range map[string]Result{"ok": SentOK, "gone": SentGone, "bogus": SentGone, "flaky": SentFailed} {
		res, err := s.Send(ctx, token, m)
		if res != want {
			t.Errorf("%s: result %v (err %v), want %v", token, res, err, want)
		}
		if (want == SentFailed) != (err != nil) {
			t.Errorf("%s: err = %v", token, err)
		}
	}
	if f.tokens != 1 {
		t.Errorf("access token fetched %d times, want 1 (cached)", f.tokens)
	}
	msg := f.messages[0]
	android := msg["android"].(map[string]any)
	if android["priority"] != "high" || len(android["collapse_key"].(string)) != 64 {
		t.Errorf("android = %v (want high priority, a 64-byte collapse key)", android)
	}
	if msg["data"].(map[string]any)["url"] != "hosbah://t/t1/alerts" {
		t.Errorf("data = %v", msg["data"])
	}
	apns := msg["apns"].(map[string]any)
	if apns["headers"].(map[string]any)["apns-collapse-id"] == nil {
		t.Errorf("apns = %v (want a collapse id)", apns)
	}
}

func TestSenderFromEnvIsOffWithoutConfig(t *testing.T) {
	for _, k := range []string{"HOSBAH_FCM_PROJECT_ID", "HOSBAH_FCM_SERVICE_ACCOUNT", "HOSBAH_FCM_SERVICE_ACCOUNT_FILE",
		"CLOUDY_FCM_PROJECT_ID", "CLOUDY_FCM_SERVICE_ACCOUNT", "CLOUDY_FCM_SERVICE_ACCOUNT_FILE"} {
		t.Setenv(k, "")
	}
	s, err := SenderFromEnv()
	if s != nil || err != nil {
		t.Fatalf("SenderFromEnv() = %v, %v; want nil, nil", s, err)
	}
	t.Setenv("HOSBAH_FCM_PROJECT_ID", "proj-1")
	t.Setenv("HOSBAH_FCM_SERVICE_ACCOUNT", "not json")
	if _, err := SenderFromEnv(); err == nil {
		t.Fatal("a broken service account should be an error, not silently off")
	}
}

func TestNilServiceIsANoOp(t *testing.T) {
	Set(nil)
	Notify(Event{TeamID: "t", Kind: EventAppDown})
	Record(context.Background(), Event{TeamID: "t", Kind: EventAppDown})
	Current().Channels(context.Background(), Event{})
	if Current().Enabled() {
		t.Fatal("no service is not enabled")
	}
}

func metrics(cpu, mem, disk float64) *string {
	b, _ := json.Marshal(map[string]float64{"cpu_percent": cpu, "mem_used_bytes": mem, "mem_total_bytes": 100,
		"disk_used_bytes": disk, "disk_total_bytes": 100})
	s := string(b)
	return &s
}

func kinds(evs []Event) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Kind+":"+e.Tag)
	}
	return out
}

func TestSweeperOfflineAndResourceHigh(t *testing.T) {
	sw := New(nil, nil, nil).newSweeper()
	t0 := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	row := func(now time.Time, seen time.Time, m *string) []db.AlertableServersRow {
		return []db.AlertableServersRow{{ID: "s1", TeamID: "t1", Name: "edge-1", Metrics: m, LastSeenAt: &seen}}
	}

	// First sweep after a restart: already hot and the state is learnt, nothing alerts.
	if evs := sw.evaluate(row(t0, t0, metrics(95, 10, 10)), t0); len(evs) != 0 {
		t.Fatalf("first sweep alerted: %v", kinds(evs))
	}
	// Second hot sweep: CPU has now been high twice in a row.
	t1 := t0.Add(time.Minute)
	evs := sw.evaluate(row(t1, t1, metrics(96, 10, 10)), t1)
	if got := kinds(evs); len(got) != 1 || got[0] != "resource.high:resource:s1:cpu" {
		t.Fatalf("second sweep = %v, want one CPU alert", got)
	}
	if evs[0].TitleAR == "" || evs[0].Link != "servers/s1" {
		t.Fatalf("alert = %+v", evs[0])
	}
	// Still hot inside the cooldown: quiet. Memory climbs over twice: its own alert.
	t2 := t1.Add(time.Minute)
	if evs := sw.evaluate(row(t2, t2, metrics(97, 92, 10)), t2); len(evs) != 0 {
		t.Fatalf("inside cooldown = %v", kinds(evs))
	}
	t3 := t2.Add(time.Minute)
	if got := kinds(sw.evaluate(row(t3, t3, metrics(97, 93, 10)), t3)); len(got) != 1 || got[0] != "resource.high:resource:s1:memory" {
		t.Fatalf("memory = %v", got)
	}
	// Agent goes quiet: one offline alert, then nothing more while it stays offline.
	t4 := t3.Add(OfflineAfter + time.Minute)
	if got := kinds(sw.evaluate(row(t4, t3, nil), t4)); len(got) != 1 || !strings.HasPrefix(got[0], "server.offline") {
		t.Fatalf("offline = %v", got)
	}
	t5 := t4.Add(time.Minute)
	if evs := sw.evaluate(row(t5, t3, nil), t5); len(evs) != 0 {
		t.Fatalf("still offline = %v", kinds(evs))
	}
	// A server that disappears is forgotten.
	sw.evaluate(nil, t5.Add(time.Minute))
	if len(sw.state) != 0 {
		t.Fatalf("state kept %d servers", len(sw.state))
	}
}
