package agentproto

import "testing"

func TestGuestEnrolParamsValidate(t *testing.T) {
	cases := []struct {
		name string
		p    GuestEnrolParams
		ok   bool
	}{
		{"ok", GuestEnrolParams{VMID: "101", CommandBox: "box"}, true},
		{"bad vmid empty", GuestEnrolParams{VMID: "", CommandBox: "box"}, false},
		{"bad vmid non numeric", GuestEnrolParams{VMID: "abc", CommandBox: "box"}, false},
		{"bad vmid too low", GuestEnrolParams{VMID: "0", CommandBox: "box"}, false},
		{"missing command_box", GuestEnrolParams{VMID: "101", CommandBox: ""}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.p.Validate()
			if c.ok && err != nil {
				t.Fatalf("expected valid, got %v", err)
			}
			if !c.ok && err == nil {
				t.Fatalf("expected an error, got nil")
			}
		})
	}
}

func TestGuestEnrolContextBindsToVMID(t *testing.T) {
	if GuestEnrolContext("101") == GuestEnrolContext("102") {
		t.Fatalf("GuestEnrolContext must differ per vmid so a box cannot be replayed against another guest")
	}
}

func TestKindGuestEnrolIsAnAgentKind(t *testing.T) {
	found := false
	for _, k := range AgentKinds {
		if k == KindGuestEnrol {
			found = true
		}
	}
	if !found {
		t.Fatalf("KindGuestEnrol must be in AgentKinds so the dispatcher signs and sends it to the agent")
	}
}
