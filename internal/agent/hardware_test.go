package agent

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "hardware", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseCPUInfo(t *testing.T) {
	model, cores, threads := parseCPUInfo(fixture(t, "cpuinfo_2c4t.txt"))
	if model != "Intel(R) Core(TM) i7-8700 CPU @ 3.20GHz" || cores != 2 || threads != 4 {
		t.Fatalf("x86: %q %d cores %d threads", model, cores, threads)
	}
	model, cores, threads = parseCPUInfo(fixture(t, "cpuinfo_arm.txt"))
	if model != "Raspberry Pi 4 Model B Rev 1.4" || cores != 2 || threads != 2 {
		t.Fatalf("arm: %q %d cores %d threads", model, cores, threads)
	}
	if m, c, th := parseCPUInfo(""); m != "" || c != 0 || th != 0 {
		t.Fatalf("empty: %q %d %d", m, c, th)
	}
}

func TestParseMounts(t *testing.T) {
	got := parseMounts(fixture(t, "mounts.txt"))
	want := []mountEntry{
		{Device: "/dev/sda1", Mount: "/", FS: "ext4"},
		{Device: "/dev/sda15", Mount: "/boot/efi", FS: "vfat"},
		{Device: "/dev/sdb1", Mount: "/mnt/data disk", FS: "xfs"},
		{Device: "rpool/ROOT/pve-1", Mount: "/srv", FS: "zfs"}, // one entry per pool
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mounts:\n got %+v\nwant %+v", got, want)
	}
	if len(parseMounts("garbage\n")) != 0 {
		t.Fatal("garbage should give no mounts")
	}
}

func TestParseNvidia(t *testing.T) {
	inv := parseNvidiaInventory(fixture(t, "nvidia_inventory.txt"))
	want := []agentproto.GPU{
		{Index: 0, Name: "NVIDIA GeForce RTX 3090", Vendor: "NVIDIA", MemoryTotalBytes: 24576 << 20, Driver: "550.54.14"},
		{Index: 1, Name: "Tesla T4", Vendor: "NVIDIA", MemoryTotalBytes: 15360 << 20},
	}
	if !reflect.DeepEqual(inv, want) {
		t.Fatalf("inventory:\n got %+v\nwant %+v", inv, want)
	}
	m := parseNvidiaMetrics(fixture(t, "nvidia_metrics.txt"))
	wantM := []agentproto.GPUMetric{
		{Index: 0, UtilPercent: 37, MemUsedBytes: 1024 << 20, MemTotalBytes: 24576 << 20, TempC: 61},
		{Index: 1, MemTotalBytes: 15360 << 20},
	}
	if !reflect.DeepEqual(m, wantM) {
		t.Fatalf("metrics:\n got %+v\nwant %+v", m, wantM)
	}
	// nvidia-smi's own failure text ("NVIDIA-SMI has failed…") parses to nothing.
	if len(parseNvidiaMetrics("NVIDIA-SMI has failed because it couldn't communicate with the NVIDIA driver.")) != 0 {
		t.Fatal("an error message should give no GPUs")
	}
}

func TestParseLspci(t *testing.T) {
	got := parseLspci(fixture(t, "lspci_mm.txt"))
	want := []agentproto.GPU{
		{Index: 0, Name: "UHD Graphics 630 (Desktop)", Vendor: "Intel"},
		{Index: 1, Name: "Navi 21 [Radeon RX 6800]", Vendor: "AMD"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lspci:\n got %+v\nwant %+v", got, want)
	}
}

func TestParseNetDevAndRate(t *testing.T) {
	c, ok := parseNetDev(fixture(t, "net_dev.txt"))
	if !ok || c.rx != 1002000 || c.tx != 400600 { // eth0 + ens5; lo, docker0, veth* are left out
		t.Fatalf("net dev: %+v %v", c, ok)
	}
	rx, tx := netRate(netCounters{rx: 1000, tx: 500}, netCounters{rx: 3000, tx: 400}, 2*time.Second)
	if rx != 1000 || tx != 0 { // tx went backwards: 0, not a wrap-sized number
		t.Fatalf("rate: %v %v", rx, tx)
	}
	if _, ok := parseNetDev(""); ok {
		t.Fatal("empty /proc/net/dev should not be ok")
	}
}

func TestIsPublicIP(t *testing.T) {
	for ip, want := range map[string]bool{"8.8.8.8": true, "10.0.0.5": false, "192.168.1.2": false, "100.101.1.1": false,
		"169.254.1.1": false, "127.0.0.1": false, "2001:4860::8888": true, "fd00::1": false, "": false} {
		if got := isPublicIP(net.ParseIP(ip)); got != want {
			t.Errorf("isPublicIP(%q) = %v", ip, got)
		}
	}
}
