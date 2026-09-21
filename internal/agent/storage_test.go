package agent

import (
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

const (
	stAppID = "00000000-0000-4000-8000-0000000000a1"
	stSvcID = "00000000-0000-4000-8000-0000000000d1"
	stDepID = "00000000-0000-4000-8000-0000000000e1"
)

func stStart(process string, mounts ...agentproto.Mount) agentproto.StartParams {
	p := agentproto.StartParams{AppID: stAppID, App: "shop", DeploymentID: stDepID, Image: "cloudy/shop:" + stDepID, Process: process, Mounts: mounts}
	if process == "web" {
		p.Port = 8080
	}
	return p
}

// The docker argv of a release container carries the app's volumes — web and workers alike — before
// the image, as one --mount element each.
func TestStartArgsMountTheVolumesIntoWebAndWorkers(t *testing.T) {
	data := agentproto.Mount{Volume: agentproto.VolumeName(stAppID, "data"), MountPath: "/data"}
	up := agentproto.Mount{Volume: agentproto.VolumeName(stAppID, "uploads"), MountPath: "/srv/app/uploads"}
	if data.Volume != "cloudy-vol-"+stAppID+"-data" {
		t.Fatalf("derived volume name: %s", data.Volume)
	}
	for _, process := range []string{"web", "worker:jobs"} {
		p := stStart(process, data, up)
		if process != "web" {
			p.Command = "bin/jobs"
		}
		if err := p.Validate(); err != nil {
			t.Fatalf("%s: %v", process, err)
		}
		args, _ := startArgs(p, "c1", map[string]string{"A": "1"})
		image := -1
		for i, a := range args {
			if a == p.Image {
				image = i
			}
		}
		for _, m := range []agentproto.Mount{data, up} {
			want, found := "type=volume,source="+m.Volume+",target="+m.MountPath, false
			for i, a := range args {
				if a == want && i > 0 && args[i-1] == "--mount" && i < image {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: %q is not a --mount before the image in %v", process, want, args)
			}
		}
	}
	if args, _ := startArgs(stStart("web"), "c1", nil); strings.Contains(strings.Join(args, " "), "--mount") {
		t.Errorf("an app without volumes gets no --mount: %v", args)
	}
}

// The agent validates what it is told to mount with the control plane's own rules.
func TestStartParamsRefuseBadMounts(t *testing.T) {
	own := agentproto.VolumeName(stAppID, "data")
	for name, mounts := range map[string][]agentproto.Mount{
		"another app's volume":   {{Volume: agentproto.VolumeName("00000000-0000-4000-8000-0000000000a2", "data"), MountPath: "/data"}},
		"a host path":            {{Volume: "/var/lib/docker", MountPath: "/data"}},
		"a free volume name":     {{Volume: "cloudy-dbdata-main", MountPath: "/data"}},
		"an option in the name":  {{Volume: own + ",readonly", MountPath: "/data"}},
		"an option in the path":  {{Volume: own, MountPath: "/data,volume-opt=o=bind"}},
		"a relative path":        {{Volume: own, MountPath: "data"}},
		"the root":               {{Volume: own, MountPath: "/"}},
		"not normalised":         {{Volume: own, MountPath: "/data/"}},
		"traversal":              {{Volume: own, MountPath: "/data/../etc"}},
		"under /etc":             {{Volume: own, MountPath: "/etc/app"}},
		"the docker socket":      {{Volume: own, MountPath: "/var/run/docker.sock"}},
		"the same volume twice":  {{Volume: own, MountPath: "/a"}, {Volume: own, MountPath: "/b"}},
		"a mount inside another": {{Volume: own, MountPath: "/data"}, {Volume: agentproto.VolumeName(stAppID, "cache"), MountPath: "/data/cache"}},
	} {
		if err := stStart("web", mounts...).Validate(); err == nil {
			t.Errorf("%s: accepted %v", name, mounts)
		}
	}
	for _, root := range agentproto.ForbiddenMountRoots {
		for _, p := range []string{root, root + "/x"} {
			if _, err := agentproto.CleanMountPath(p); err == nil {
				t.Errorf("%s must be refused", p)
			}
		}
	}
	for _, ok := range []string{"/data", "/srv/app/storage", "/var/lib/app", "/runtime", "/etcetera", "/opt/my_app-1.2"} {
		if got, err := agentproto.CleanMountPath(ok); err != nil || got != ok {
			t.Errorf("%s must be allowed: %q %v", ok, got, err)
		}
	}
	if got, err := agentproto.CleanMountPath("/data//x/./y/"); err != nil || got != "/data/x/y" {
		t.Errorf("normalising: %q %v", got, err)
	}
	if _, err := agentproto.CleanMountPath("/" + strings.Repeat("a", 200)); err == nil {
		t.Error("a path over 200 characters must be refused")
	}
}

func newStorageFake(answers map[string]string) (*storageHost, *fakeRunner) {
	f := &fakeRunner{answers: answers, fail: map[string]string{}}
	return &storageHost{run: f.run}, f
}

func TestVolumeUsageReadsDockerAndNeverRunsAContainer(t *testing.T) {
	data, cache, fresh := agentproto.VolumeName(stAppID, "data"), agentproto.VolumeName(stAppID, "cache"), agentproto.VolumeName(stAppID, "fresh")
	h, f := newStorageFake(map[string]string{
		"docker system df -v --format json": `{"Images":[],"Containers":[],"Volumes":[{"Name":"` + data + `","Links":"1","Size":"1.5MB"},` +
			`{"Name":"` + cache + `","Links":"0","Size":"N/A"},{"Name":"someone-elses","Size":"9GB"}],"BuildCache":[]}`,
		"docker volume inspect --format {{.Name}} " + data:  data + "\n",
		"docker volume inspect --format {{.Name}} " + cache: cache + "\n",
	})
	f.fail["docker volume inspect --format {{.Name}} "+fresh] = "Error response from daemon: get " + fresh + ": no such volume"
	oc, _ := opCtx(t, agentproto.VolumeUsageParams{AppID: stAppID, Volumes: []string{data, cache, fresh}})
	res, _, err := h.volumeUsage(oc)
	if err != nil {
		t.Fatal(err)
	}
	got := res.(agentproto.VolumeUsageResult).Volumes
	if len(got) != 3 || !got[0].Exists || got[0].Bytes == nil || *got[0].Bytes != 1_500_000 {
		t.Fatalf("data: %+v", got)
	}
	if !got[1].Exists || got[1].Bytes != nil {
		t.Errorf("a size Docker does not report is null: %+v", got[1])
	}
	if got[2].Exists || got[2].Bytes != nil {
		t.Errorf("a volume that does not exist yet: %+v", got[2])
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c.argv, "docker run") || strings.HasPrefix(c.argv, "docker exec") {
			t.Errorf("usage must not start anything: %s", c.argv)
		}
	}
	oc, _ = opCtx(t, agentproto.VolumeUsageParams{AppID: stAppID, Volumes: []string{"cloudy-dbdata-main"}})
	if _, code, err := h.volumeUsage(oc); err == nil || code != 2 {
		t.Errorf("a volume that is not the app's must be refused: %d %v", code, err)
	}
}

func TestVolumeRemoveFailsCleanlyWhileInUse(t *testing.T) {
	data := agentproto.VolumeName(stAppID, "data")
	h, f := newStorageFake(nil)
	f.fail["docker volume rm "+data] = "Error response from daemon: remove " + data + ": volume is in use - [4f2a]"
	oc, _ := opCtx(t, agentproto.VolumeRemoveParams{AppID: stAppID, Volume: data})
	if _, code, err := h.volumeRemove(oc); err == nil || code != 1 || !strings.Contains(err.Error(), "still used by a container") {
		t.Fatalf("in use: %d %v", code, err)
	}
	for _, c := range f.calls {
		if strings.Contains(c.argv, " -f") || strings.Contains(c.argv, "--force") {
			t.Errorf("never forced: %s", c.argv)
		}
	}

	h, f = newStorageFake(nil)
	oc, _ = opCtx(t, agentproto.VolumeRemoveParams{AppID: stAppID, Volume: data})
	res, _, err := h.volumeRemove(oc)
	if err != nil || !res.(agentproto.VolumeRemoveResult).Removed || len(f.calls) != 1 || f.calls[0].argv != "docker volume rm "+data {
		t.Fatalf("remove: %+v %v %+v", res, err, f.calls)
	}

	h, f = newStorageFake(nil)
	f.fail["docker volume rm "+data] = "Error response from daemon: get " + data + ": no such volume"
	oc, _ = opCtx(t, agentproto.VolumeRemoveParams{AppID: stAppID, Volume: data})
	if res, _, err := h.volumeRemove(oc); err != nil || res.(agentproto.VolumeRemoveResult).Removed {
		t.Fatalf("already gone is fine: %+v %v", res, err)
	}

	oc, _ = opCtx(t, agentproto.VolumeRemoveParams{AppID: stAppID, Volume: "cloudy-dbdata-main"})
	if _, code, _ := h.volumeRemove(oc); code != 2 {
		t.Error("a volume that is not the app's must be refused")
	}
}

func bucketBoxes(t *testing.T, bucket string) (ed25519.PrivateKey, string, string) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	root, err := agentproto.SealBox(pubB64(pub), agentproto.DBSecretContext(stSvcID, "root"), []byte(`{"password":"r00t-pw-marker"}`))
	if err != nil {
		t.Fatal(err)
	}
	key, err := agentproto.SealBox(pubB64(pub), agentproto.DBSecretContext(stSvcID, agentproto.BucketSecretPurpose(bucket)), []byte(`{"secret":"key-secret-marker-0123456789"}`))
	if err != nil {
		t.Fatal(err)
	}
	return priv, root, key
}

