//go:build !linux

package agent

import (
	"context"
	"runtime"

	"github.com/fadymondy/cloudy/internal/agentproto"
)

// The agent targets Linux. These stubs let the package build and its protocol, signing and
// executor tests run on a developer's Windows or macOS machine; facts are simply sparse there.

func collectPlatform(_ context.Context, f *agentproto.Facts) { f.OSID = runtime.GOOS }

func systemdRunning() bool { return false }

func containerVirt(context.Context) string { return "" }

func activeSwap() string { return "" }

func diskUsage(string) (total, used uint64) { return 0, 0 }
