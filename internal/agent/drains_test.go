package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// withTempDrainsFile points drainsFile at a fresh temp file for the duration of one test, so tests
// never touch a real /var/lib/hosbah and never interfere with each other.
func withTempDrainsFile(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "log-drains.json")
	prev := drainsFile
	drainsFile = func() string { return path }
	t.Cleanup(func() { drainsFile = prev })
}

func testSyslogConfig(drainID string) agentproto.DrainConfig {
	return agentproto.DrainConfig{DrainID: drainID, TeamID: "00000000-0000-4000-8000-0000000000e1", Kind: agentproto.DrainKindSyslog,
		Syslog: &agentproto.SyslogSink{Transport: agentproto.SyslogTransportUDP, Host: "syslog.example.test", Port: 514}}
}

func newTestDrainHost() *drainHost {
	h := newDrainHost()
	h.baseCtx = context.Background()
	h.serverID = "srv-1"
	return h
}

func TestDrainHostSetPersistsAndStarts(t *testing.T) {
	withTempDrainsFile(t)
	h := newTestDrainHost()
	drainID := "00000000-0000-4000-8000-0000000000d1"
	cfg := testSyslogConfig(drainID)

	oc, _ := opCtx(t, cfg)
	res, exit, err := h.set(oc)
	if err != nil || exit != 0 {
		t.Fatalf("set: exit=%d err=%v", exit, err)
	}
	if m, ok := res.(map[string]any); !ok || m["drain_id"] != drainID {
		t.Fatalf("set result = %+v", res)
	}

	// Persisted to disk.
	saved, err := loadDrainConfigs()
	if err != nil {
		t.Fatalf("loadDrainConfigs: %v", err)
	}
	if _, ok := saved[drainID]; !ok {
		t.Fatalf("drain not persisted: %+v", saved)
	}

	// Running: status reports it configured.
	statusOC, _ := opCtx(t, agentproto.DrainStatusParams{DrainID: drainID})
	sres, exit, err := h.status(statusOC)
	if err != nil || exit != 0 {
		t.Fatalf("status: exit=%d err=%v", exit, err)
	}
	st, ok := sres.(agentproto.DrainStatus)
	if !ok || !st.Configured {
		t.Fatalf("status = %+v, want Configured", sres)
	}

	h.mu.Lock()
	_, running := h.runtimes[drainID]
	h.mu.Unlock()
	if !running {
		t.Fatal("set should have started a runtime for the drain")
	}
}

func TestDrainHostSetRemove(t *testing.T) {
	withTempDrainsFile(t)
	h := newTestDrainHost()
	drainID := "00000000-0000-4000-8000-0000000000d2"
	cfg := testSyslogConfig(drainID)

	oc, _ := opCtx(t, cfg)
	if _, exit, err := h.set(oc); err != nil || exit != 0 {
		t.Fatalf("initial set: exit=%d err=%v", exit, err)
	}

	remove := agentproto.DrainConfig{DrainID: drainID, TeamID: cfg.TeamID, Remove: true}
	rmOC, _ := opCtx(t, remove)
	res, exit, err := h.set(rmOC)
	if err != nil || exit != 0 {
		t.Fatalf("remove: exit=%d err=%v", exit, err)
	}
	if m, ok := res.(map[string]any); !ok || m["removed"] != true {
		t.Fatalf("remove result = %+v", res)
	}

	saved, err := loadDrainConfigs()
	if err != nil {
		t.Fatalf("loadDrainConfigs: %v", err)
	}
	if _, ok := saved[drainID]; ok {
		t.Fatalf("drain should no longer be persisted: %+v", saved)
	}

	h.mu.Lock()
	_, running := h.runtimes[drainID]
	h.mu.Unlock()
	if running {
		t.Fatal("remove should have stopped the drain's runtime")
	}

	statusOC, _ := opCtx(t, agentproto.DrainStatusParams{DrainID: drainID})
	sres, _, _ := h.status(statusOC)
	if st, ok := sres.(agentproto.DrainStatus); !ok || st.Configured {
		t.Fatalf("status after remove = %+v, want not configured", sres)
	}
}

