package proxmox

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/cloudy/internal/providers"
	"github.com/fadymondy/cloudy/internal/providers/sshboot"
)

// TestLive runs against a real Proxmox VE. It is skipped unless CLOUDY_PROXMOX_LIVE=1.
//
// Tier 1 (CLOUDY_PROXMOX_LIVE=1): read-only — Validate, Regions, Images.
// Tier 2 (also CLOUDY_PROXMOX_LIVE_CREATE=1): creates an LXC (if PROXMOX_LXC_TEMPLATE is set) and a
// VM (if PROXMOX_TEMPLATE_VMID is set), waits for each to run with an IP, then deletes it. Deletion
// is registered with t.Cleanup, so it happens even when the test fails.
func TestLive(t *testing.T) {
	if os.Getenv("CLOUDY_PROXMOX_LIVE") != "1" {
		t.Skip("set CLOUDY_PROXMOX_LIVE=1 (and the PROXMOX_* variables) to run against a real Proxmox")
	}
	// PROXMOX_* or the shorter PVE_* names (PVE_URL, PVE_TOKEN_ID, PVE_TOKEN_SECRET, PVE_NODE, PVE_STORAGE, PVE_BRIDGE, PVE_POOL…).
	env := func(k string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		if k == "PROXMOX_API_URL" {
			return os.Getenv("PVE_URL")
		}
		return os.Getenv("PVE_" + strings.TrimPrefix(k, "PROXMOX_"))
	}
	cred := providers.Credential{
		Config: map[string]string{
			"api_url":         env("PROXMOX_API_URL"),
			"token_id":        env("PROXMOX_TOKEN_ID"),
			"node":            env("PROXMOX_NODE"),
			"storage":         env("PROXMOX_STORAGE"),
			"bridge":          env("PROXMOX_BRIDGE"),
			"pool":            env("PROXMOX_POOL"),
			"template_vmid":   env("PROXMOX_TEMPLATE_VMID"),
			"lxc_template":    env("PROXMOX_LXC_TEMPLATE"),
			"tls_fingerprint": env("PROXMOX_TLS_FINGERPRINT"),
		},
		Secret: env("PROXMOX_TOKEN_SECRET"),
	}
	d, err := New(cred)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	regions, err := d.Regions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("regions: %+v", regions)
	images, err := d.Images(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("images: %+v", images)

	if os.Getenv("CLOUDY_PROXMOX_LIVE_CREATE") != "1" {
		return
	}
	if cred.Config["lxc_template"] != "" {
		t.Run("lxc", func(t *testing.T) { liveCreate(t, d, providers.KindLXC) })
	}
	if cred.Config["template_vmid"] != "" {
		t.Run("vm", func(t *testing.T) { liveCreate(t, d, providers.KindVM) })
	}
}

func liveCreate(t *testing.T, d *Driver, kind string) {
	pub, _, err := sshboot.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	id, err := d.Create(ctx, providers.CreateSpec{Name: "cloudy-live-" + kind, Kind: kind, SSHPublicKeys: []string{pub}, Tags: []string{"cloudy-live-test"}})
	if id != "" {
		t.Cleanup(func() {
			dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer dcancel()
			if err := d.Delete(dctx, id); err != nil {
				t.Errorf("cleanup: deleting %s: %v", id, err)
			}
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	for {
		m, err := d.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if m.Status == providers.StatusRunning && m.IPv4 != "" {
			t.Logf("%s running: %+v", kind, m)
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s never reported running with an IP; last %+v", kind, m)
		case <-time.After(5 * time.Second):
		}
	}
}
