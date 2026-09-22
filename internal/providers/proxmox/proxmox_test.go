package proxmox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/providers"
)

const (
	testNode    = "pve1"
	testTokenID = "hosbah@pve!ctl"
	testSecret  = "5f1c0e2a-secret-token-value"
	testTmpl    = 9000
)

// ---- fake PVE --------------------------------------------------------------------------------

type call struct {
	Method, Path, RawBody string
	Form                  url.Values // decoded body (POST/PUT) or query (GET/DELETE)
}

type fakeGuest struct {
	typ, name, status string
	template, agent   bool
	config            map[string]string
	ip                string
}

type fakeTask struct {
	polls int
	exit  string
}

type fakePVE struct {
	mu        sync.Mutex
	srv       *httptest.Server
	nextID    int
	guests    map[int]*fakeGuest
	tasks     map[string]*fakeTask
	taskN     int
	calls     []call
	failTasks map[string]string // task type -> exitstatus
	authSeen  []string
}

func newFakePVE(t *testing.T) *fakePVE {
	f := &fakePVE{
		nextID: 100,
		guests: map[int]*fakeGuest{
			testTmpl: {typ: "qemu", name: "ubuntu-2404-ci", status: "stopped", template: true,
				config: map[string]string{"scsi0": "local-lvm:base-9000-disk-0,size=3584M"}},
			7000: {typ: "qemu", name: "not-a-template", status: "running", config: map[string]string{}},
		},
		tasks:     map[string]*fakeTask{},
		failTasks: map[string]string{},
	}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePVE) fingerprint() string {
	sum := sha256.Sum256(f.srv.Certificate().Raw)
	return hex.EncodeToString(sum[:])
}

func (f *fakePVE) callsTo(method, suffix string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.Method == method && strings.HasSuffix(c.Path, suffix) {
			out = append(out, c)
		}
	}
	return out
}

func reply(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

// fail mimics Proxmox: the message is the HTTP reason phrase, the body is {"data":null}.
func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, msg, code)
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	body := `{"data":null}`
	fmt.Fprintf(buf, "HTTP/1.1 %d %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", code, msg, len(body), body)
	_ = buf.Flush()
}

func (f *fakePVE) task(typ string, vmid int) string {
	f.taskN++
	upid := fmt.Sprintf("UPID:%s:%08X:0000ABCD:%08X:%s:%d:%s:", testNode, f.taskN, f.taskN, typ, vmid, testTokenID)
	exit := "OK"
	if e, ok := f.failTasks[typ]; ok {
		exit = e
	}
	f.tasks[upid] = &fakeTask{exit: exit}
	return upid
}

