package agentproto

import (
	"encoding/base64"
	"strings"
	"testing"
)

func validTermParams() TerminalOpenParams {
	return TerminalOpenParams{SessionID: "11111111-1111-4111-8111-111111111111", Target: TerminalTargetHost, Cols: 80, Rows: 24}
}

func TestTerminalOpenParamsValidate(t *testing.T) {
	if err := validTermParams().Validate(); err != nil {
		t.Fatalf("host target should validate: %v", err)
	}

	bad := validTermParams()
	bad.SessionID = "not-a-uuid"
	if err := bad.Validate(); err == nil {
		t.Fatal("bad session id should be refused")
	}

	bad = validTermParams()
	bad.Cols = 0
	if err := bad.Validate(); err == nil {
		t.Fatal("cols out of range should be refused")
	}

	bad = validTermParams()
	bad.Rows = 1000
	if err := bad.Validate(); err == nil {
		t.Fatal("rows out of range should be refused")
	}

	app := validTermParams()
	app.Target = "app:22222222-2222-4222-8222-222222222222/web"
	if err := app.Validate(); err == nil {
		t.Fatal("an app target needs a deployment id")
	}
	app.DeploymentID = "33333333-3333-4333-8333-333333333333"
	if err := app.Validate(); err != nil {
		t.Fatalf("app target with deployment should validate: %v", err)
	}

	app2 := validTermParams()
	app2.Target = "app:not-a-uuid/web"
	app2.DeploymentID = "33333333-3333-4333-8333-333333333333"
	if err := app2.Validate(); err == nil {
		t.Fatal("bad app id should be refused")
	}

	app3 := validTermParams()
	app3.Target = "nonsense"
	if err := app3.Validate(); err == nil {
		t.Fatal("an unknown target shape should be refused")
	}
}

func TestParseTerminalAppTarget(t *testing.T) {
	appID, process, ok := ParseTerminalAppTarget("app:abc/web")
	if !ok || appID != "abc" || process != "web" {
		t.Fatalf("got %q %q %v", appID, process, ok)
	}
	if _, _, ok := ParseTerminalAppTarget("host"); ok {
		t.Fatal("host should not parse as an app target")
	}
	if _, _, ok := ParseTerminalAppTarget("app:abc"); ok {
		t.Fatal("a target with no process should be refused")
	}
}

func TestValidTermData(t *testing.T) {
	if !ValidTermData("") {
		t.Fatal("empty is valid (no data this frame)")
	}
	small := base64.StdEncoding.EncodeToString([]byte("hello"))
	if !ValidTermData(small) {
		t.Fatal("a small payload should validate")
	}
	if ValidTermData("not base64!!") {
		t.Fatal("malformed base64 should be refused")
	}
	big := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", MaxTermFrame+1)))
	if ValidTermData(big) {
		t.Fatal("a payload over the limit should be refused")
	}
}

func TestValidTermSize(t *testing.T) {
	if !ValidTermSize(80, 24) {
		t.Fatal("80x24 should be valid")
	}
	if ValidTermSize(0, 24) || ValidTermSize(80, 0) || ValidTermSize(MaxTermCols+1, 24) {
		t.Fatal("out of range sizes should be refused")
	}
}

func TestTerminalOpenInAgentKinds(t *testing.T) {
	found := false
	for _, k := range AgentKinds {
		if k == KindTerminalOpen {
			found = true
		}
	}
	if !found {
		t.Fatal("terminal.open must be an allowed agent kind")
	}
}
