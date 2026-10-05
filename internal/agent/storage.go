package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// A site's storage on the server: its Docker volumes (volume.usage, volume.remove) and its buckets
// on a MinIO data service (bucket.create, bucket.remove — mc INSIDE the MinIO container, the same
// path db.create takes). Every argv is fixed; the names in it are derived or validated, and every
// secret reaches mc through the environment of `docker exec -e NAME`, never an argv.

type storageHost struct {
	run runFunc
	// runIn is run with stdin (object.browse uploads); nil uses execRunIn.
	runIn func(ctx context.Context, env []string, stdin []byte, name string, args ...string) ([]byte, error)
}

func newStorageHost() *storageHost { return &storageHost{run: execRun} }

// ---- volume.usage ----------------------------------------------------------------------------------

// volumeUsage never starts a container to measure: existence is `docker volume inspect`, the size
// is whatever `docker system df -v` reports for the volume (nil when it reports none).
func (h *storageHost) volumeUsage(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.VolumeUsageParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	sizes := map[string]*int64{}
	if out, err := h.run(oc.Ctx, nil, "docker", "system", "df", "-v", "--format", "json"); err == nil {
		sizes = parseDfVolumes(out)
	} else {
		oc.Info("docker did not report disk usage: %v", err)
	}
	res := agentproto.VolumeUsageResult{Volumes: []agentproto.VolumeUsage{}}
	for _, v := range p.Volumes {
		// Reported under the name asked for; measured under the name it has here (cloudy-vol-… when it
		// was made before the rename).
		u := agentproto.VolumeUsage{Volume: v}
		name := resolveName(oc.Ctx, h.run, kindVolume, v)
		if out, err := h.run(oc.Ctx, nil, "docker", "volume", "inspect", "--format", "{{.Name}}", name); err == nil && strings.TrimSpace(string(out)) == name {
			u.Exists = true
			u.Bytes = sizes[name]
		}
		res.Volumes = append(res.Volumes, u)
	}
	return res, 0, nil
}

