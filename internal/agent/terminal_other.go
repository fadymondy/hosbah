//go:build !linux

package agent

import (
	"context"
	"errors"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// spawnTerminal: the agent only ever ships built for Linux (CLAUDE.md); this stub keeps
// `go build ./...` and the rest of the test suite green on a developer's machine of any OS. It
// refuses cleanly rather than importing a PTY package that has no unix process-group semantics.
func spawnTerminal(_ context.Context, _ agentproto.TerminalOpenParams) (terminalPTY, error) {
	return nil, errors.New("terminal sessions run on Linux only")
}
