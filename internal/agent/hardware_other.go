//go:build !linux

package agent

import (
	"context"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// collectHardware off Linux (see facts_other.go): interfaces only.
func collectHardware(context.Context) *agentproto.Hardware {
	h := &agentproto.Hardware{Disks: []agentproto.Disk{}, GPUs: []agentproto.GPU{}}
	h.Interfaces, h.PublicIP = interfaces()
	return h
}
