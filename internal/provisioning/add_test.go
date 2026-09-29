package provisioning

import (
	"testing"

	db "github.com/fadymondy/hosbah/internal/db/gen"
)

func TestMatchProxmoxNode(t *testing.T) {
	str := func(s string) *string { return &s }
	servers := []db.ServersOfTeamForAutoLinkRow{
		{ID: "guest", Name: "pve-3x1-web", Hostname: str("pve-3x1"), ParentServerID: str("node")},
		{ID: "other", Name: "win-docker", Hostname: str("win"), IpAddress: str("172.17.0.5")},
		{ID: "node", Name: "main", Hostname: str("pve-3x1.example.com"), IpAddress: str("45.129.183.99")},
	}
	cases := []struct{ config, want string }{
		{`{"node":"pve-3x1","api_url":"https://10.0.0.1:8006"}`, "node"},
		{`{"node":"elsewhere","api_url":"https://45.129.183.99:8006"}`, "node"},
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
