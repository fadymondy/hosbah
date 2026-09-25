//go:build linux

package agent

import (
	"strings"
	"testing"
)

// renderSFTPDropIn does no syscalls, so it is exercised directly (no root, no sshd, no chroot):
// only sftpWriteDropIn/sftpEnsureRootDir/etc — the parts that actually touch the filesystem or
// spawn useradd/sshd — need a real Linux box, and are exercised by hand against a throwaway VM.
func TestRenderSFTPDropIn(t *testing.T) {
	got := renderSFTPDropIn()
	if got == "" {
		t.Fatal("renderSFTPDropIn returned nothing")
	}
	for _, want := range []string{
		"Match Group " + sftpGroupRW,
		"Match Group " + sftpGroupRO,
		"ChrootDirectory %h",
		"ForceCommand internal-sftp",
		"ForceCommand internal-sftp -R",
		"AllowTcpForwarding no",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderSFTPDropIn missing %q:\n%s", want, got)
		}
	}
	if renderSFTPDropIn() != got {
		t.Fatal("renderSFTPDropIn must be deterministic (it is compared byte-for-byte to decide whether to rewrite the file)")
	}
}