const runningMinIO = "docker inspect --type container --format {{.State.Status}} cloudy-db-files"

func TestBucketCreateScopesTheKeyToOneBucketAndKeepsSecretsOffTheArgv(t *testing.T) {
	priv, root, key := bucketBoxes(t, "shop-media")
	h, f := newStorageFake(map[string]string{runningMinIO: "running\n"})
	p := agentproto.BucketCreateParams{ServiceID: stSvcID, Container: "cloudy-db-files", Bucket: "shop-media", AccessKey: "cbAbCdEfGh0123456789",
		Policy: agentproto.BucketPolicyName("cbAbCdEfGh0123456789"), Secret: key, RootSecret: root}
	oc, lines := opCtx(t, p)
	oc.priv = priv
	if _, _, err := h.bucketCreate(oc); err != nil {
		t.Fatal(err)
	}
	var argv []string
	sawSecret, sawPolicy := false, false
	for _, c := range f.calls {
		argv = append(argv, c.argv)
		if strings.Contains(c.argv, "marker") {
			t.Errorf("a secret is on an argv: %s", c.argv)
		}
		for _, e := range c.env {
			sawSecret = sawSecret || e == "S=key-secret-marker-0123456789"
			if strings.HasPrefix(e, "POLICY=") {
				sawPolicy = true
				var doc struct {
					Statement []struct {
						Resource []string
					}
				}
				if err := json.Unmarshal([]byte(strings.TrimPrefix(e, "POLICY=")), &doc); err != nil || len(doc.Statement) != 2 {
					t.Fatalf("policy: %v %s", err, e)
				}
				for _, st := range doc.Statement {
					for _, r := range st.Resource {
						if r != "arn:aws:s3:::shop-media" && r != "arn:aws:s3:::shop-media/*" {
							t.Errorf("the policy reaches %s", r)
						}
					}
				}
			}
		}
	}
	all := strings.Join(argv, "\n")
	for _, want := range []string{"mc mb --ignore-existing local/shop-media", "mc admin policy attach local cloudy-bucket-cbAbCdEfGh0123456789 --user cbAbCdEfGh0123456789",
		"-e MC_HOST_local", "-e S -e U"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in\n%s", want, all)
		}
	}
	if strings.Contains(all, "readwrite") {
		t.Errorf("the service-wide readwrite policy must not be used:\n%s", all)
	}
	if !sawSecret || !sawPolicy {
		t.Errorf("the secret and the policy travel in the environment: %v %v", sawSecret, sawPolicy)
	}
	if strings.Contains(strings.Join(*lines, "\n"), "marker") {
		t.Errorf("a secret is in the log: %v", *lines)
	}

	// A box sealed for another bucket does not open.
	p.Bucket = "other-bucket"
	oc, _ = opCtx(t, p)
	oc.priv = priv
	if _, _, err := h.bucketCreate(oc); err == nil {
		t.Error("a key box bound to another bucket must not open")
	}
	// A policy name that is not the key's own is refused.
	p.Bucket, p.Policy = "shop-media", "consoleAdmin"
	oc, _ = opCtx(t, p)
	oc.priv = priv
	if _, code, _ := h.bucketCreate(oc); code != 2 {
		t.Error("a free policy name must be refused")
	}
}

