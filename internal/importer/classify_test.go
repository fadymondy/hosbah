package importer

import (
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func TestClassify(t *testing.T) {
	type want struct {
		key, target, kind, version, host string
		port                             int
	}
	cases := []struct {
		name string
		in   agentproto.Service
		want *want // nil = skipped
	}{
		// Hosbah's own workloads.
		{"managed db", agentproto.Service{Kind: "container", Name: "cloudy-db-pg1", Image: "postgres:16"}, nil},
		{"app process", agentproto.Service{Kind: "container", Name: "cloudy-shop-web-01234567", Image: "cloudy/shop:v3"}, nil},
		{"release run", agentproto.Service{Kind: "container", Name: "cloudy-app-x", Image: "cloudy/x:v1"}, nil},
		{"restore test", agentproto.Service{Kind: "container", Name: "cloudy-dbrestore-ab12", Image: "postgres:16"}, nil},
		{"caddy unit", agentproto.Service{Kind: "systemd", Name: "cloudy-caddy", Status: "active", Ports: []string{"80"}}, nil},
		{"frpc", agentproto.Service{Kind: "container", Name: "cloudy-frpc", Image: "snowdreamtech/frpc"}, nil},
		{"hosbah-", agentproto.Service{Kind: "container", Name: "hosbah-agent", Image: "x"}, nil},

		// Database engines by image.
		{"postgres", agentproto.Service{Kind: "container", Name: "pg-main", Image: "postgres:16-alpine", Ports: []string{"127.0.0.1:5433->5432/tcp"}},
			&want{"pg-main", TargetDataService, "postgres", "16-alpine", "127.0.0.1", 5433}},
		{"postgis", agentproto.Service{Kind: "container", Name: "gis", Image: "postgis/postgis:15-3.4"}, &want{"gis", TargetDataService, "postgres", "15-3.4", "", 0}},
		{"timescale", agentproto.Service{Kind: "container", Name: "ts", Image: "timescale/timescaledb:latest-pg16"}, &want{"ts", TargetDataService, "postgres", "latest-pg16", "", 0}},
		{"bitnami pg", agentproto.Service{Kind: "container", Name: "bpg", Image: "bitnami/postgresql"}, &want{"bpg", TargetDataService, "postgres", "latest", "", 0}},
		{"mysql", agentproto.Service{Kind: "container", Name: "my", Image: "mysql:8.4", Ports: []string{"0.0.0.0:3306->3306/tcp, :::3306->3306/tcp"}},
			&want{"my", TargetDataService, "mysql", "8.4", "", 3306}},
		{"mariadb", agentproto.Service{Kind: "container", Name: "maria", Image: "mariadb:11"}, &want{"maria", TargetDataService, "mysql", "11", "", 0}},
		{"redis", agentproto.Service{Kind: "container", Name: "cache", Image: "redis"}, &want{"cache", TargetDataService, "redis", "latest", "", 0}},
		{"valkey", agentproto.Service{Kind: "container", Name: "vk", Image: "valkey/valkey:8"}, &want{"vk", TargetDataService, "redis", "8", "", 0}},
		{"mongo", agentproto.Service{Kind: "container", Name: "mongo", Image: "mongo:7"}, &want{"mongo", TargetDataService, "mongodb", "7", "", 0}},
		{"minio", agentproto.Service{Kind: "container", Name: "s3", Image: "minio/minio:RELEASE.2024"}, &want{"s3", TargetDataService, "minio", "RELEASE.2024", "", 0}},
		{"registry with port", agentproto.Service{Kind: "container", Name: "rpg", Image: "reg.local:5000/library/postgres:15"}, &want{"rpg", TargetDataService, "postgres", "15", "", 0}},

		// Companion tools named after an engine are apps, not databases.
		{"mongo-express", agentproto.Service{Kind: "container", Name: "me", Image: "mongo-express:1"}, &want{"me", TargetApp, "", "", "", 0}},
		{"redis-commander", agentproto.Service{Kind: "container", Name: "rc", Image: "rediscommander/redis-commander"}, &want{"rc", TargetApp, "", "", "", 0}},
		{"pg exporter", agentproto.Service{Kind: "container", Name: "pe", Image: "prometheuscommunity/postgres-exporter"}, &want{"pe", TargetApp, "", "", "", 0}},
		{"minio client", agentproto.Service{Kind: "container", Name: "mc", Image: "minio/mc"}, &want{"mc", TargetApp, "", "", "", 0}},

		// Other containers are sites.
		{"web", agentproto.Service{Kind: "container", Name: "shop-web", Image: "nginx:1.27", Ports: []string{"0.0.0.0:8081->80/tcp"}}, &want{"shop-web", TargetApp, "", "", "", 0}},

		// Guests.
		{"guest site", agentproto.Service{Kind: "guest", Name: "wordpress", ID: "101", Type: "lxc", Address: "10.0.0.11"}, &want{"guest:101", TargetApp, "", "", "", 0}},
		{"guest pg", agentproto.Service{Kind: "guest", Name: "pg-prod", ID: "102", Type: "qemu", Address: "10.0.0.12"},
			&want{"guest:102", TargetDataService, "postgres", "unknown", "10.0.0.12", 0}},
		{"guest maria", agentproto.Service{Kind: "guest", Name: "db.maria.internal", ID: "103", Type: "lxc"}, &want{"guest:103", TargetDataService, "mysql", "unknown", "", 0}},
		{"guest redis token", agentproto.Service{Kind: "guest", Name: "redis-01", ID: "104", Type: "lxc"}, &want{"guest:104", TargetDataService, "redis", "unknown", "", 0}},
		{"guest not a token", agentproto.Service{Kind: "guest", Name: "pgadmin", ID: "105", Type: "lxc"}, &want{"guest:105", TargetApp, "", "", "", 0}},
		{"guest mongo", agentproto.Service{Kind: "guest", Name: "mongo", ID: "106", Type: "lxc"}, &want{"guest:106", TargetDataService, "mongodb", "unknown", "", 0}},
		{"guest minio", agentproto.Service{Kind: "guest", Name: "minio-store", ID: "107", Type: "lxc"}, &want{"guest:107", TargetDataService, "minio", "unknown", "", 0}},
		{"guest without id", agentproto.Service{Kind: "guest", Name: "x"}, nil},

		// systemd: only a non-base unit that publishes ports.
		{"unit without ports", agentproto.Service{Kind: "systemd", Name: "nginx", Status: "active"}, nil},
		{"unit with ports", agentproto.Service{Kind: "systemd", Name: "myapp", Status: "active", Ports: []string{"3000"}}, &want{"myapp", TargetApp, "", "", "", 0}},
		{"sshd", agentproto.Service{Kind: "systemd", Name: "sshd", Ports: []string{"22"}}, nil},
		{"systemd-resolved", agentproto.Service{Kind: "systemd", Name: "systemd-resolved", Ports: []string{"53"}}, nil},
		{"pveproxy", agentproto.Service{Kind: "systemd", Name: "pveproxy", Ports: []string{"8006"}}, nil},
		{"docker", agentproto.Service{Kind: "systemd", Name: "docker", Ports: []string{"2375"}}, nil},
		{"postfix", agentproto.Service{Kind: "systemd", Name: "postfix", Ports: []string{"25"}}, nil},

		{"unknown kind", agentproto.Service{Kind: "vm", Name: "x"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify([]agentproto.Service{c.in})
			if c.want == nil {
				if len(got) != 0 {
					t.Fatalf("want skipped, got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want one item, got %+v", got)
			}
			g := got[0]
			if g.OriginKey != c.want.key || g.Target != c.want.target || g.Kind != c.want.kind || g.Version != c.want.version ||
				g.Host != c.want.host || g.Port != c.want.port {
				t.Fatalf("got key=%q target=%q kind=%q version=%q host=%q port=%d, want %+v", g.OriginKey, g.Target, g.Kind, g.Version, g.Host, g.Port, *c.want)
			}
		})
	}
}

func TestClassifyState(t *testing.T) {
	for status, want := range map[string]string{"running": StateRunning, "active": StateRunning, "Up 3 hours": StateRunning,
		"exited": StateStopped, "stopped": StateStopped, "inactive": StateStopped, "failed": StateStopped, "": StateStopped} {
		got := Classify([]agentproto.Service{{Kind: "container", Name: "x", Image: "nginx", Status: status}})
		if got[0].State != want {
			t.Errorf("%q -> %q, want %q", status, got[0].State, want)
		}
	}
}

func TestClassifyDedupes(t *testing.T) {
	got := Classify([]agentproto.Service{{Kind: "container", Name: "a", Image: "nginx"}, {Kind: "container", Name: "a", Image: "nginx"}})
	if len(got) != 1 {
		t.Fatalf("a repeated key must be imported once, got %d", len(got))
	}
}

func TestNames(t *testing.T) {
	for in, want := range map[string]string{
		"My Site!!": "my-site", "shop_web": "shop-web", "---": "site", "Ünïcode": "n-code",
		"a-very-long-container-name-that-goes-well-beyond-forty-chars": "a-very-long-container-name-that-goes-wel",
	} {
		if got := Slug(in, "site"); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	used := map[string]bool{"web": true, "web-2": true}
	if got := UniqueName("web", "site", used); got != "web-3" {
		t.Errorf("UniqueName = %q, want web-3", got)
	}
	long := "a-very-long-container-name-that-goes-well-beyond-forty-chars"
	used = map[string]bool{Slug(long, "site"): true}
	if got := UniqueName(long, "site", used); len(got) > 40 || got[len(got)-2:] != "-2" {
		t.Errorf("UniqueName(long) = %q", got)
	}
}

func TestFirstPublished(t *testing.T) {
	for in, want := range map[string]struct {
		h string
		p int
	}{
		"0.0.0.0:5432->5432/tcp":            {"", 5432},
		"127.0.0.1:6379->6379/tcp":          {"127.0.0.1", 6379},
		":::8080->80/tcp":                   {"", 8080},
		"5432/tcp":                          {"", 0},
		"5432/tcp, 0.0.0.0:15432->5432/tcp": {"", 15432},
		"0.0.0.0:8000-8010->8000-8010/tcp":  {"", 8000},
		"[::1]:9000->9000/tcp":              {"::1", 9000},
	} {
		h, p := FirstPublished([]string{in})
		if h != want.h || p != want.p {
			t.Errorf("FirstPublished(%q) = %q,%d want %q,%d", in, h, p, want.h, want.p)
		}
	}
}
