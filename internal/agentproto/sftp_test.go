package agentproto

import "testing"

const testSFTPID = "00000000-0000-4000-8000-0000000000f1"

func TestSFTPUserValidateCreate(t *testing.T) {
	base := SFTPUserParams{ID: testSFTPID, Action: "create", Username: "alice", RootDir: "/srv/uploads",
		PublicKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBogus alice@laptop"}}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid create rejected: %v", err)
	}

	noWay := base
	noWay.PublicKeys = nil
	if noWay.Validate() == nil {
		t.Fatal("create needs a public key or a password")
	}

	pwOnly := base
	pwOnly.PublicKeys = nil
	pwOnly.PasswordSecret = "cab1:sealed"
	if err := pwOnly.Validate(); err != nil {
		t.Fatalf("password-only create rejected: %v", err)
	}

	badUser := base
	badUser.Username = "Alice-Not-Lowercase"
	if badUser.Validate() == nil {
		t.Fatal("uppercase username must be rejected")
	}

	badAction := base
	badAction.Action = "destroy"
	if badAction.Validate() == nil {
		t.Fatal("unknown action must be rejected")
	}

	badID := base
	badID.ID = "not-a-uuid"
	if badID.Validate() == nil {
		t.Fatal("bad id must be rejected")
	}

	relRoot := base
	relRoot.RootDir = "srv/uploads"
	if relRoot.Validate() == nil {
		t.Fatal("a relative root dir must be rejected")
	}

	tooManyKeys := base
	keys := make([]string, MaxSFTPPublicKeys+1)
	for i := range keys {
		keys[i] = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBogus k" + string(rune('a'+i))
	}
	tooManyKeys.PublicKeys = keys
	if tooManyKeys.Validate() == nil {
		t.Fatal("more than MaxSFTPPublicKeys keys must be rejected")
	}

	badKey := base
	badKey.PublicKeys = []string{"not an ssh key"}
	if badKey.Validate() == nil {
		t.Fatal("a bad key must be rejected")
	}

	multiline := base
	multiline.PublicKeys = []string{"ssh-ed25519 AAAA\nssh-ed25519 BBBB"}
	if multiline.Validate() == nil {
		t.Fatal("a multi-line key field must be rejected (authorized_keys injection)")
	}
}

func TestSFTPUserValidateDelete(t *testing.T) {
	// delete needs only id, action and username — no root dir, no key, no password.
	del := SFTPUserParams{ID: testSFTPID, Action: "delete", Username: "alice"}
	if err := del.Validate(); err != nil {
		t.Fatalf("valid delete rejected: %v", err)
	}
	noUser := SFTPUserParams{ID: testSFTPID, Action: "delete"}
	if noUser.Validate() == nil {
		t.Fatal("delete still needs a username")
	}
}

func TestSFTPSecretContextBindsID(t *testing.T) {
	if SFTPSecretContext("a") == SFTPSecretContext("b") {
		t.Fatal("SFTPSecretContext must bind to the sftp user's own id")
	}
}