func TestDrainHostSetRejectsAnInvalidConfig(t *testing.T) {
	withTempDrainsFile(t)
	h := newTestDrainHost()
	// A syslog drain with no syslog settings: Validate() fails before anything is persisted.
	bad := agentproto.DrainConfig{DrainID: "00000000-0000-4000-8000-0000000000d3", TeamID: "00000000-0000-4000-8000-0000000000e1",
		Kind: agentproto.DrainKindSyslog}
	oc, _ := opCtx(t, bad)
	if _, exit, err := h.set(oc); err == nil || exit != 2 {
		t.Fatalf("set with an invalid config: exit=%d err=%v, want exit 2 and an error", exit, err)
	}
	saved, err := loadDrainConfigs()
	if err != nil {
		t.Fatalf("loadDrainConfigs: %v", err)
	}
	if len(saved) != 0 {
		t.Fatalf("nothing should have been persisted: %+v", saved)
	}
}

func TestDrainHostStatusOfAnUnknownDrainIsNotConfigured(t *testing.T) {
	withTempDrainsFile(t)
	h := newTestDrainHost()
	oc, _ := opCtx(t, agentproto.DrainStatusParams{DrainID: "00000000-0000-4000-8000-0000000000d4"})
	res, exit, err := h.status(oc)
	if err != nil || exit != 0 {
		t.Fatalf("status: exit=%d err=%v", exit, err)
	}
	st, ok := res.(agentproto.DrainStatus)
	if !ok || st.Configured {
		t.Fatalf("status of an unconfigured drain = %+v", res)
	}
}

func TestDrainHostStatusRejectsBadParams(t *testing.T) {
	withTempDrainsFile(t)
	h := newTestDrainHost()
	oc, _ := opCtx(t, agentproto.DrainStatusParams{DrainID: "not-a-uuid"})
	if _, exit, err := h.status(oc); err == nil || exit != 2 {
		t.Fatalf("status with a bad drain id: exit=%d err=%v, want exit 2 and an error", exit, err)
	}
}

func TestDrainHostTestRejectsAnInvalidConfig(t *testing.T) {
	withTempDrainsFile(t)
	h := newTestDrainHost()
	oc, _ := opCtx(t, agentproto.DrainTestParams{Drain: agentproto.DrainConfig{DrainID: "00000000-0000-4000-8000-0000000000d5",
		TeamID: "00000000-0000-4000-8000-0000000000e1", Kind: "carrier-pigeon"}})
	if _, exit, err := h.test(oc); err == nil || exit != 2 {
		t.Fatalf("test with an invalid config: exit=%d err=%v, want exit 2 and an error", exit, err)
	}
}

func TestDrainHostTestReportsAnUnreachableSink(t *testing.T) {
	withTempDrainsFile(t)
	h := newTestDrainHost()
	// A syslog collector nothing listens on: the ship fails fast (loopback, closed port), and test
	// must report that failure in-band rather than as a bare operation error.
	cfg := agentproto.DrainConfig{DrainID: "00000000-0000-4000-8000-0000000000d6", TeamID: "00000000-0000-4000-8000-0000000000e1",
		Kind: agentproto.DrainKindSyslog, Syslog: &agentproto.SyslogSink{Transport: agentproto.SyslogTransportUDP, Host: "127.0.0.1", Port: 1}}
	oc, _ := opCtx(t, agentproto.DrainTestParams{Drain: cfg})
	res, exit, err := h.test(oc)
	if err == nil {
		t.Fatal("an unreachable sink should be reported as an error")
	}
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
	result, ok := res.(agentproto.DrainTestResult)
	if !ok || result.OK || result.Detail == "" {
		t.Fatalf("result = %+v, want OK=false with a detail", res)
	}
}

