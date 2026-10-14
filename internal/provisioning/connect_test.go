package provisioning

import "testing"

func TestParseProbe(t *testing.T) {
	p := parseProbe("os=debian\nver=12\nuid=0\ndocker=1\npve=8.2.4\nnode=pve-node\n")
	if p.osID != "debian" || p.osVersion != "12" || p.uid != 0 || !p.docker || !p.proxmox || p.pveVersion != "8.2.4" || p.node != "pve-node" || p.sudo {
		t.Fatalf("probe = %+v", p)
	}
	p = parseProbe("os=rocky\nver=9.4\nuid=1000\nsudo=1\n")
	if p.uid != 1000 || !p.sudo || p.proxmox || p.docker {
		t.Fatalf("probe = %+v", p)
	}
	if parseProbe("").uid != -1 {
		t.Fatal("a missing uid must not read as root")
	}
}
