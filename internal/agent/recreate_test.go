package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

const shopInspect = `[{
  "Id": "0123456789abcdef0123456789abcdef",
  "Name": "/shop-web",
  "Image": "sha256:aaaa",
  "State": {"Running": true, "Status": "running"},
  "Config": {
    "Hostname": "0123456789ab", "User": "", "WorkingDir": "/app",
    "Env": ["PATH=/usr/bin", "DATABASE_URL=postgres://u:secret@db/x"],
    "Cmd": ["node", "server.js"], "Entrypoint": ["docker-entrypoint.sh"],
    "Image": "ghcr.io/acme/shop:1.4",
    "Labels": {"com.docker.compose.service": "web", "tier": "front"},
    "Healthcheck": {"Test": ["CMD-SHELL", "curl -f http://localhost/ || exit 1"], "Interval": 30000000000}
  },
  "HostConfig": {
    "Binds": ["/srv/shop/uploads:/app/uploads:rw"],
    "NetworkMode": "shop_default",
    "PortBindings": {"80/tcp": [{"HostIp": "", "HostPort": "8081"}], "443/tcp": [{"HostIp": "127.0.0.1", "HostPort": "8443"}]},
    "RestartPolicy": {"Name": "unless-stopped", "MaximumRetryCount": 0},
    "LogConfig": {"Type": "json-file", "Config": {"max-size": "10m"}},
    "ShmSize": 67108864, "Memory": 536870912, "NanoCpus": 1500000000
  },
  "Mounts": [
    {"Type": "bind", "Source": "/srv/shop/uploads", "Destination": "/app/uploads", "RW": true},
    {"Type": "volume", "Name": "4f2c9e0d1a", "Destination": "/app/cache", "RW": true}
  ],
  "NetworkSettings": {"Networks": {
    "shop_default": {"Aliases": ["shop-web", "web", "0123456789ab"]},
    "monitoring": {"Aliases": null, "IPAMConfig": {"IPv4Address": "172.30.0.9"}}
  }}
}]`

func inspect(t *testing.T, raw string) inspected {
	t.Helper()
	var ins []inspected
	if err := json.Unmarshal([]byte(raw), &ins); err != nil || len(ins) != 1 {
		t.Fatalf("fixture: %v", err)
	}
	return ins[0]
}

func TestRecreatePlanReproducesTheContainer(t *testing.T) {
	plan, err := planRecreate(inspect(t, shopInspect), nil)
	if err != nil {
		t.Fatal(err)
	}
	create := strings.Join(plan.Create, " ")
	for _, want := range []string{
		"create --name shop-web",
		"-e DATABASE_URL -e PATH",
		"--label com.docker.compose.service=web --label tier=front",
		"--workdir /app",
		"--health-cmd curl -f http://localhost/ || exit 1 --health-interval 30s",
		"--publish 127.0.0.1:8443:443/tcp --publish 8081:80/tcp",
		"--volume /srv/shop/uploads:/app/uploads:rw",
		"--volume 4f2c9e0d1a:/app/cache",
		"--restart unless-stopped",
		"--memory 536870912", "--cpus 1.5",
		"--log-driver json-file --log-opt max-size=10m",
		"--network shop_default --network-alias web",
		"--entrypoint docker-entrypoint.sh ghcr.io/acme/shop:1.4 node server.js",
	} {
		if !strings.Contains(create, want) {
			t.Errorf("create argv lacks %q:\n%s", want, create)
		}
	}
	if strings.Contains(create, "secret") {
		t.Fatalf("an env value reached the argv: %s", create)
	}
	if strings.Contains(create, "--hostname") || strings.Contains(create, "--shm-size") {
		t.Fatalf("defaults must not be spelled out: %s", create)
	}
	if len(plan.Env) != 2 || plan.Env[0] != "DATABASE_URL=postgres://u:secret@db/x" {
		t.Fatalf("env: %v", plan.Env)
	}
	if len(plan.Connect) != 1 || strings.Join(plan.Connect[0], " ") != "network connect --ip 172.30.0.9 monitoring shop-web" {
		t.Fatalf("extra networks: %v", plan.Connect)
	}
}

