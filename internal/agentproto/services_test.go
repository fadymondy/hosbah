package agentproto

import "testing"

func TestServiceListParamsValidate(t *testing.T) {
	if err := (ServiceListParams{}).Validate(); err != nil {
		t.Errorf("an empty query should validate: %v", err)
	}
	if err := (ServiceListParams{Query: "caddy"}).Validate(); err != nil {
		t.Errorf("a short query should validate: %v", err)
	}
	if err := (ServiceListParams{Query: string(make([]byte, 201))}).Validate(); err == nil {
		t.Error("a query over 200 bytes should not validate")
	}
}

func TestValidUnitName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"caddy.service", true},
		{"my-app_1.service", true},
		{"user@1.service", true},
		{"caddy", false},              // missing .service
		{"caddy.timer", false},        // wrong unit type
		{"caddy; rm -rf /", false},    // shell metacharacters
		{"../../etc/passwd", false},   // path traversal
		{"", false},
		{string(make([]byte, 260)) + ".service", false}, // too long
	}
	for _, c := range cases {
		if got := ValidUnitName(c.name); got != c.want {
			t.Errorf("ValidUnitName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestProtectedUnit(t *testing.T) {
	if !ProtectedUnit("hosbah-agent.service") || !ProtectedUnit("cloudy-agent.service") {
		t.Error("the agent's own unit, under either name, must be protected")
	}
	if ProtectedUnit("caddy.service") {
		t.Error("caddy.service is not protected")
	}
}

func TestServiceActionParamsValidate(t *testing.T) {
	valid := ServiceActionParams{Unit: "caddy.service", Action: "restart"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("a valid action should validate: %v", err)
	}

	for _, action := range []string{"start", "stop", "restart", "reload"} {
		p := ServiceActionParams{Unit: "caddy.service", Action: action}
		if err := p.Validate(); err != nil {
			t.Errorf("action %q should validate: %v", action, err)
		}
	}

	badAction := ServiceActionParams{Unit: "caddy.service", Action: "explode"}
	if err := badAction.Validate(); err == nil {
		t.Error("an unknown action should not validate")
	}

	badUnit := ServiceActionParams{Unit: "caddy; rm -rf /", Action: "restart"}
	if err := badUnit.Validate(); err == nil {
		t.Error("a bad unit name should not validate")
	}

	for _, unit := range ProtectedUnits {
		p := ServiceActionParams{Unit: unit, Action: "restart"}
		if err := p.Validate(); err == nil {
			t.Errorf("acting on %s must always be refused", unit)
		}
	}
}
