//go:build !linux

package agent

import "os"

// ownerOf and preserveOwner are Linux-only (uid/gid): the agent only ever ships for Linux servers,
// this build tag exists so the package still builds and its tests still run on a developer's
// machine, whatever that machine is.

func ownerOf(info os.FileInfo) string { return "" }

func preserveOwner(tmpName string, existing os.FileInfo) {}
