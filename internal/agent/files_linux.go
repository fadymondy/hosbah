//go:build linux

package agent

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// ownerOf is the file's user name (falling back to the bare uid when the name can't be looked up).
func ownerOf(info os.FileInfo) string {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	if u, err := user.LookupId(strconv.FormatUint(uint64(st.Uid), 10)); err == nil {
		return u.Username
	}
	return strconv.FormatUint(uint64(st.Uid), 10)
}

// preserveOwner chowns tmpName to existing's uid/gid, best-effort (a non-root agent — never the
// normal case, but tests run unprivileged — simply keeps whatever owner it created the file as).
func preserveOwner(tmpName string, existing os.FileInfo) {
	st, ok := existing.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	_ = os.Chown(tmpName, int(st.Uid), int(st.Gid))
}