func TestRecreateRefusesWhatItCannotReproduce(t *testing.T) {
	for name, patch := range map[string]func(*inspected){
		"an image id":    func(c *inspected) { c.Config.Image = "sha256:0123456789abcdef" },
		"volumes-from":   func(c *inspected) { c.HostConfig.VolumesFrom = []string{"other"} },
		"a link":         func(c *inspected) { c.HostConfig.Links = []string{"/db:/shop-web/db"} },
		"a shared netns": func(c *inspected) { c.HostConfig.NetworkMode = "container:vpn" },
		"an exec healthcheck": func(c *inspected) {
			c.Config.Healthcheck = &healthcheck{Test: []string{"CMD", "/bin/check"}}
		},
		"a cgroup parent": func(c *inspected) { c.HostConfig.CgroupParent = "/custom" },
	} {
		c := inspect(t, shopInspect)
		patch(&c)
		if _, err := planRecreate(c, nil); err == nil || !strings.Contains(err.Error(), "cannot be recreated exactly") {
			t.Errorf("%s: want a refusal, got %v", name, err)
		}
	}
	// The image's own exec-form healthcheck is inherited, not refused.
	c := inspect(t, shopInspect)
	hc := &healthcheck{Test: []string{"CMD", "/bin/check"}}
	c.Config.Healthcheck = hc
	if _, err := planRecreate(c, &healthcheck{Test: []string{"CMD", "/bin/check"}}); err != nil {
		t.Fatalf("the image's own healthcheck: %v", err)
	}
}

func TestRecreateSwapsAndCleansUp(t *testing.T) {
	w, f := newFake(map[string]string{
		"docker inspect --type container shop-web":                            shopInspect,
		"docker image inspect":                                                "null",
		"docker inspect --type container --format {{.State.Status}} shop-web": "running",
		"docker create": "newid\n",
	})
	oc, _ := opCtx(t, agentproto.RecreateParams{Name: "shop-web"})
	oc.progress = func(int, int) {}
	res, _, err := w.recreate(oc)
	if err != nil {
		t.Fatal(err)
	}
	got := f.mutating()
	want := []string{
		"docker pull ghcr.io/acme/shop:1.4",
		"docker rename shop-web shop-web-old-0123456789ab",
		"docker stop shop-web-old-0123456789ab",
		"docker create --name shop-web",
		"docker network connect --ip 172.30.0.9 monitoring shop-web",
		"docker start shop-web",
		"docker rm shop-web-old-0123456789ab",
	}
	if len(got) != len(want) {
		t.Fatalf("commands:\n%s", strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Fatalf("step %d: %q, want %q", i, got[i], want[i])
		}
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c.argv, "docker create") && (len(c.env) != 2 || strings.Contains(c.argv, "secret")) {
			t.Fatalf("the env must reach docker create through its environment: %+v", c)
		}
	}
	if r := res.(agentproto.RecreateResult); r.Image != "ghcr.io/acme/shop:1.4" || r.ContainerID != "newid" {
		t.Fatalf("result: %+v", r)
	}
}

func TestRecreateRestoresTheOldContainerOnFailure(t *testing.T) {
	w, f := newFake(map[string]string{
		"docker inspect --type container shop-web": shopInspect,
		"docker image inspect":                     "null",
	})
	f.fail["docker start shop-web"] = "port is already allocated"
	oc, _ := opCtx(t, agentproto.RecreateParams{Name: "shop-web"})
	if _, _, err := w.recreate(oc); err == nil || !strings.Contains(err.Error(), "the old container is back") {
		t.Fatalf("want a restored failure, got %v", err)
	}
	got := strings.Join(f.mutating(), "\n")
	for _, want := range []string{"docker rm -f shop-web", "docker rename shop-web-old-0123456789ab shop-web"} {
		if !strings.Contains(got, want) {
			t.Fatalf("restore lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "docker rm shop-web-old") {
		t.Fatalf("the old container must never be removed on failure:\n%s", got)
	}
	// A failed pull changes nothing.
	w, f = newFake(map[string]string{"docker inspect --type container shop-web": shopInspect, "docker image inspect": "null"})
	f.fail["docker pull"] = "manifest unknown"
	if _, _, err := w.recreate(oc); err == nil || strings.Contains(strings.Join(f.mutating(), " "), "rename") {
		t.Fatalf("a failed pull: %v %v", err, f.mutating())
	}
}
