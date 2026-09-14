//go:build !linux

package agent

import (
	"time"

	"github.com/fadymondy/cloudy/internal/agentproto"
)

// Sampler is a stub off Linux (see facts_other.go): heartbeats carry a timestamp only.
type Sampler struct{}

// NewSampler returns the stub sampler.
func NewSampler() *Sampler { return &Sampler{} }

// Sample returns an empty reading.
func (s *Sampler) Sample() agentproto.Metrics { return agentproto.Metrics{At: time.Now().UTC()} }