func (f *fakePVE) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	form := r.URL.Query()
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		form, _ = url.ParseQuery(string(raw))
	}
	path := strings.TrimPrefix(r.URL.Path, "/api2/json")
	f.calls = append(f.calls, call{r.Method, path, string(raw), form})
	f.authSeen = append(f.authSeen, r.Header.Get("Authorization"))

	if r.Header.Get("Authorization") != "PVEAPIToken="+testTokenID+"="+testSecret {
		fail(w, 401, "authentication failure")
		return
	}
	seg := strings.Split(strings.Trim(path, "/"), "/")
	key := r.Method + " " + path
	switch {
	case key == "GET /version":
		reply(w, map[string]string{"version": "8.2.4", "release": "8.2"})
	case key == "GET /nodes":
		reply(w, []map[string]string{{"node": "pve2", "status": "offline"}, {"node": testNode, "status": "online"}})
	case key == "GET /cluster/nextid":
		id := f.nextID
		f.nextID++
		reply(w, strconv.Itoa(id)) // Proxmox answers a string
	case len(seg) < 2 || seg[0] != "nodes" || seg[1] != testNode:
		fail(w, 500, "no such node")
	case len(seg) == 3 && seg[2] == "status":
		reply(w, map[string]any{"uptime": 1})
	case len(seg) == 5 && seg[2] == "tasks" && seg[4] == "status":
		t, ok := f.tasks[seg[3]]
		if !ok {
			fail(w, 500, "no such task")
			return
		}
		t.polls++
		if t.polls < 2 {
			reply(w, map[string]string{"status": "running"})
			return
		}
		reply(w, map[string]string{"status": "stopped", "exitstatus": t.exit})
	case key == "GET /nodes/"+testNode+"/qemu":
		var list []map[string]any
		for id, g := range f.guests {
			if g.typ == "qemu" {
				e := map[string]any{"vmid": id, "name": g.name, "status": g.status}
				if g.template {
					e["template"] = 1
				}
				list = append(list, e)
			}
		}
		reply(w, list)
	case len(seg) == 5 && seg[2] == "storage" && seg[4] == "content":
		if form.Get("content") != "vztmpl" {
			fail(w, 400, "want content=vztmpl")
			return
		}
		switch seg[3] {
		case "local":
			reply(w, []map[string]any{{"volid": "local:vztmpl/ubuntu-24.04-standard_24.04-2_amd64.tar.zst", "content": "vztmpl"}})
		case "local-lvm":
			reply(w, []any{})
		default:
			fail(w, 500, "storage '"+seg[3]+"' does not exist")
		}
	case key == "POST /nodes/"+testNode+"/lxc":
		id, _ := strconv.Atoi(form.Get("vmid"))
		status := "stopped"
		if form.Get("start") == "1" {
			status = "running"
		}
		cfg := map[string]string{}
		for k := range form {
			cfg[k] = form.Get(k)
		}
		f.guests[id] = &fakeGuest{typ: "lxc", name: form.Get("hostname"), status: status, config: cfg, ip: "10.10.10.61"}
		reply(w, f.task("vzcreate", id))
	default:
		f.serveGuest(w, r.Method, seg, form)
	}
}

// serveGuest handles /nodes/{node}/{qemu|lxc}/{vmid}[/...].
func (f *fakePVE) serveGuest(w http.ResponseWriter, method string, seg []string, form url.Values) {
	if len(seg) < 4 || (seg[2] != "qemu" && seg[2] != "lxc") {
		fail(w, 501, "not implemented in fake")
		return
	}
	id, _ := strconv.Atoi(seg[3])
	g, ok := f.guests[id]
	if !ok || g.typ != seg[2] {
		fail(w, 500, fmt.Sprintf("Configuration file 'nodes/%s/%s/%d.conf' does not exist", testNode, seg[2], id))
		return
	}
	rest := strings.Join(seg[4:], "/")
	switch method + " " + rest {
	case "POST clone":
		newID, _ := strconv.Atoi(form.Get("newid"))
		f.guests[newID] = &fakeGuest{typ: "qemu", name: form.Get("name"), status: "stopped",
			config: map[string]string{"scsi0": strings.Replace(g.config["scsi0"], "base-9000", fmt.Sprintf("vm-%d", newID), 1)}}
		reply(w, f.task("qmclone", id))
	case "POST config":
		for k := range form {
			g.config[k] = form.Get(k)
		}
		reply(w, f.task("qmconfig", id))
	case "GET config":
		reply(w, g.config)
	case "PUT resize":
		g.config["scsi0"] = strings.Split(g.config["scsi0"], ",")[0] + ",size=" + form.Get("size")
		reply(w, f.task("resize", id))
	case "GET status/current":
		reply(w, map[string]any{"status": g.status, "name": g.name, "vmid": id})
	case "POST status/start", "POST status/reboot":
		g.status = "running"
		reply(w, f.task(seg[2]+"start", id))
	case "POST status/stop", "POST status/shutdown":
		g.status = "stopped"
		reply(w, f.task(seg[2]+"stop", id))
	case "GET interfaces":
		reply(w, []map[string]string{
			{"name": "lo", "inet": "127.0.0.1/8", "inet6": "::1/128"},
			{"name": "eth0", "inet": g.ip + "/24", "inet6": "fe80::be24:11ff:fe00:1/64"},
		})
	case "GET agent/network-get-interfaces":
		if !g.agent {
			fail(w, 500, "QEMU guest agent is not running")
			return
		}
		reply(w, map[string]any{"result": []map[string]any{
			{"name": "lo", "ip-addresses": []map[string]any{{"ip-address": "127.0.0.1", "ip-address-type": "ipv4"}}},
			{"name": "docker0", "ip-addresses": []map[string]any{{"ip-address": "172.17.0.1", "ip-address-type": "ipv4"}}},
			{"name": "eth0", "ip-addresses": []map[string]any{
				{"ip-address": "fe80::1", "ip-address-type": "ipv6"},
				{"ip-address": g.ip, "ip-address-type": "ipv4"},
				{"ip-address": "2001:db8::10", "ip-address-type": "ipv6"},
			}},
		}})
	case "DELETE ":
		if g.status == "running" {
			fail(w, 500, "VM is running - destroy failed")
			return
		}
		delete(f.guests, id)
		reply(w, f.task(seg[2]+"destroy", id))
	default:
		fail(w, 501, "not implemented in fake: "+method+" "+rest)
	}
}

