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

func TestSFTPCheckEffective(t *testing.T) {
	ok := "port 22\nchrootdirectory /srv/x\nforcecommand internal-sftp\n"
	if err := sftpCheckEffective(ok, false); err != nil {
		t.Fatal(err)
	}
	if sftpCheckEffective(ok, true) == nil {
		t.Fatal("a read-only user under the read-write rule must be refused")
	}
	if sftpCheckEffective("chrootdirectory /srv/x\nforcecommand internal-sftp -R\n", true) != nil {
		t.Fatal("read-only rule should pass")
	}
	if sftpCheckEffective("chrootdirectory none\n", false) == nil {
		t.Fatal("an ignored drop-in (no chroot) must be refused")
	}
}