// parseDfVolumes reads the Volumes of `docker system df -v --format json`: one JSON document (or
// one per line, on older clients). Size is the CLI's rounded, decimal-unit text ("1.234GB"); "N/A"
// and anything unreadable is nil.
func parseDfVolumes(out []byte) map[string]*int64 {
	type vol struct {
		Name string `json:"Name"`
		Size string `json:"Size"`
	}
	sizes := map[string]*int64{}
	add := func(raw []byte) {
		var doc struct {
			Volumes []vol `json:"Volumes"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			return
		}
		for _, v := range doc.Volumes {
			if n, ok := parseDfSize(v.Size); ok {
				sizes[v.Name] = &n
			}
		}
	}
	add(out)
	if len(sizes) == 0 {
		for _, l := range strings.Split(string(out), "\n") {
			if l = strings.TrimSpace(l); strings.HasPrefix(l, "{") {
				add([]byte(l))
			}
		}
	}
	return sizes
}

// parseDfSize reads go-units' HumanSize: a number and B, kB, MB, GB, TB or PB (powers of 1000).
func parseDfSize(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		mult   float64
	}{{"PB", 1e15}, {"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"KB", 1e3}, {"B", 1}}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), 64)
			if err != nil || f < 0 {
				return 0, false
			}
			return int64(f * u.mult), true
		}
	}
	return 0, false
}

// ---- volume.remove ---------------------------------------------------------------------------------

// volumeRemove is `docker volume rm` without -f: Docker itself refuses while any container (running
// or stopped) uses the volume, and the operation fails with a clean message. Already gone is fine.
func (h *storageHost) volumeRemove(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.VolumeRemoveParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	res := agentproto.VolumeRemoveResult{Volume: p.Volume}
	p.Volume = resolveName(oc.Ctx, h.run, kindVolume, p.Volume)
	if _, err := h.run(oc.Ctx, nil, "docker", "volume", "rm", p.Volume); err != nil {
		low := strings.ToLower(err.Error())
		switch {
		case strings.Contains(low, "no such volume"):
			oc.Info("volume %s was already gone", p.Volume)
			return res, 0, nil
		case strings.Contains(low, "in use"):
			return nil, 1, errors.New("the volume is still used by a container — deploy or restart the app without it first; nothing was removed")
		}
		oc.Info("%v", err)
		return nil, 1, errors.New("could not remove the volume")
	}
	res.Removed = true
	oc.Info("removed volume %s and its data", p.Volume)
	return res, 0, nil
}

// ---- bucket.create / bucket.remove -----------------------------------------------------------------

func mcHost(rootpw string) string {
	return "MC_HOST_local=http://" + agentproto.MinIORootUser + ":" + rootpw + "@127.0.0.1:9000"
}

// mc runs one command in the MinIO container. env values travel as `-e NAME` (docker reads them
// from its own environment), so no secret is on an argv.
func (h *storageHost) mc(oc *OpContext, container string, env []string, argv ...string) (string, error) {
	full := append([]string{"exec", "-i"}, envArgs(mapEnv(env))...)
	full = append(append(full, container), argv...)
	out, err := h.run(oc.Ctx, env, "docker", full...)
	if err != nil {
		return string(out) + "\n" + err.Error(), err
	}
	return string(out), nil
}

// policyScript writes the policy document ($POLICY — not a secret) to a file mc can read, creates
// the policy $P from it and removes the file. A constant: nothing of the params is in its text.
const policyScript = `f="/tmp/$P.json"; printf '%s' "$POLICY" > "$f" && mc admin policy create local "$P" "$f"; rc=$?; rm -f "$f"; exit $rc`

// bucketCreate makes the bucket, a MinIO user (the access key) and a policy that reaches that one
// bucket, and attaches it. Unlike db.create's built-in readwrite policy, this key sees nothing else
// on the service. Re-running it is safe: every step tolerates "already there".
func (h *storageHost) bucketCreate(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.BucketCreateParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	root, err := oc.openSecret(p.ServiceID, "root", p.RootSecret)
	if err != nil {
		return nil, 1, err
	}
	key, err := oc.openSecret(p.ServiceID, agentproto.BucketSecretPurpose(p.Bucket), p.Secret)
	if err != nil {
		return nil, 1, err
	}
	if len(key["secret"]) < 8 || len(key["secret"]) > 40 || root["password"] == "" {
		return nil, 2, errors.New("a secret is malformed")
	}
	if !h.running(oc, p.Container) {
		return nil, 1, errors.New("the data service is not running")
	}
	host := mcHost(root["password"])
	oc.Info("creating bucket %q and an access key limited to it", p.Bucket)
	steps := []struct {
		env     []string
		argv    []string
		already string
	}{
		{[]string{host}, []string{"mc", "mb", "--ignore-existing", "local/" + p.Bucket}, ""},
		{[]string{host, "U=" + p.AccessKey, "S=" + key["secret"]}, []string{"sh", "-c", `mc admin user add local "$U" "$S"`}, ""},
		{[]string{host, "P=" + p.Policy, "POLICY=" + agentproto.BucketPolicy(p.Bucket)}, []string{"sh", "-c", policyScript}, ""},
		{[]string{host}, []string{"mc", "admin", "policy", "attach", "local", p.Policy, "--user", p.AccessKey}, "already in effect"},
	}
	for _, st := range steps {
		if out, err := h.mc(oc, p.Container, st.env, st.argv...); err != nil {
			if st.already != "" && strings.Contains(out, st.already) {
				continue
			}
			oc.Info("%s", scrub(clip(strings.TrimSpace(out), 600), root["password"], key["secret"]))
			return nil, 1, errors.New("could not create the bucket and its access key")
		}
	}
	return agentproto.BucketCreateResult{Bucket: p.Bucket, AccessKey: p.AccessKey}, 0, nil
}

// bucketRemove revokes the key, and with DeleteData removes the bucket. The emptiness check comes
// FIRST: a non-empty bucket without Force fails before anything has changed.
func (h *storageHost) bucketRemove(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.BucketRemoveParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	p.Container = resolveName(oc.Ctx, h.run, kindContainer, p.Container)
	root, err := oc.openSecret(p.ServiceID, "root", p.RootSecret)
	if err != nil {
		return nil, 1, err
	}
	if !h.running(oc, p.Container) {
		return nil, 1, errors.New("the data service is not running")
	}
	env := []string{mcHost(root["password"])}
	res := agentproto.BucketRemoveResult{Bucket: p.Bucket}
	gone := func(out string) bool {
		low := strings.ToLower(out)
		return strings.Contains(low, "does not exist") || strings.Contains(low, "not found") || strings.Contains(low, "no such")
	}
	if p.DeleteData && !p.Force {
		out, err := h.mc(oc, p.Container, env, "mc", "ls", "local/"+p.Bucket)
		if err != nil && !gone(out) {
			oc.Info("%s", scrub(clip(strings.TrimSpace(out), 600), root["password"]))
			return nil, 1, errors.New("could not look into the bucket; nothing was changed")
		}
		if err == nil && strings.TrimSpace(out) != "" {
			return nil, 1, errors.New(agentproto.ErrBucketNotEmpty + " — nothing was changed; empty it, or remove it with force")
		}
	}
	if out, err := h.mc(oc, p.Container, env, "mc", "admin", "user", "rm", "local", p.AccessKey); err != nil && !gone(out) {
		oc.Info("%s", scrub(clip(strings.TrimSpace(out), 600), root["password"]))
		return nil, 1, errors.New("could not revoke the access key")
	}
	res.KeyRevoked = true
	// A key made before the rename has a cloudy-bucket-… policy: both names go, whichever it is.
	for _, policy := range agentproto.NameCandidates(p.Policy) {
		if out, err := h.mc(oc, p.Container, env, "mc", "admin", "policy", "rm", "local", policy); err != nil && !gone(out) {
			oc.Info("the key is revoked; its policy %s could not be removed", policy)
		}
	}
	if p.DeleteData {
		argv := []string{"mc", "rb", "local/" + p.Bucket}
		if p.Force {
			argv = []string{"mc", "rb", "--force", "local/" + p.Bucket}
		}
		if out, err := h.mc(oc, p.Container, env, argv...); err != nil && !gone(out) {
			oc.Info("%s", scrub(clip(strings.TrimSpace(out), 600), root["password"]))
			return nil, 1, errors.New("the access key is revoked, but the bucket could not be removed")
		}
		res.BucketRemoved = true
	}
	oc.Info("revoked access key %s%s", p.AccessKey, map[bool]string{true: " and removed bucket " + p.Bucket, false: ""}[res.BucketRemoved])
	return res, 0, nil
}

func (h *storageHost) running(oc *OpContext, container string) bool {
	out, err := h.run(oc.Ctx, nil, "docker", "inspect", "--type", "container", "--format", "{{.State.Status}}", container)
	return err == nil && strings.TrimSpace(string(out)) == "running"
}

// scrub removes secrets from text before it is logged (mc echoes the alias URL in some errors).
func scrub(s string, secrets ...string) string {
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "***")
		}
	}
	return s
}