// ---- helpers ---------------------------------------------------------------------------------

func testCred(f *fakePVE, extra map[string]string) providers.Credential {
	cfg := map[string]string{
		"api_url":         f.srv.URL,
		"token_id":        testTokenID,
		"node":            testNode,
		"storage":         "local-lvm",
		"template_vmid":   strconv.Itoa(testTmpl),
		"lxc_template":    "local:vztmpl/ubuntu-24.04-standard_24.04-2_amd64.tar.zst",
		"tls_fingerprint": f.fingerprint(),
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return providers.Credential{Config: cfg, Secret: testSecret}
}

func newTestDriver(t *testing.T, f *fakePVE, extra map[string]string) *Driver {
	t.Helper()
	d, err := New(testCred(f, extra))
	if err != nil {
		t.Fatal(err)
	}
	d.c.poll = 5 * time.Millisecond
	return d
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// ---- tests -----------------------------------------------------------------------------------

func TestRegisteredAndCapabilities(t *testing.T) {
	f := newFakePVE(t)
	p, err := providers.New("proxmox", testCred(f, nil))
	if err != nil {
		t.Fatal(err)
	}
	if p.Key() != "proxmox" || p.Capabilities() != (providers.Caps{Create: true, Power: true}) {
		t.Fatalf("key/caps = %s %+v", p.Key(), p.Capabilities())
	}
	if _, err := providers.New("proxmox", providers.Credential{Config: map[string]string{}}); err == nil {
		t.Fatal("empty credential accepted")
	}
}

func TestValidateAuthHeader(t *testing.T) {
	f := newFakePVE(t)
	d := newTestDriver(t, f, nil)
	if err := d.Validate(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	want := "PVEAPIToken=hosbah@pve!ctl=" + testSecret
	for _, h := range f.authSeen {
		if h != want {
			t.Fatalf("Authorization = %q, want %q", h, want)
		}
	}
	if len(f.callsTo("GET", "/nodes/pve1/status")) != 1 || len(f.callsTo("GET", "/version")) != 1 {
		t.Fatal("Validate did not call /version and /nodes/pve1/status")
	}
	bad := newTestDriver(t, f, map[string]string{"node": "ghost"})
	if err := bad.Validate(ctxT(t)); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("unknown node: %v", err)
	}
}

func TestUnauthorizedIsClearAndSecretFree(t *testing.T) {
	f := newFakePVE(t)
	cred := testCred(f, nil)
	cred.Secret = "wrong-secret-must-not-leak"
	d, err := New(cred)
	if err != nil {
		t.Fatal(err)
	}
	err = d.Validate(ctxT(t))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
	if !strings.Contains(err.Error(), "refused or lacks permission") || strings.Contains(err.Error(), cred.Secret) {
		t.Fatalf("error text: %v", err)
	}
	if _, err := d.Create(ctxT(t), providers.CreateSpec{Kind: "lxc"}); err == nil || strings.Contains(err.Error(), cred.Secret) {
		t.Fatalf("create error: %v", err)
	}
	if got := d.c.scrub(errors.New("echo " + cred.Secret)).Error(); strings.Contains(got, cred.Secret) {
		t.Fatalf("scrub leaked: %s", got)
	}
}

func TestFingerprintPinning(t *testing.T) {
	f := newFakePVE(t)
	// Upper case with colons is accepted.
	var colon []string
	for i := 0; i < len(f.fingerprint()); i += 2 {
		colon = append(colon, strings.ToUpper(f.fingerprint()[i:i+2]))
	}
	if err := newTestDriver(t, f, map[string]string{"tls_fingerprint": strings.Join(colon, ":")}).Validate(ctxT(t)); err != nil {
		t.Fatalf("correct fingerprint refused: %v", err)
	}
	wrong := strings.Repeat("ab", 32)
	err := newTestDriver(t, f, map[string]string{"tls_fingerprint": wrong}).Validate(ctxT(t))
	if err == nil || !strings.Contains(err.Error(), "does not match the pinned") {
		t.Fatalf("wrong fingerprint: %v", err)
	}
	// No pin and no insecure_tls: the self-signed cert fails normal verification.
	if err := newTestDriver(t, f, map[string]string{"tls_fingerprint": ""}).Validate(ctxT(t)); err == nil {
		t.Fatal("self-signed certificate accepted without a pin")
	}
	if err := newTestDriver(t, f, map[string]string{"tls_fingerprint": "", "insecure_tls": "true"}).Validate(ctxT(t)); err != nil {
		t.Fatalf("insecure_tls: %v", err)
	}
	for _, bad := range []map[string]string{
		{"tls_fingerprint": "zz"},
		{"insecure_tls": "true"}, // together with the fingerprint from testCred
		{"api_url": "http://pve:8006"},
		{"token_id": "hosbah"},
	} {
		if _, err := New(testCred(f, bad)); err == nil {
			t.Errorf("config %v accepted", bad)
		}
	}
}

func TestCreateVMWireEncodingWithoutSnippets(t *testing.T) {
	f := newFakePVE(t)
	d := newTestDriver(t, f, map[string]string{"pool": "hosbah", "bridge": "vmbr1"})
	if d.DeliversUserData("vm") || d.DeliversUserData("lxc") {
		t.Fatal("DeliversUserData true without a snippet store")
	}
	keys := []string{"ssh-ed25519 AAAAC3Nz+aC/1lZDI1NTE5AAAA= hosbah@ctl", "ssh-rsa AAAAB3+Nza/C1yc2E= fady laptop"}
	id, err := d.Create(ctxT(t), providers.CreateSpec{
		Name: "Web 01!", Kind: "vm", Size: "medium", SSHPublicKeys: keys,
		UserData: "#cloud-config\n", Tags: []string{"Team-A", "bad tag!"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "pve1/qemu/100" {
		t.Fatalf("id = %q", id)
	}
	clone := f.callsTo("POST", "/qemu/9000/clone")
	if len(clone) != 1 {
		t.Fatal("no clone call")
	}
	for k, v := range map[string]string{"newid": "100", "name": "web-01", "full": "1", "storage": "local-lvm", "pool": "hosbah"} {
		if got := clone[0].Form.Get(k); got != v {
			t.Errorf("clone %s = %q, want %q", k, got, v)
		}
	}
	conf := f.callsTo("POST", "/qemu/100/config")
	if len(conf) != 1 {
		t.Fatal("no config call")
	}
	c := conf[0]
	for k, v := range map[string]string{"cores": "2", "memory": "2048", "ciuser": "hosbah", "ipconfig0": "ip=dhcp", "net0": "virtio,bridge=vmbr1", "tags": "team-a;badtag"} {
		if got := c.Form.Get(k); got != v {
			t.Errorf("config %s = %q, want %q", k, got, v)
		}
	}
	if c.Form.Has("cicustom") {
		t.Error("cicustom set without a snippet store")
	}
	// Wire encoding: the value is percent-encoded (space %20, '+' %2B, newline %0A) and then
	// form-encoded, so '%' itself travels as %25.
	joined := strings.Join(keys, "\n")
	inner := strings.ReplaceAll(url.QueryEscape(joined), "+", "%20")
	if !strings.Contains(c.RawBody, "sshkeys="+url.QueryEscape(inner)) {
		t.Fatalf("raw body sshkeys encoding wrong: %s", c.RawBody)
	}
	for _, frag := range []string{"ssh-ed25519%2520AAAAC3Nz%252BaC%252F1lZDI1NTE5AAAA%253D", "%250Assh-rsa"} {
		if !strings.Contains(c.RawBody, frag) {
			t.Errorf("raw body lacks %q", frag)
		}
	}
	// What Proxmox does: form-decode (done by ParseQuery), then uri_unescape (%XX only).
	got, err := url.PathUnescape(c.Form.Get("sshkeys"))
	if err != nil || got != joined {
		t.Fatalf("PVE-side decode = %q, %v; want %q", got, err, joined)
	}
	resize := f.callsTo("PUT", "/qemu/100/resize")
	if len(resize) != 1 || resize[0].Form.Get("disk") != "scsi0" || resize[0].Form.Get("size") != "20G" {
		t.Fatalf("resize calls = %+v", resize)
	}
	if len(f.callsTo("POST", "/qemu/100/status/start")) != 1 {
		t.Fatal("VM not started")
	}
}

func TestCreateVMSkipsShrinkAndDefaultsBridge(t *testing.T) {
	f := newFakePVE(t)
	d := newTestDriver(t, f, nil)
	// A 3.5 GB template disk grown to 2 GB would be a shrink: no resize call.
	if _, err := d.Create(ctxT(t), providers.CreateSpec{Name: "x", Image: "vm:9000", Options: map[string]string{"disk_gb": "2", "ipconfig0": "ip=10.10.10.50/24,gw=10.10.10.1"}}); err != nil {
		t.Fatal(err)
	}
	if len(f.callsTo("PUT", "/resize")) != 0 {
		t.Fatal("resize attempted to shrink")
	}
	c := f.callsTo("POST", "/qemu/100/config")[0]
	if c.Form.Has("net0") || c.Form.Has("sshkeys") || c.Form.Get("ipconfig0") != "ip=10.10.10.50/24,gw=10.10.10.1" {
		t.Fatalf("config form = %v", c.Form)
	}
}

func TestCreateVMWithSnippetStore(t *testing.T) {
	f := newFakePVE(t)
	dir := t.TempDir()
	d := newTestDriver(t, f, map[string]string{"snippets_storage": "snip", "snippets_dir": dir})
	if !d.DeliversUserData("vm") || d.DeliversUserData("lxc") {
		t.Fatal("DeliversUserData wrong with a store")
	}
	ud := providers.AgentUserData("curl -fsSL https://x/install/tok | sh", []string{"ssh-ed25519 AAAA k"})
	id, err := d.Create(ctxT(t), providers.CreateSpec{Name: "vm1", Kind: "vm", UserData: ud})
	if err != nil {
		t.Fatal(err)
	}
	c := f.callsTo("POST", "/qemu/100/config")[0]
	if got := c.Form.Get("cicustom"); got != "user=snip:snippets/hosbah-100.yaml" {
		t.Fatalf("cicustom = %q", got)
	}
	b, err := os.ReadFile(filepath.Join(dir, "hosbah-100.yaml"))
	if err != nil || string(b) != ud {
		t.Fatalf("snippet = %q, %v", b, err)
	}
	// A guest created before the rename has a cloudy-<vmid>.yaml snippet: Delete removes that too.
	if err := os.WriteFile(filepath.Join(dir, "cloudy-100.yaml"), []byte(ud), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(ctxT(t), id); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hosbah-100.yaml", "cloudy-100.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("snippet %s not removed on Delete", name)
		}
	}
	if _, err := New(testCred(f, map[string]string{"snippets_storage": "snip"})); err == nil {
		t.Fatal("snippets_storage without snippets_dir accepted")
	}
}

func TestCreateLXC(t *testing.T) {
	f := newFakePVE(t)
	d := newTestDriver(t, f, map[string]string{"pool": "hosbah"})
	keys := []string{"ssh-ed25519 AAAA+/= one", "ssh-ed25519 BBBB two"}
	id, err := d.Create(ctxT(t), providers.CreateSpec{
		Name: "db box", Kind: "lxc", Size: "large", SSHPublicKeys: keys,
		Options: map[string]string{"ipconfig0": "ip=10.10.10.61/24,gw=10.10.10.1", "memory_mb": "3000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "pve1/lxc/100" {
		t.Fatalf("id = %q", id)
	}
	cr := f.callsTo("POST", "/nodes/pve1/lxc")
	if len(cr) != 1 {
		t.Fatal("no lxc create")
	}
	want := map[string]string{
		"vmid": "100", "hostname": "db-box", "ostemplate": "local:vztmpl/ubuntu-24.04-standard_24.04-2_amd64.tar.zst",
		"rootfs": "local-lvm:40", "cores": "4", "memory": "3000", "swap": "512", "unprivileged": "1",
		"features": "nesting=1,keyctl=1", "net0": "name=eth0,bridge=vmbr0,ip=10.10.10.61/24,gw=10.10.10.1",
		"ssh-public-keys": "ssh-ed25519 AAAA+/= one\nssh-ed25519 BBBB two", "pool": "hosbah", "start": "1",
	}
	for k, v := range want {
		if got := cr[0].Form.Get(k); got != v {
			t.Errorf("lxc %s = %q, want %q", k, got, v)
		}
	}
	m, err := d.Get(ctxT(t), id)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "running" || m.IPv4 != "10.10.10.61" || m.PrivateIP != "10.10.10.61" || m.IPv6 != "" || m.Name != "db-box" {
		t.Fatalf("machine = %+v", m)
	}
}

func TestTaskFailureSurfacesAndCleansUp(t *testing.T) {
	f := newFakePVE(t)
	d := newTestDriver(t, f, nil)
	f.failTasks["qmconfig"] = "can't lock file '/var/lock/qemu-server/lock-100.conf' - got timeout"
	_, err := d.Create(ctxT(t), providers.CreateSpec{Kind: "vm"})
	var te *taskError
	if !errors.As(err, &te) || !strings.Contains(err.Error(), "got timeout") {
		t.Fatalf("got %v, want the task exit status", err)
	}
	f.mu.Lock()
	_, left := f.guests[100]
	f.mu.Unlock()
	if left {
		t.Fatal("half-created VM was not removed")
	}
}

func TestGetVMGuestAgent(t *testing.T) {
	f := newFakePVE(t)
	d := newTestDriver(t, f, nil)
	id, err := d.Create(ctxT(t), providers.CreateSpec{Kind: "vm"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := d.Get(ctxT(t), id)
	if err != nil || m.Status != "running" || m.IPv4 != "" {
		t.Fatalf("agent down: %+v, %v", m, err)
	}
	f.mu.Lock()
	f.guests[100].agent, f.guests[100].ip = true, "203.0.113.7"
	f.mu.Unlock()
	m, err = d.Get(ctxT(t), id)
	if err != nil || m.IPv4 != "203.0.113.7" || m.IPv6 != "2001:db8::10" || m.PrivateIP != "" {
		t.Fatalf("agent up: %+v, %v", m, err)
	}
	if err := d.Power(ctxT(t), id, providers.PowerStop); err != nil {
		t.Fatal(err)
	}
	if m, _ = d.Get(ctxT(t), id); m.Status != "stopped" {
		t.Fatalf("after stop: %+v", m)
	}
	if err := d.Power(ctxT(t), id, "explode"); err == nil {
		t.Fatal("unknown power action accepted")
	}
}

func TestDelete(t *testing.T) {
	f := newFakePVE(t)
	d := newTestDriver(t, f, nil)
	if err := d.Delete(ctxT(t), "pve1/qemu/4242"); err != nil {
		t.Fatalf("missing guest: %v", err)
	}
	id, err := d.Create(ctxT(t), providers.CreateSpec{Kind: "lxc"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(ctxT(t), id); err != nil {
		t.Fatal(err)
	}
	if len(f.callsTo("POST", "/lxc/100/status/stop")) != 1 {
		t.Fatal("running guest not stopped before destroy")
	}
	del := f.callsTo("DELETE", "/lxc/100")
	if len(del) != 1 || del[0].Form.Get("purge") != "1" || del[0].Form.Get("destroy-unreferenced-disks") != "1" {
		t.Fatalf("delete calls = %+v", del)
	}
	if err := d.Delete(ctxT(t), id); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	for _, bad := range []string{"", "pve1/kvm/1", "pve1/qemu/x", "/qemu/100"} {
		if err := d.Delete(ctxT(t), bad); err == nil {
			t.Errorf("id %q accepted", bad)
		}
	}
}

func TestRegionsSizesImagesKeys(t *testing.T) {
	f := newFakePVE(t)
	d := newTestDriver(t, f, nil)
	regions, err := d.Regions(ctxT(t))
	if err != nil || len(regions) != 2 || regions[0] != (providers.Region{ID: "pve1", Name: "pve1", Status: "online"}) {
		t.Fatalf("regions = %+v, %v", regions, err)
	}
	sz, _ := d.Sizes(ctxT(t), "")
	if len(sz) != 3 || sz[0].ID != "small" || sz[1] != (providers.Size{ID: "medium", Name: "Medium", CPUs: 2, MemoryMB: 2048, DiskGB: 20}) {
		t.Fatalf("sizes = %+v", sz)
	}
	imgs, err := d.Images(ctxT(t), "")
	if err != nil {
		t.Fatal(err)
	}
	want := []providers.Image{
		{ID: "vm:9000", Name: "ubuntu-2404-ci", Kind: "vm"},
		{ID: "lxc:local:vztmpl/ubuntu-24.04-standard_24.04-2_amd64.tar.zst", Name: "ubuntu-24.04-standard_24.04-2_amd64.tar.zst", Kind: "lxc"},
	}
	if fmt.Sprint(imgs) != fmt.Sprint(want) {
		t.Fatalf("images = %+v", imgs)
	}
	fp, err := d.EnsureSSHKey(ctxT(t), "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGXv8Kn2oS5LJ7g1JmGd6Sa5vbgUwvDDZkF7w8y1UO2k c")
	if err != nil || !strings.HasPrefix(fp, "SHA256:") {
		t.Fatalf("fingerprint = %q, %v", fp, err)
	}
	if _, err := d.EnsureSSHKey(ctxT(t), "not a key"); err == nil {
		t.Fatal("garbage key accepted")
	}
}

func TestCreateSpecValidation(t *testing.T) {
	f := newFakePVE(t)
	d := newTestDriver(t, f, map[string]string{"template_vmid": "", "lxc_template": ""})
	for _, s := range []providers.CreateSpec{
		{Kind: "vm"},                 // no template configured
		{Kind: "lxc"},                // no lxc template configured
		{Kind: "docker"},             // bad kind
		{Kind: "lxc", Image: "vm:1"}, // mismatch
		{Image: "ubuntu"},            // bad image ref
		{Kind: "lxc", Image: "lxc:local:vztmpl/x", Size: "huge"},
		{Kind: "lxc", Image: "lxc:local:vztmpl/x", Options: map[string]string{"cores": "0"}},
		{Kind: "lxc", Image: "lxc:local:vztmpl/x", Options: map[string]string{"ipconfig0": "ip=dhcp,bridge=vmbr9"}},
	} {
		if _, err := d.Create(ctxT(t), s); err == nil {
			t.Errorf("spec %+v accepted", s)
		}
	}
	if len(f.callsTo("GET", "/cluster/nextid")) != 0 {
		t.Fatal("invalid specs reached the API")
	}
}

func TestHelpers(t *testing.T) {
	for in, want := range map[string]string{"Web 01!": "web-01", "---": "hosbah-7", "9lives": "hosbah-7", strings.Repeat("a", 70): strings.Repeat("a", 63)} {
		if got := hostname(in, 7); got != want {
			t.Errorf("hostname(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]float64{"x:y,size=3584M": 3.5, "x,size=32G": 32, "x,size=1T": 1024} {
		if got, ok := diskSizeGB(in); !ok || got != want {
			t.Errorf("diskSizeGB(%q) = %v, %v", in, got, ok)
		}
	}
	if _, ok := diskSizeGB("x,iothread=1"); ok {
		t.Error("diskSizeGB without size= reported ok")
	}
}
