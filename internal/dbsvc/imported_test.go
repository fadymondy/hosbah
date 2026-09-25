package dbsvc

import (
	"strings"
	"testing"

	db "github.com/fadymondy/hosbah/internal/db/gen"
)

func strptrT(s string) *string { return &s }

// svcFixture builds a minimal db.DataService for usable()/backupable()/restorable()/execTarget() —
// these read only Source, Kind, Status, Version, RootSealed, OriginKind, OriginKey, Origin and
// ImportedState, and never touch the database.
func svcFixture(opts ...func(*db.DataService)) db.DataService {
	svc := db.DataService{
		ID: "svc1", Name: "pg-shared", Kind: "postgres", Version: "16", Status: "running", Source: "managed",
	}
	for _, o := range opts {
		o(&svc)
	}
	return svc
}

func imported(originKind, originKey, origin string) func(*db.DataService) {
	return func(svc *db.DataService) {
		svc.Source = SourceImported
		svc.OriginKind = strptrT(originKind)
		if originKey != "" {
			svc.OriginKey = strptrT(originKey)
			if originKind == "container" {
				svc.Container = strptrT(originKey) // contOf falls back to OriginKey for a container
			}
		}
		if origin != "" {
			svc.Origin = strptrT(origin)
		}
	}
}

func withCredentials(svc *db.DataService) { svc.RootSealed = strptrT("cab1:abc") }

func lxcOrigin(vmid string) string {
	return `{"guest_type":"lxc","vmid":"` + vmid + `"}`
}

func qemuOrigin(vmid string) string {
	return `{"guest_type":"qemu","vmid":"` + vmid + `"}`
}

// TestUsableDecisionTable covers every branch usable() takes: a managed service, an imported
// container with and without credentials, an imported LXC guest with and without credentials, an
// imported QEMU guest, MinIO in an LXC guest, an unsupported imported kind and a missing workload.
func TestUsableDecisionTable(t *testing.T) {
	cases := []struct {
		name    string
		svc     db.DataService
		wantErr string // substring, "" means no error
	}{
		{"managed service always usable", svcFixture(), ""},
		{"imported container, no credentials", svcFixture(imported("container", "c1", "")), "credentials"},
		{"imported container, with credentials", svcFixture(imported("container", "c1", ""), withCredentials), ""},
		{"imported lxc guest, no credentials", svcFixture(imported("guest", "102", lxcOrigin("102"))), "credentials"},
		{"imported lxc guest, with credentials", svcFixture(imported("guest", "102", lxcOrigin("102")), withCredentials), ""},
		{"imported qemu guest refused", svcFixture(imported("guest", "103", qemuOrigin("103")), withCredentials), "QEMU"},
		{"minio in an lxc guest refused", svcFixture(imported("guest", "102", lxcOrigin("102")), withCredentials, func(svc *db.DataService) { svc.Kind = "minio" }), "MinIO"},
		{"unsupported imported kind", svcFixture(imported("container", "c1", ""), withCredentials, func(svc *db.DataService) { svc.Kind = "mongodb" }), "not supported"},
		{"missing workload", svcFixture(imported("container", "c1", ""), withCredentials, func(svc *db.DataService) { svc.ImportedState = strptrT("missing") }), "no longer reported"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := usable(c.svc)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("expected an error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

// TestBackupableRefusesGuests: backups stay unsupported for any Proxmox guest, LXC or QEMU, even
// though db.create/db.user/db.query/db.insights now reach an LXC guest.
func TestBackupableRefusesGuests(t *testing.T) {
	if err := backupable(svcFixture()); err != nil {
		t.Fatalf("a managed service must be backupable: %v", err)
	}
	if err := backupable(svcFixture(imported("container", "c1", ""), withCredentials)); err != nil {
		t.Fatalf("an imported container must be backupable: %v", err)
	}
	err := backupable(svcFixture(imported("guest", "102", lxcOrigin("102")), withCredentials))
	if err == nil || !strings.Contains(err.Error(), "guest") {
		t.Fatalf("an lxc guest must not be backupable, got %v", err)
	}
}

// TestExecTargetOfService: execTarget derives target/vmid/guest_type/container from the workload,
// leaving Target empty (implying "container") for every managed or container-origin service.
func TestExecTargetOfService(t *testing.T) {
	target, vmid, guestType, container := execTarget(svcFixture())
	if target != "" || vmid != "" || guestType != "" || container == "" {
		t.Fatalf("managed service: target=%q vmid=%q guestType=%q container=%q", target, vmid, guestType, container)
	}

	target, vmid, guestType, container = execTarget(svcFixture(imported("container", "c1", "")))
	if target != "" || vmid != "" || guestType != "" || container != "c1" {
		t.Fatalf("imported container: target=%q vmid=%q guestType=%q container=%q", target, vmid, guestType, container)
	}

	target, vmid, guestType, container = execTarget(svcFixture(imported("guest", "102", lxcOrigin("102"))))
	if target != "guest" || vmid != "102" || guestType != "lxc" || container != "" {
		t.Fatalf("imported lxc guest: target=%q vmid=%q guestType=%q container=%q", target, vmid, guestType, container)
	}
}

// TestRestorableRefusesGuest: a restore-test needs the engine's own image, which a Proxmox guest
// never records, but backupable() now refuses it first with the clearer message.
func TestRestorableRefusesGuest(t *testing.T) {
	err := restorable(svcFixture(imported("guest", "102", lxcOrigin("102")), withCredentials))
	if err == nil || !strings.Contains(err.Error(), "guest") {
		t.Fatalf("a guest restore-test must be refused, got %v", err)
	}
}
