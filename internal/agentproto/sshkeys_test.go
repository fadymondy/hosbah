package agentproto

import "testing"

const testEd25519Key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBltbBb6xxYQ3xzz7C+3qXjBXA6R+6YkKn6f4qz1L4nQ test@host"

func TestParseAuthorizedKeyEd25519(t *testing.T) {
	typ, blob, err := ParseAuthorizedKey(testEd25519Key)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "ed25519" {
		t.Fatalf("got %s", typ)
	}
	if len(blob) == 0 {
		t.Fatal("empty blob")
	}
}

func TestParseAuthorizedKeyRejectsPrivateKey(t *testing.T) {
	if _, _, err := ParseAuthorizedKey("-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"); err == nil {
		t.Fatal("want error")
	}
}

func TestParseAuthorizedKeyRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "not-a-key", "ssh-ed25519", "ssh-ed25519 not-base64!!"} {
		if _, _, err := ParseAuthorizedKey(s); err == nil {
			t.Fatalf("want error for %q", s)
		}
	}
}

func TestParseAuthorizedKeyMismatchedType(t *testing.T) {
	// The declared algorithm ("ssh-rsa") does not match what's encoded in the blob (ed25519's).
	fields := "ssh-rsa AAAAC3NzaC1lZDI1NTE5AAAAIBltbBb6xxYQ3xzz7C+3qXjBXA6R+6YkKn6f4qz1L4nQ"
	if _, _, err := ParseAuthorizedKey(fields); err == nil {
		t.Fatal("want error: type does not match blob")
	}
}

func TestFingerprintStable(t *testing.T) {
	f1 := Fingerprint(testEd25519Key)
	f2 := Fingerprint(testEd25519Key + " a-different-comment")
	if f1 == "" {
		t.Fatal("empty fingerprint")
	}
	if f1 != f2 {
		t.Fatalf("fingerprint should ignore the comment: %s != %s", f1, f2)
	}
	if f1[:7] != "SHA256:" {
		t.Fatalf("got %s", f1)
	}
}

func TestFingerprintInvalid(t *testing.T) {
	if f := Fingerprint("garbage"); f != "" {
		t.Fatalf("got %s want empty", f)
	}
}

func TestValidUnixUser(t *testing.T) {
	valid := []string{"root", "deploy", "app_1", "a"}
	invalid := []string{"", "Root", "1abc", "-abc", "has space", "toolongtoolongtoolongtoolongtoolong"}
	for _, u := range valid {
		if !ValidUnixUser(u) {
			t.Errorf("%q should be valid", u)
		}
	}
	for _, u := range invalid {
		if ValidUnixUser(u) {
			t.Errorf("%q should be invalid", u)
		}
	}
}

func TestSSHKeyInstallParamsValidate(t *testing.T) {
	fp := Fingerprint(testEd25519Key)
	good := SSHKeyInstallParams{User: "root", PublicKey: testEd25519Key, Fingerprint: fp}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.Fingerprint = "SHA256:wrong"
	if err := bad.Validate(); err == nil {
		t.Fatal("want error: fingerprint mismatch")
	}
	bad2 := good
	bad2.User = "Root Admin"
	if err := bad2.Validate(); err == nil {
		t.Fatal("want error: bad user")
	}
}
