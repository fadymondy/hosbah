package provisioning

import (
	"testing"

	db "github.com/fadymondy/hosbah/internal/db/gen"
)

func TestMatchProxmoxNode(t *testing.T) {
	str := func(s string) *string { return &s }
	servers := []db.ServersOfTeamForAutoLinkRow{
		{ID: "guest", Name: "pve-node-web", Hostname: str("pve-node"), ParentServerID: str("node")},
		{ID: "other", Name: "win-docker", Hostname: str("win"), IpAddress: str("172.17.0.5")},
		{ID: "node", Name: "main", Hostname: str("pve-node.example.com"), IpAddress: str("203.0.113.10")},
	}
	cases := []struct{ config, want string }{
		{`{"node":"pve-node","api_url":"https://10.0.0.1:8006"}`, "node"},
		{`{"node":"elsewhere","api_url":"https://203.0.113.10:8006"}`, "node"},
		{`{"node":"MAIN"}`, "node"},
		{`{"node":"nope","api_url":"https://10.9.9.9:8006"}`, ""},
		{`not json`, ""},
	}
	for _, c := range cases {
		if got := matchProxmoxNode(c.config, servers); got != c.want {
			t.Errorf("matchProxmoxNode(%s) = %q, want %q", c.config, got, c.want)
		}
	}
}