func TestOpenDrainAuthOpensABoxSealedToThisAgentsKey(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	drainID := "00000000-0000-4000-8000-0000000000d7"
	sealed, err := agentproto.SealBox(base64.StdEncoding.EncodeToString(pub), agentproto.DrainSecretContext(drainID), []byte("secret-token"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := agentproto.DrainConfig{DrainID: drainID, Kind: agentproto.DrainKindHTTP,
		HTTP: &agentproto.HTTPSink{URL: "https://collector.example.test/ingest", AuthHeader: "Authorization", AuthSealed: sealed}}
	oc := &OpContext{Ctx: context.Background(), priv: priv, log: func(string, string) {}}
	val, err := openDrainAuth(oc, cfg)
	if err != nil {
		t.Fatalf("openDrainAuth: %v", err)
	}
	if val != "secret-token" {
		t.Errorf("opened value = %q, want secret-token", val)
	}

	// A box sealed to a different drain id does not open (context mismatch).
	otherCfg := cfg
	otherCfg.DrainID = "00000000-0000-4000-8000-0000000000d8"
	if _, err := openDrainAuth(oc, otherCfg); err == nil {
		t.Error("a box sealed to a different drain id should not open")
	}

	// A box opened with the wrong agent key does not open either.
	_, wrongPriv, _ := ed25519.GenerateKey(nil)
	wrongOC := &OpContext{Ctx: context.Background(), priv: wrongPriv, log: func(string, string) {}}
	if _, err := openDrainAuth(wrongOC, cfg); err == nil {
		t.Error("a box opened with the wrong agent key should not open")
	}

	// No auth_sealed: an empty value, no error (a drain need not have an auth header).
	noAuth := agentproto.DrainConfig{DrainID: drainID, Kind: agentproto.DrainKindHTTP,
		HTTP: &agentproto.HTTPSink{URL: "https://collector.example.test/ingest"}}
	if v, err := openDrainAuth(oc, noAuth); err != nil || v != "" {
		t.Errorf("no auth_sealed: got %q, %v", v, err)
	}
}

func TestLoadDrainConfigsMissingFileIsEmptyNotAnError(t *testing.T) {
	withTempDrainsFile(t)
	m, err := loadDrainConfigs()
	if err != nil {
		t.Fatalf("loadDrainConfigs on a missing file: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("expected an empty map, got %+v", m)
	}
}

func TestLoadDrainConfigsCorruptFileErrors(t *testing.T) {
	withTempDrainsFile(t)
	if err := os.MkdirAll(filepath.Dir(drainsFile()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(drainsFile(), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDrainConfigs(); err == nil {
		t.Fatal("a corrupt drains file should error")
	}
}

func TestSaveDrainConfigsRoundTrips(t *testing.T) {
	withTempDrainsFile(t)
	drainID := "00000000-0000-4000-8000-0000000000d9"
	cfg := testSyslogConfig(drainID)
	if err := saveDrainConfigs(map[string]agentproto.DrainConfig{drainID: cfg}); err != nil {
		t.Fatalf("saveDrainConfigs: %v", err)
	}
	loaded, err := loadDrainConfigs()
	if err != nil {
		t.Fatalf("loadDrainConfigs: %v", err)
	}
	got, ok := loaded[drainID]
	if !ok {
		t.Fatalf("saved drain not found: %+v", loaded)
	}
	raw1, _ := json.Marshal(cfg)
	raw2, _ := json.Marshal(got)
	if string(raw1) != string(raw2) {
		t.Errorf("round trip changed the config:\n  before %s\n  after  %s", raw1, raw2)
	}
}

func TestDrainRuntimeRecordsSuccessErrorAndDrop(t *testing.T) {
	rt := &drainRuntime{}
	rt.recordSuccess(3)
	rt.recordSuccess(2)
	if rt.status.Shipped != 5 {
		t.Errorf("Shipped = %d, want 5", rt.status.Shipped)
	}
	if rt.status.LastSuccessAt == nil || time.Since(*rt.status.LastSuccessAt) > time.Minute {
		t.Error("LastSuccessAt should be set to roughly now")
	}
	rt.recordError(context.DeadlineExceeded)
	if rt.status.LastError == "" || rt.status.LastErrorAt == nil {
		t.Error("recordError should set LastError and LastErrorAt")
	}
	rt.recordDrop(4)
	rt.recordDrop(0) // a non-positive drop is a no-op
	if rt.status.Dropped != 4 {
		t.Errorf("Dropped = %d, want 4", rt.status.Dropped)
	}
	snap := rt.snapshot("drain-x")
	if snap.DrainID != "drain-x" || !snap.Configured || snap.Shipped != 5 || snap.Dropped != 4 {
		t.Errorf("snapshot = %+v", snap)
	}
}
