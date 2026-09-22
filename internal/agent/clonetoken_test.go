package agent

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// A GitHub App clone token opens only on its server, for its repository, and only as a token; the
// helper that hands it to git names the variable, never the value.
func TestCloneToken(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	oc := &OpContext{priv: priv, log: func(string, string) {}}
	const repo = "https://github.com/acme/web"
	seal := func(repoURL, token string) string {
		box, err := agentproto.SealBox(pubB64, agentproto.GitTokenContext(repoURL), []byte(token))
		if err != nil {
			t.Fatal(err)
		}
		return box
	}

	got, err := openCloneToken(oc, agentproto.GitSource{RepoURL: repo, Ref: "main", Token: seal(repo, "ghs_abcDEF123")})
	if err != nil || got != "ghs_abcDEF123" {
		t.Fatalf("open: %q %v", got, err)
	}
	// Sealed for another repository: refused (a box cannot be moved to another clone).
	if _, err := openCloneToken(oc, agentproto.GitSource{RepoURL: "https://github.com/acme/other", Token: seal(repo, "ghs_x")}); err == nil {
		t.Error("a token sealed for another repository opened")
	}
	// Sealed to another server: refused.
	otherPub, _, _ := ed25519.GenerateKey(nil)
	box, _ := agentproto.SealBox(base64.StdEncoding.EncodeToString(otherPub), agentproto.GitTokenContext(repo), []byte("ghs_x"))
	if _, err := openCloneToken(oc, agentproto.GitSource{RepoURL: repo, Token: box}); err == nil {
		t.Error("a token sealed to another server opened")
	}
	// Not github.com, or not token-shaped (it reaches a shell-run helper through the environment).
	if _, err := openCloneToken(oc, agentproto.GitSource{RepoURL: "https://gitlab.com/acme/web", Token: seal("https://gitlab.com/acme/web", "ghs_x")}); err == nil {
		t.Error("a token for a non-GitHub repository opened")
	}
	for _, bad := range []string{"ghs_x; rm -rf /", "ghs_$(id)", "a\nb", "a`b`", ""} {
		if _, err := openCloneToken(oc, agentproto.GitSource{RepoURL: repo, Token: seal(repo, bad)}); err == nil {
			t.Errorf("token %q accepted", bad)
		}
	}
	if !strings.Contains(appTokenHelper, "$"+appTokenEnv) || strings.Contains(appTokenHelper, "ghs_") {
		t.Errorf("helper: %s", appTokenHelper)
	}
}