func TestBucketRemoveChecksEmptinessBeforeAnythingChanges(t *testing.T) {
	priv, root, _ := bucketBoxes(t, "shop-media")
	p := agentproto.BucketRemoveParams{ServiceID: stSvcID, Container: "cloudy-db-files", Bucket: "shop-media", AccessKey: "cbAbCdEfGh0123456789",
		Policy: agentproto.BucketPolicyName("cbAbCdEfGh0123456789"), DeleteData: true, RootSecret: root}
	ls := "docker exec -i -e MC_HOST_local cloudy-db-files mc ls local/shop-media"

	h, f := newStorageFake(map[string]string{runningMinIO: "running\n", ls: "[2026-09-01 10:00:00 UTC] 1.2MiB STANDARD logo.png\n"})
	oc, _ := opCtx(t, p)
	oc.priv = priv
	if _, _, err := h.bucketRemove(oc); err == nil || !strings.Contains(err.Error(), agentproto.ErrBucketNotEmpty) {
		t.Fatalf("not empty: %v", err)
	}
	for _, c := range f.calls {
		if strings.Contains(c.argv, " rm ") || strings.Contains(c.argv, " rb ") {
			t.Errorf("nothing may change before the refusal: %s", c.argv)
		}
	}

	run := func(p agentproto.BucketRemoveParams, answers map[string]string) (agentproto.BucketRemoveResult, string) {
		t.Helper()
		h, f := newStorageFake(answers)
		oc, _ := opCtx(t, p)
		oc.priv = priv
		res, _, err := h.bucketRemove(oc)
		if err != nil {
			t.Fatal(err)
		}
		var argv []string
		for _, c := range f.calls {
			argv = append(argv, c.argv)
		}
		return res.(agentproto.BucketRemoveResult), strings.Join(argv, "\n")
	}
	res, argv := run(p, map[string]string{runningMinIO: "running\n"}) // empty
	if !res.KeyRevoked || !res.BucketRemoved || !strings.Contains(argv, "mc rb local/shop-media") || strings.Contains(argv, "--force") {
		t.Errorf("empty bucket: %+v\n%s", res, argv)
	}
	p.Force = true
	res, argv = run(p, map[string]string{runningMinIO: "running\n", ls: "x\n"})
	if !res.BucketRemoved || !strings.Contains(argv, "mc rb --force local/shop-media") {
		t.Errorf("forced: %+v\n%s", res, argv)
	}
	p.Force, p.DeleteData = false, false
	res, argv = run(p, map[string]string{runningMinIO: "running\n", ls: "x\n"})
	if !res.KeyRevoked || res.BucketRemoved || strings.Contains(argv, " rb ") ||
		!strings.Contains(argv, "mc admin user rm local cbAbCdEfGh0123456789") || !strings.Contains(argv, "mc admin policy rm local cloudy-bucket-cbAbCdEfGh0123456789") {
		t.Errorf("revoke only: %+v\n%s", res, argv)
	}
	if strings.Contains(argv, "r00t-pw-marker") {
		t.Errorf("the root password is on an argv:\n%s", argv)
	}
}

func TestStorageLanes(t *testing.T) {
	lane := func(kind, params string) string {
		return laneOf(agentproto.SignedOp{Kind: kind, Params: json.RawMessage(params)})
	}
	if l := lane(agentproto.KindVolumeUsage, `{"app_id":"`+stAppID+`"}`); l != "" {
		t.Errorf("volume.usage is read-only and never queues: %q", l)
	}
	if l := lane(agentproto.KindVolumeRemove, `{"app_id":"`+stAppID+`"}`); l != "app:"+stAppID {
		t.Errorf("volume.remove shares the app's lane (never beside a deploy): %q", l)
	}
	for _, k := range []string{agentproto.KindBucketCreate, agentproto.KindBucketRemove} {
		if l := lane(k, `{"service_id":"`+stSvcID+`"}`); l != "db:"+stSvcID {
			t.Errorf("%s shares the service's lane: %q", k, l)
		}
	}
}
