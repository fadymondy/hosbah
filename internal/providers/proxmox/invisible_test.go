package proxmox

import (
	"errors"
	"fmt"
	"testing"
)

func TestInvisibleGuest(t *testing.T) {
	seen := fmt.Errorf("%w (GET /nodes/pve/lxc/121/status/current: HTTP 403 Permission check failed (/vms/121, VM.Audit))", ErrUnauthorized)
	if !invisibleGuest(seen) {
		t.Fatal("a 403 on the guest's own VM.Audit is an invisible guest")
	}
	for _, e := range []error{
		fmt.Errorf("%w (HTTP 401 authentication failure)", ErrUnauthorized),
		fmt.Errorf("%w (HTTP 403 Permission check failed (/nodes/pve, Sys.Audit))", ErrUnauthorized),
		errors.New("Permission check failed (/vms/121, VM.Audit)"),
	} {
		if invisibleGuest(e) {
			t.Errorf("%v must not count as an invisible guest", e)
		}
	}
}
