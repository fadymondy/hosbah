//go:build !linux

package agent

import "os/exec"

// configureGroup: the agent only ever ships built for Linux (CLAUDE.md); this no-op stub keeps
// `go build ./...` and the rest of the test suite green on a developer's machine of any OS. Without
// it, ctx.Done() still kills the direct child (exec.CommandContext's default), just not a whole
// process group.
func configureGroup(_ *exec.Cmd) {}
