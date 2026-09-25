package agent

import (
	"crypto/ed25519"
	"slices"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// Every kind the agent has a handler for must be on agentproto.AgentKinds: the control plane's
// queue refuses to enqueue anything else ("unknown operation kind") and the agent refuses to run
// it, so a handler missing from the list is a feature that fails for every user.
func TestEveryHandledKindIsAllowed(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	e := NewExecutor("srv", pub, "test", "https://cp.test")
	for kind := range e.handlers {
		if !slices.Contains(agentproto.AgentKinds, kind) {
			t.Errorf("%s has a handler but is not in agentproto.AgentKinds", kind)
		}
	}
}
