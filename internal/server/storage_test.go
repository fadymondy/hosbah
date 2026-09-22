package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

type volumeJSON struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	MountPath string    `json:"mount_path"`
	Volume    string    `json:"volume"`
	Mounted   bool      `json:"mounted"`
	Bytes     *int64    `json:"bytes"`
	CreatedAt time.Time `json:"created_at"`
	Applies   string    `json:"applies"`
}

type bucketJSON struct {
	ID        string   `json:"id"`
	ServiceID string   `json:"service_id"`
	Service   string   `json:"service"`
	Bucket    string   `json:"bucket"`
	AccessKey string   `json:"access_key"`
	EnvPrefix string   `json:"env_prefix"`
	Env       []string `json:"env"`
	Status    string   `json:"status"`
	Error     *string  `json:"error"`
	Applies   string   `json:"applies"`
	Operation string   `json:"operation"`
}

type storageJSON struct {
	Volumes        []volumeJSON `json:"volumes"`
	Buckets        []bucketJSON `json:"buckets"`
	UsageOperation *string      `json:"usage_operation"`
}

/*
"Storage" end to end with a fake agent: what a mount path must be, the derived volume name, the
mounts in the signed start params of web AND workers (a restart through the real pipeline), the
mounted flag, best-effort usage, delete_data refused while mounted; a bucket with its secret sealed
at rest, sealed to the agent in the operation and nowhere in plaintext, env collisions, removal
(not empty, force), roles, other teams, imported apps, MCP and the cascade.
*/
func TestAppStorage(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	ctx := context.Background()

	owner := newClient(t, ts.URL)
	owner.register("stor-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Storage " + suffix}, 201, &team)
	var readTok, writeTok struct{ Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "stor-read", "scopes": []string{"read"}, "expires_in_days": 30}, 201, &readTok)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "stor-write", "scopes": []string{"read", "write"}, "expires_in_days": 30}, 201, &writeTok)

	viewerEmail := "stor-viewer-" + suffix + "@example.test"
	viewer := newClient(t, ts.URL)
	viewer.register(viewerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	stranger := newClient(t, ts.URL)
	stranger.register("stor-stranger-" + suffix + "@example.test")
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "Elsewhere " + suffix}, 201, nil)

	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "stor-"+short, "203.0.113.31", []agentproto.Service{
		{Kind: "container", Name: "imp-stor-" + short, Image: "nginx:1.27", Status: "running", Ports: []string{"0.0.0.0:8083->80/tcp"}}})
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "stor-" + short, "server": srv, "repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	appBase := func(id string) string { return "/api/teams/" + team.ID + "/apps/" + id }
	base := appBase(app.App.ID)
	volName := func(slug string) string { return "hosbah-vol-" + app.App.ID + "-" + slug }

	// The fake agent. bucket.remove answers what the test scripted; volume.usage can be switched off.
	var (
		mu           sync.Mutex
		removeErr    string
		usageSilent  bool
		usageAnswers = map[string]agentproto.VolumeUsage{}
	)
	agent := startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		mu.Lock()
		defer mu.Unlock()
		switch op.Kind {
		case agentproto.KindDBProvision:
			var p agentproto.DBProvisionParams
			_ = json.Unmarshal(op.Params, &p)
			return agentproto.DBProvisionResult{Container: p.Container, Volume: p.Volume, Host: "172.17.0.1", Port: p.Port}, ""
		case agentproto.KindAppRollback:
			var p agentproto.RollbackParams
			_ = json.Unmarshal(op.Params, &p)
			if err := p.Web.Validate(); err != nil {
				return nil, err.Error()
			}
			res := agentproto.RollbackResult{Web: agentproto.StartResult{Container: "hosbah-stor-web-1", HostPort: 40123}}
			for i, w := range p.Workers {
				if err := w.Validate(); err != nil {
					return nil, err.Error()
				}
				res.Workers = append(res.Workers, agentproto.StartResult{Container: fmt.Sprintf("hosbah-stor-worker-%d", i)})
			}
			return res, ""
		case agentproto.KindVolumeUsage:
			if usageSilent {
				return nil, "docker is not answering"
			}
			var p agentproto.VolumeUsageParams
			_ = json.Unmarshal(op.Params, &p)
			res := agentproto.VolumeUsageResult{}
			for _, v := range p.Volumes {
				u := usageAnswers[v]
				u.Volume = v
				res.Volumes = append(res.Volumes, u)
			}
			return res, ""
		case agentproto.KindVolumeRemove:
			var p agentproto.VolumeRemoveParams
			_ = json.Unmarshal(op.Params, &p)
			return agentproto.VolumeRemoveResult{Volume: p.Volume, Removed: true}, ""
		case agentproto.KindBucketCreate:
			var p agentproto.BucketCreateParams
			_ = json.Unmarshal(op.Params, &p)
			if err := p.Validate(); err != nil {
				return nil, err.Error()
			}
			return agentproto.BucketCreateResult{Bucket: p.Bucket, AccessKey: p.AccessKey}, ""
		case agentproto.KindBucketRemove:
			var p agentproto.BucketRemoveParams
			_ = json.Unmarshal(op.Params, &p)
			if removeErr != "" && !p.Force {
				return nil, removeErr
			}
			return agentproto.BucketRemoveResult{Bucket: p.Bucket, KeyRevoked: true, BucketRemoved: p.DeleteData}, ""
		}
		return nil, "this fake agent does not run " + op.Kind
	})

	storage := func(c *client, id string) storageJSON {
		t.Helper()
		var s storageJSON
		c.mustJSON("GET", appBase(id)+"/storage", nil, 200, &s)
		return s
	}
	volumeOf := func(s storageJSON, name string) volumeJSON {
		t.Helper()
		for _, v := range s.Volumes {
			if v.Name == name {
				return v
			}
		}
		t.Fatalf("no volume %s in %+v", name, s.Volumes)
		return volumeJSON{}
	}

	t.Run("what a mount path and a name must be", func(t *testing.T) {
		if s := storage(viewer, app.App.ID); s.Volumes == nil || s.Buckets == nil || len(s.Volumes) != 0 || len(s.Buckets) != 0 || s.UsageOperation != nil {
			t.Fatalf("empty storage is two empty lists and no operation: %+v", s)
		}
		bad := map[string]map[string]any{
			"relative":        {"name": "x", "mount_path": "data"},
			"the root":        {"name": "x", "mount_path": "/"},
			"root, spelled":   {"name": "x", "mount_path": "/./"},
			"traversal":       {"name": "x", "mount_path": "/data/../etc"},
			"traversal to /":  {"name": "x", "mount_path": "/data/.."},
			"docker socket":   {"name": "x", "mount_path": "/var/run/docker.sock"},
			"a comma":         {"name": "x", "mount_path": "/data,readonly"},
			"a space":         {"name": "x", "mount_path": "/my data"},
			"a colon":         {"name": "x", "mount_path": "/data:ro"},
			"too long":        {"name": "x", "mount_path": "/" + strings.Repeat("a", 200)},
			"upper-case name": {"name": "Data", "mount_path": "/data"},
			"digit first":     {"name": "1data", "mount_path": "/data"},
			"name too long":   {"name": "a" + strings.Repeat("b", 31), "mount_path": "/data"},
			"underscore name": {"name": "my_data", "mount_path": "/data"},
		}
		for _, root := range agentproto.ForbiddenMountRoots {
			bad["at "+root] = map[string]any{"name": "x", "mount_path": root}
			bad["under "+root] = map[string]any{"name": "x", "mount_path": root + "/app"}
		}
		for name, body := range bad {
			if status, text := owner.do("POST", base+"/storage/volumes", body); status != 422 {
				t.Errorf("%s: %d (want 422): %s", name, status, text)
			}
		}
		if n := len(storage(owner, app.App.ID).Volumes); n != 0 {
			t.Fatalf("a refused volume leaves nothing: %d", n)
		}
	})

	var data volumeJSON
	t.Run("a volume: derived name, normalised path, overlaps, roles, other teams", func(t *testing.T) {
		viewer.mustJSON("POST", base+"/storage/volumes", map[string]any{"name": "data", "mount_path": "/data"}, 403, nil)
		stranger.mustJSON("POST", base+"/storage/volumes", map[string]any{"name": "data", "mount_path": "/data"}, 404, nil)
		stranger.mustJSON("GET", base+"/storage", nil, 404, nil)

		owner.mustJSON("POST", base+"/storage/volumes", map[string]any{"name": "data", "mount_path": "/srv//app/./data/"}, 201, &data)
		if data.Name != "data" || data.MountPath != "/srv/app/data" || data.Volume != volName("data") || data.Mounted || data.Applies != "next_deploy" || data.Bytes != nil {
			t.Fatalf("created: %+v", data)
		}
		for name, body := range map[string]map[string]any{
			"the same name":        {"name": "data", "mount_path": "/other"},
			"the same path":        {"name": "other", "mount_path": "/srv/app/data"},
			"inside another":       {"name": "other", "mount_path": "/srv/app/data/cache"},
			"around another":       {"name": "other", "mount_path": "/srv/app"},
			"around, by traversal": {"name": "other", "mount_path": "/srv/app/x/.."},
		} {
			if status, text := owner.do("POST", base+"/storage/volumes", body); status != 409 {
				t.Errorf("%s: %d (want 409): %s", name, status, text)
			}
		}
		// A sibling with a common prefix is not an overlap.
		owner.mustJSON("POST", base+"/storage/volumes", map[string]any{"name": "data-2", "mount_path": "/srv/app/data2"}, 201, nil)

		viewer.mustJSON("DELETE", base+"/storage/volumes/data", map[string]any{"confirm": "data"}, 403, nil)
		stranger.mustJSON("DELETE", base+"/storage/volumes/"+data.ID, map[string]any{"confirm": "data"}, 404, nil)
		owner.mustJSON("DELETE", base+"/storage/volumes/data", map[string]any{"confirm": "nope"}, 422, nil)
		owner.mustJSON("DELETE", base+"/storage/volumes/nope", map[string]any{"confirm": "nope"}, 404, nil)
	})

	t.Run("an imported app has no storage", func(t *testing.T) {
		apps, _ := listImported(t, owner, team.ID, srv)
		imp, ok := apps["imp-stor-"+short]
		if !ok {
			t.Fatalf("no imported app: %+v", apps)
		}
		owner.mustJSON("POST", appBase(imp.ID)+"/storage/volumes", map[string]any{"name": "data", "mount_path": "/data"}, 422, nil)
		owner.mustJSON("POST", appBase(imp.ID)+"/storage/buckets", map[string]any{"service": "files", "bucket": "imported-bucket"}, 422, nil)
		if s := storage(owner, imp.ID); len(s.Volumes) != 0 || len(s.Buckets) != 0 {
			t.Fatalf("imported app's storage: %+v", s)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "create_app_volume", map[string]any{"app": imp.ID, "name": "data", "mount_path": "/data"}); !res.IsError {
			t.Errorf("MCP create_app_volume on an imported app: %+v", res)
		}
	})

	t.Run("the cap is 10", func(t *testing.T) {
		for i := 3; i <= 10; i++ {
			owner.mustJSON("POST", base+"/storage/volumes", map[string]any{"name": fmt.Sprintf("v%d", i), "mount_path": fmt.Sprintf("/mnt/v%d", i)}, 201, nil)
		}
		owner.mustJSON("POST", base+"/storage/volumes", map[string]any{"name": "v11", "mount_path": "/mnt/v11"}, 409, nil)
		for i := 3; i <= 10; i++ {
			owner.mustJSON("DELETE", base+"/storage/volumes/"+fmt.Sprintf("v%d", i), map[string]any{"confirm": fmt.Sprintf("v%d", i)}, 200, nil)
		}
	})

	t.Run("a restart mounts the volumes into web and every worker, and the release remembers", func(t *testing.T) {
		seedLiveRelease(t, team.ID, app.App.ID, srv, app.App.Name)
		err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true, TeamID: team.ID}, func(_ *db.Queries, tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE releases SET config = $2 WHERE app_id = $1 AND status = 'live'`, app.App.ID,
				`{"port":8080,"health_path":"/","health_timeout":30,"processes":{"web":"","worker:jobs":"bin/jobs","worker:mail":"bin/mail"}}`)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if v := volumeOf(storage(owner, app.App.ID), "data"); v.Mounted {
			t.Fatalf("a live release that was started without the volume does not mount it: %+v", v)
		}

		var dep struct {
			ID string `json:"id"`
		}
		owner.mustJSON("POST", base+"/restart", nil, 201, &dep)
		deadline := time.Now().Add(60 * time.Second)
		for {
			var d struct {
				Deployment struct {
					Status string  `json:"status"`
					Error  *string `json:"error"`
				} `json:"deployment"`
			}
			owner.mustJSON("GET", base+"/deployments/"+dep.ID, nil, 200, &d)
			if d.Deployment.Status == "succeeded" {
				break
			}
			if d.Deployment.Status == "failed" || time.Now().After(deadline) {
				t.Fatalf("the restart: %+v", d.Deployment)
			}
			time.Sleep(150 * time.Millisecond)
		}
		rb := agentOps(agent, agentproto.KindAppRollback)
		if len(rb) != 1 {
			t.Fatalf("one app.rollback, got %d", len(rb))
		}
		var p agentproto.RollbackParams
		if err := json.Unmarshal(rb[0].Params, &p); err != nil {
			t.Fatal(err)
		}
		want := []agentproto.Mount{{Volume: volName("data"), MountPath: "/srv/app/data"}, {Volume: volName("data-2"), MountPath: "/srv/app/data2"}}
		if len(p.Workers) != 2 {
			t.Fatalf("two workers: %+v", p.Workers)
		}
		for _, sp := range append([]agentproto.StartParams{p.Web}, p.Workers...) {
			if fmt.Sprint(sp.Mounts) != fmt.Sprint(want) {
				t.Errorf("%s mounts %v, want %v", sp.Process, sp.Mounts, want)
			}
		}
		s := storage(owner, app.App.ID)
		if !volumeOf(s, "data").Mounted || !volumeOf(s, "data-2").Mounted {
			t.Fatalf("after the restart both are mounted: %+v", s.Volumes)
		}
		// A volume added now is not mounted until the next one.
		owner.mustJSON("POST", base+"/storage/volumes", map[string]any{"name": "late", "mount_path": "/late"}, 201, nil)
		if v := volumeOf(storage(owner, app.App.ID), "late"); v.Mounted {
			t.Fatalf("late: %+v", v)
		}
	})

	t.Run("usage is best effort", func(t *testing.T) {
		n := int64(1_500_000)
		mu.Lock()
		usageAnswers[volName("data")] = agentproto.VolumeUsage{Exists: true, Bytes: &n}
		usageAnswers[volName("data-2")] = agentproto.VolumeUsage{Exists: true}
		mu.Unlock()
		s := storage(viewer, app.App.ID)
		if s.UsageOperation == nil {
			t.Fatalf("a connected server is asked: %+v", s)
		}
		if v := volumeOf(s, "data"); v.Bytes == nil || *v.Bytes != n {
			t.Errorf("data: %+v", v)
		}
		if v := volumeOf(s, "data-2"); v.Bytes != nil {
			t.Errorf("Docker reported no size: %+v", v)
		}
		if v := volumeOf(s, "late"); v.Bytes != nil {
			t.Errorf("a volume Docker has not created: %+v", v)
		}
		ops := agentOps(agent, agentproto.KindVolumeUsage)
		var p agentproto.VolumeUsageParams
		_ = json.Unmarshal(ops[len(ops)-1].Params, &p)
		if p.AppID != app.App.ID || len(p.Volumes) != 3 {
			t.Errorf("volume.usage params: %+v", p)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "get_app_storage", map[string]any{"app": app.App.Name}); res.IsError ||
			!strings.Contains(res.Text, "1500000 bytes") || !strings.Contains(res.Text, volName("data")) || !strings.Contains(res.Text, "mounted") {
			t.Errorf("MCP get_app_storage: %+v", res)
		}

		mu.Lock()
		usageSilent = true
		mu.Unlock()
		if v := volumeOf(storage(owner, app.App.ID), "data"); v.Bytes != nil || !v.Mounted {
			t.Errorf("a failed usage operation is still 200 with null bytes: %+v", v)
		}
	})

	t.Run("delete_data is refused while the live release mounts the volume", func(t *testing.T) {
		before := len(agentOps(agent, agentproto.KindVolumeRemove))
		if status, text := owner.do("DELETE", base+"/storage/volumes/data-2", map[string]any{"confirm": "data-2", "delete_data": true}); status != 409 || !strings.Contains(text, "deploy or restart") {
			t.Fatalf("mounted + delete_data: %d %s", status, text)
		}
		if volumeOf(storage(owner, app.App.ID), "data-2").ID == "" || len(agentOps(agent, agentproto.KindVolumeRemove)) != before {
			t.Fatal("a refused removal changes nothing")
		}
		// Not mounted: the record goes and the agent removes the derived volume.
		var out struct {
			Removed    bool    `json:"removed"`
			Applies    string  `json:"applies"`
			DeleteData bool    `json:"delete_data"`
			Operation  *string `json:"operation"`
		}
		owner.mustJSON("DELETE", base+"/storage/volumes/late", map[string]any{"confirm": "late", "delete_data": true}, 200, &out)
		if !out.Removed || !out.DeleteData || out.Operation == nil || out.Applies != "next_deploy" {
			t.Fatalf("removed: %+v", out)
		}
		deadline := time.Now().Add(15 * time.Second)
		for len(agentOps(agent, agentproto.KindVolumeRemove)) == before && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		rm := agentOps(agent, agentproto.KindVolumeRemove)
		if len(rm) != before+1 {
			t.Fatal("volume.remove never reached the agent")
		}
		var p agentproto.VolumeRemoveParams
		_ = json.Unmarshal(rm[len(rm)-1].Params, &p)
		if p.AppID != app.App.ID || p.Volume != volName("late") {
			t.Errorf("volume.remove params: %+v", p)
		}
		// Without delete_data a mounted volume's record goes; the data stays (no operation).
		owner.mustJSON("DELETE", base+"/storage/volumes/data-2", map[string]any{"confirm": "data-2"}, 200, &out)
		if !out.Removed || out.DeleteData || out.Operation != nil || len(agentOps(agent, agentproto.KindVolumeRemove)) != before+1 {
			t.Fatalf("record only: %+v", out)
		}
		// MCP: confirm is required, like REST.
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "create_app_volume", map[string]any{"app": app.App.ID, "name": "mcp-vol", "mount_path": "/mcp"}); res.IsError {
			t.Fatalf("MCP create_app_volume: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "remove_app_volume", map[string]any{"app": app.App.ID, "volume": "mcp-vol", "confirm": "mcp-vol"}); !res.IsError {
			t.Errorf("a read token must not remove: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "remove_app_volume", map[string]any{"app": app.App.ID, "volume": "mcp-vol"}); !res.IsError || !strings.Contains(res.Text, "confirm") {
			t.Errorf("MCP remove_app_volume without confirm: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "remove_app_volume", map[string]any{"app": app.App.ID, "volume": "mcp-vol", "confirm": "mcp-vol"}); res.IsError {
			t.Errorf("MCP remove_app_volume: %+v", res)
		}
	})

	// ---- buckets -----------------------------------------------------------------------------------

	var minio struct{ ID, Name, Status string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/data-services", map[string]any{"name": "files-" + short, "server": srv, "kind": "minio"}, 201, &minio)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/data-services", map[string]any{"name": "cache-" + short, "server": srv, "kind": "redis"}, 201, nil)
	for deadline := time.Now().Add(30 * time.Second); ; {
		var d struct {
			Service struct{ Status string } `json:"service"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/data-services/"+minio.ID, nil, 200, &d)
		if d.Service.Status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the MinIO service never ran: %+v", d)
		}
		time.Sleep(100 * time.Millisecond)
	}
	bucketName := "media-" + short
	waitBucket := func(ref, want string) bucketJSON {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			for _, b := range storage(owner, app.App.ID).Buckets {
				if (b.ID == ref || b.Bucket == ref) && b.Status == want {
					return b
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("bucket %s never became %s: %+v", ref, want, storage(owner, app.App.ID).Buckets)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	envNames := func() map[string]bool {
		t.Helper()
		var env struct {
			Variables []struct{ Name string } `json:"variables"`
		}
		owner.mustJSON("GET", base+"/env", nil, 200, &env)
		out := map[string]bool{}
		for _, v := range env.Variables {
			out[v.Name] = true
		}
		return out
	}
	reveal := func(name string) string {
		t.Helper()
		var out struct{ Value string }
		owner.mustJSON("POST", base+"/env/"+name+"/reveal", nil, 200, &out)
		return out.Value
	}

	t.Run("what a bucket must be", func(t *testing.T) {
		for name, body := range map[string]map[string]any{
			"upper case":         {"service": minio.ID, "bucket": "Media"},
			"an underscore":      {"service": minio.ID, "bucket": "my_media"},
			"a dot":              {"service": minio.ID, "bucket": "my.media"},
			"ends with a dash":   {"service": minio.ID, "bucket": "media-"},
			"punycode":           {"service": minio.ID, "bucket": "xn--media"},
			"a bad prefix":       {"service": minio.ID, "bucket": "okay-bucket", "env_prefix": "s3"},
			"a prefix with dash": {"service": minio.ID, "bucket": "okay-bucket", "env_prefix": "MY-S3"},
			"not a MinIO":        {"service": "cache-" + short, "bucket": "okay-bucket"},
		} {
			if status, text := owner.do("POST", base+"/storage/buckets", body); status != 422 {
				t.Errorf("%s: %d (want 422): %s", name, status, text)
			}
		}
		owner.mustJSON("POST", base+"/storage/buckets", map[string]any{"service": "no-such-service", "bucket": "okay-bucket"}, 404, nil)
		viewer.mustJSON("POST", base+"/storage/buckets", map[string]any{"service": minio.ID, "bucket": bucketName}, 403, nil)
		stranger.mustJSON("POST", base+"/storage/buckets", map[string]any{"service": minio.ID, "bucket": bucketName}, 404, nil)
		if s := storage(owner, app.App.ID); len(s.Buckets) != 0 || len(envNames()) != 0 {
			t.Fatalf("refused buckets leave nothing: %+v %v", s.Buckets, envNames())
		}
	})

	var secret string
	t.Run("a bucket: the secret is sealed at rest, sealed to the agent in the operation, and nowhere else", func(t *testing.T) {
		var created bucketJSON
		status, text := owner.do("POST", base+"/storage/buckets", map[string]any{"service": minio.Name, "bucket": bucketName})
		if status != 201 {
			t.Fatalf("create: %d %s", status, text)
		}
		_ = json.Unmarshal([]byte(text), &created)
		wantEnv := []string{"S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY", "S3_REGION", "S3_USE_PATH_STYLE"}
		if created.Bucket != bucketName || created.EnvPrefix != "S3" || fmt.Sprint(created.Env) != fmt.Sprint(wantEnv) || created.Applies != "next_deploy" ||
			created.Operation == "" || created.ServiceID != minio.ID || created.AccessKey == "" {
			t.Fatalf("created: %+v", created)
		}
		ready := waitBucket(created.ID, "ready")
		names := envNames()
		for _, n := range wantEnv {
			if !names[n] {
				t.Errorf("%s was not set on the app", n)
			}
		}
		secret = reveal("S3_SECRET_ACCESS_KEY")
		if len(secret) < 32 || reveal("S3_ACCESS_KEY_ID") != ready.AccessKey || reveal("S3_BUCKET") != bucketName || reveal("S3_REGION") != "us-east-1" ||
			reveal("S3_USE_PATH_STYLE") != "true" || !strings.HasPrefix(reveal("S3_ENDPOINT"), "http://172.17.0.1:") {
			t.Fatalf("the variables: secret %d chars, endpoint %s", len(secret), reveal("S3_ENDPOINT"))
		}
		// No response carries it.
		if strings.Contains(text, secret) {
			t.Error("the create response carries the secret")
		}
		if _, body := owner.do("GET", base+"/storage", nil); strings.Contains(body, secret) || strings.Contains(body, "secret_sealed") {
			t.Errorf("GET storage carries the secret: %s", body)
		}
		// The rows: sealed in app_buckets; operations.params and .result hold no plaintext.
		var sealed, params string
		var result sql.NullString
		err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
			if err := tx.QueryRow(`SELECT secret_sealed FROM app_buckets WHERE id = $1`, created.ID).Scan(&sealed); err != nil {
				return err
			}
			return tx.QueryRow(`SELECT params::text, result::text FROM operations WHERE id = $1`, created.Operation).Scan(&params, &result)
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(sealed, "csv1:") || strings.Contains(sealed, secret) {
			t.Errorf("app_buckets.secret_sealed is not sealed: %.20s…", sealed)
		}
		if strings.Contains(params, secret) || strings.Contains(result.String, secret) {
			t.Error("operations.params or .result holds the secret in plaintext")
		}
		// The operation's log, as a member reads it.
		if _, body := owner.do("GET", "/api/teams/"+team.ID+"/servers/"+srv+"/operations/"+created.Operation, nil); strings.Contains(body, secret) {
			t.Error("the operation (params, result or log) shows the secret")
		}
		// What the agent got: a box only its key opens, bound to this service and bucket — and it is the app's secret.
		ops := agentOps(agent, agentproto.KindBucketCreate)
		if len(ops) != 1 {
			t.Fatalf("one bucket.create, got %d", len(ops))
		}
		var p agentproto.BucketCreateParams
		_ = json.Unmarshal(ops[0].Params, &p)
		if p.Bucket != bucketName || p.AccessKey != ready.AccessKey || p.Policy != "hosbah-bucket-"+ready.AccessKey || p.ServiceID != minio.ID ||
			p.Container != "hosbah-db-files-"+short || strings.Contains(string(ops[0].Params), secret) {
			t.Fatalf("bucket.create params: %+v", p)
		}
		pt, err := agentproto.OpenBox(priv, agentproto.DBSecretContext(minio.ID, "bucket:"+bucketName), p.Secret)
		if err != nil || !strings.Contains(string(pt), `"`+secret+`"`) {
			t.Fatalf("the agent's box does not carry the app's secret: %v", err)
		}
		if _, err := agentproto.OpenBox(priv, agentproto.DBSecretContext(minio.ID, "bucket:other"), p.Secret); err == nil {
			t.Error("the box opens for another bucket")
		}
		if _, err := agentproto.OpenBox(priv, agentproto.DBSecretContext(minio.ID, "root"), p.RootSecret); err != nil {
			t.Errorf("the root box: %v", err)
		}
	})

	t.Run("collisions: bucket, env names, cap", func(t *testing.T) {
		owner.mustJSON("POST", base+"/storage/buckets", map[string]any{"service": minio.ID, "bucket": bucketName, "env_prefix": "OTHER"}, 409, nil)
		// The default prefix is taken by the first bucket's variables.
		if status, text := owner.do("POST", base+"/storage/buckets", map[string]any{"service": minio.ID, "bucket": "second-" + short}); status != 409 || !strings.Contains(text, "S3_") {
			t.Fatalf("env collision: %d %s", status, text)
		}
		// A variable someone set by hand is never overwritten.
		owner.mustJSON("PUT", base+"/env/ASSETS_REGION", map[string]any{"value": "eu-west-1"}, 204, nil)
		if status, text := owner.do("POST", base+"/storage/buckets", map[string]any{"service": minio.ID, "bucket": "assets-" + short, "env_prefix": "ASSETS"}); status != 409 || !strings.Contains(text, "ASSETS_REGION") {
			t.Fatalf("hand-set variable: %d %s", status, text)
		}
		if reveal("ASSETS_REGION") != "eu-west-1" || envNames()["ASSETS_BUCKET"] {
			t.Fatal("a refused bucket must not touch the app's variables")
		}
		for i := 2; i <= 5; i++ {
			owner.mustJSON("POST", base+"/storage/buckets", map[string]any{"service": minio.ID, "bucket": fmt.Sprintf("b%d-%s", i, short), "env_prefix": fmt.Sprintf("B%d", i)}, 201, nil)
		}
		owner.mustJSON("POST", base+"/storage/buckets", map[string]any{"service": minio.ID, "bucket": "b6-" + short, "env_prefix": "B6"}, 409, nil)
		for i := 2; i <= 5; i++ {
			waitBucket(fmt.Sprintf("b%d-%s", i, short), "ready")
		}
		// A MinIO service that apps hold buckets on is not deleted.
		owner.mustJSON("DELETE", "/api/teams/"+team.ID+"/data-services/"+minio.ID, nil, 409, nil)
	})

	t.Run("removing a bucket", func(t *testing.T) {
		ref := base + "/storage/buckets/" + bucketName
		viewer.mustJSON("DELETE", ref, map[string]any{"confirm": bucketName}, 403, nil)
		stranger.mustJSON("DELETE", ref, map[string]any{"confirm": bucketName}, 404, nil)
		owner.mustJSON("DELETE", ref, map[string]any{"confirm": "nope"}, 422, nil)
		owner.mustJSON("DELETE", ref, map[string]any{"confirm": bucketName, "force": true}, 422, nil)
		owner.mustJSON("DELETE", base+"/storage/buckets/nope", map[string]any{"confirm": "nope"}, 404, nil)

		mu.Lock()
		removeErr = agentproto.ErrBucketNotEmpty + " — nothing was changed; empty it, or remove it with force"
		mu.Unlock()
		if status, text := owner.do("DELETE", ref, map[string]any{"confirm": bucketName, "delete_data": true}); status != 409 || !strings.Contains(text, "not empty") {
			t.Fatalf("not empty: %d %s", status, text)
		}
		b := waitBucket(bucketName, "ready")
		if b.Error == nil || !envNames()["S3_SECRET_ACCESS_KEY"] || reveal("S3_SECRET_ACCESS_KEY") != secret {
			t.Fatalf("a refused removal leaves the bucket and its variables: %+v", b)
		}

		var out struct {
			Removed    bool     `json:"removed"`
			DeleteData bool     `json:"delete_data"`
			Unset      []string `json:"unset"`
			Operation  *string  `json:"operation"`
		}
		owner.mustJSON("DELETE", ref, map[string]any{"confirm": bucketName, "delete_data": true, "force": true}, 200, &out)
		if !out.Removed || !out.DeleteData || len(out.Unset) != 6 || out.Operation == nil {
			t.Fatalf("removed: %+v", out)
		}
		rm := agentOps(agent, agentproto.KindBucketRemove)
		var p agentproto.BucketRemoveParams
		_ = json.Unmarshal(rm[len(rm)-1].Params, &p)
		if p.Bucket != bucketName || !p.DeleteData || !p.Force || p.AccessKey != b.AccessKey || p.Policy != "hosbah-bucket-"+b.AccessKey {
			t.Errorf("bucket.remove params: %+v", p)
		}
		names := envNames()
		for _, n := range out.Unset {
			if names[n] {
				t.Errorf("%s is still set", n)
			}
		}
		if !names["ASSETS_REGION"] || !names["B2_BUCKET"] {
			t.Errorf("only the bucket's own variables go: %v", names)
		}
		for _, k := range storage(owner, app.App.ID).Buckets {
			if k.Bucket == bucketName {
				t.Errorf("the bucket is still listed: %+v", k)
			}
		}

		// MCP: create and remove, with confirm.
		mu.Lock()
		removeErr = ""
		mu.Unlock()
		mcpBucket := "mcp-" + short
		if res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "create_app_bucket", map[string]any{"app": app.App.ID, "service": minio.Name, "bucket": mcpBucket}); !res.IsError {
			t.Errorf("a read token must not create: %+v", res)
		}
		res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "create_app_bucket", map[string]any{"app": app.App.ID, "service": minio.Name, "bucket": mcpBucket})
		if res.IsError || !strings.Contains(res.Text, "S3_SECRET_ACCESS_KEY") || strings.Contains(res.Text, reveal("S3_SECRET_ACCESS_KEY")) {
			t.Fatalf("MCP create_app_bucket: %+v", res)
		}
		waitBucket(mcpBucket, "ready")
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "remove_app_bucket", map[string]any{"app": app.App.ID, "bucket": mcpBucket}); !res.IsError || !strings.Contains(res.Text, "confirm") {
			t.Errorf("MCP remove_app_bucket without confirm: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "remove_app_bucket", map[string]any{"app": app.App.ID, "bucket": mcpBucket, "confirm": mcpBucket}); res.IsError || !strings.Contains(res.Text, "revoked") {
			t.Errorf("MCP remove_app_bucket: %+v", res)
		}
		if envNames()["S3_BUCKET"] {
			t.Error("MCP removal left the variables")
		}
	})

	t.Run("audited", func(t *testing.T) {
		var audit struct {
			Entries []struct {
				Action  string          `json:"action"`
				Changes json.RawMessage `json:"changes"`
			} `json:"entries"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/audit?limit=200", nil, 200, &audit)
		got := map[string]int{}
		for _, e := range audit.Entries {
			got[e.Action]++
			if strings.Contains(string(e.Changes), secret) {
				t.Errorf("the audit log carries the secret: %s", e.Action)
			}
			if e.Action == "storage.volume_removed" && !strings.Contains(string(e.Changes), "delete_data") {
				t.Errorf("volume_removed without delete_data: %s", e.Changes)
			}
		}
		for _, a := range []string{"storage.volume_created", "storage.volume_removed", "storage.bucket_created", "storage.bucket_removed"} {
			if got[a] == 0 {
				t.Errorf("no %s in the audit log: %v", a, got)
			}
		}
	})

	t.Run("volumes and buckets go with their app", func(t *testing.T) {
		count := func() (v, b int) {
			t.Helper()
			err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
				if err := tx.QueryRow(`SELECT count(*) FROM app_volumes WHERE app_id = $1`, app.App.ID).Scan(&v); err != nil {
					return err
				}
				return tx.QueryRow(`SELECT count(*) FROM app_buckets WHERE app_id = $1`, app.App.ID).Scan(&b)
			})
			if err != nil {
				t.Fatal(err)
			}
			return v, b
		}
		if v, b := count(); v != 1 || b != 4 {
			t.Fatalf("before: %d volumes, %d buckets", v, b)
		}
		owner.mustJSON("DELETE", base, map[string]any{"confirm": app.App.Name}, 204, nil)
		if v, b := count(); v != 0 || b != 0 {
			t.Fatalf("after deleting the app: %d volumes, %d buckets", v, b)
		}
		// And the MinIO service, free of app buckets, can go.
		owner.mustJSON("DELETE", "/api/teams/"+team.ID+"/data-services/"+minio.ID, nil, 204, nil)
	})
}
