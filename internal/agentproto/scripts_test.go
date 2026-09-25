package agentproto

import "testing"

const testScriptRunServerID = "00000000-0000-4000-8000-0000000000f1"

func validScriptRunParams() ScriptRunParams {
	return ScriptRunParams{ScriptRunID: testScriptRunServerID, Name: "caddy", Body: "#!/bin/bash\necho hi\n", TimeoutSeconds: 60}
}

func TestScriptRunParamsValidate(t *testing.T) {
	if err := validScriptRunParams().Validate(); err != nil {
		t.Fatalf("a valid script should validate: %v", err)
	}

	badID := validScriptRunParams()
	badID.ScriptRunID = "not-a-uuid"
	if err := badID.Validate(); err == nil {
		t.Error("a bad script_run_id should not validate")
	}

	empty := validScriptRunParams()
	empty.Body = ""
	if err := empty.Validate(); err == nil {
		t.Error("an empty body should not validate")
	}

	tooBig := validScriptRunParams()
	tooBig.Body = string(make([]byte, MaxScriptBody+1))
	if err := tooBig.Validate(); err == nil {
		t.Error("a body over MaxScriptBody should not validate")
	}

	for _, tc := range []struct {
		name    string
		seconds int
	}{{"too low", MinScriptTimeoutSeconds - 1}, {"too high", MaxScriptTimeoutSeconds + 1}} {
		p := validScriptRunParams()
		p.TimeoutSeconds = tc.seconds
		if err := p.Validate(); err == nil {
			t.Errorf("timeout_seconds %s (%d) should not validate", tc.name, tc.seconds)
		}
	}

	for _, tc := range []struct{ seconds int }{{MinScriptTimeoutSeconds}, {MaxScriptTimeoutSeconds}} {
		p := validScriptRunParams()
		p.TimeoutSeconds = tc.seconds
		if err := p.Validate(); err != nil {
			t.Errorf("timeout_seconds at the boundary (%d) should validate: %v", tc.seconds, err)
		}
	}

	tooManyVars := validScriptRunParams()
	tooManyVars.Args = map[string]string{}
	for i := 0; i < MaxScriptEnvVars+1; i++ {
		tooManyVars.Args[randLabelKey(i)] = "v"
	}
	if err := tooManyVars.Validate(); err == nil {
		t.Error("more than MaxScriptEnvVars args/env should not validate")
	}

	badKey := validScriptRunParams()
	badKey.Args = map[string]string{"not a valid name!": "v"}
	if err := badKey.Validate(); err == nil {
		t.Error("a bad env var name should not validate")
	}

	badEnvKey := validScriptRunParams()
	badEnvKey.Env = map[string]string{"1STARTS_WITH_DIGIT_IS_FINE_HERE": "v"} // digits allowed, just not the only check
	if err := badEnvKey.Validate(); err != nil {
		t.Errorf("a digit-containing env var name should validate: %v", err)
	}

	longKey := validScriptRunParams()
	longKey.Env = map[string]string{string(make([]byte, MaxScriptEnvKey+1)): "v"}
	if err := longKey.Validate(); err == nil {
		t.Error("an over-long env var name should not validate")
	}

	longValue := validScriptRunParams()
	longValue.Env = map[string]string{"OK": string(make([]byte, MaxScriptEnvValue+1))}
	if err := longValue.Validate(); err == nil {
		t.Error("an over-long env var value should not validate")
	}
}

func TestScriptEnvContextBindsToOneRun(t *testing.T) {
	a := ScriptEnvContext("run-a")
	b := ScriptEnvContext("run-b")
	if a == b {
		t.Fatal("two different runs must bind to different contexts")
	}
	if ScriptEnvContext("run-a") != a {
		t.Fatal("the same run id must bind to the same context")
	}
}
