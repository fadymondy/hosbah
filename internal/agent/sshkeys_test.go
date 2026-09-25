package agent

import "testing"

const testEd25519Key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBltbBb6xxYQ3xzz7C+3qXjBXA6R+6YkKn6f4qz1L4nQ test@host"

func TestUpsertAuthorizedKeyAddsOnEmpty(t *testing.T) {
	marker := "# hosbah-ssh-key:SHA256:abc"
	next, changed := upsertAuthorizedKey("", marker, testEd25519Key)
	if !changed {
		t.Fatal("want changed")
	}
	want := marker + "\n" + testEd25519Key + "\n"
	if next != want {
		t.Fatalf("got %q want %q", next, want)
	}
}

func TestUpsertAuthorizedKeyIdempotent(t *testing.T) {
	marker := "# hosbah-ssh-key:SHA256:abc"
	first, _ := upsertAuthorizedKey("ssh-rsa AAAA someone-elses-key\n", marker, testEd25519Key)
	second, changed := upsertAuthorizedKey(first, marker, testEd25519Key)
	if changed {
		t.Fatal("re-install of the same key should not change anything")
	}
	if second != first {
		t.Fatalf("got %q want %q", second, first)
	}
	if got := countOccurrences(second, testEd25519Key); got != 1 {
		t.Fatalf("key appears %d times, want 1", got)
	}
}

func TestUpsertAuthorizedKeyNeverTouchesUnmarkedLines(t *testing.T) {
	hosbahEnrollmentKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHosbahOwnEnrollmentKeyLine=="
	original := hosbahEnrollmentKey + "\n# some human comment\nssh-rsa AAAA human-added-key\n"
	marker := "# hosbah-ssh-key:SHA256:abc"
	next, changed := upsertAuthorizedKey(original, marker, testEd25519Key)
	if !changed {
		t.Fatal("want changed")
	}
	if !hasSubstring(next, hosbahEnrollmentKey) || !hasSubstring(next, "human-added-key") {
		t.Fatalf("unmarked lines were touched: %q", next)
	}

	// Uninstall must not remove the unmarked lines either.
	removed, changed := removeAuthorizedKey(next, marker)
	if !changed {
		t.Fatal("want changed")
	}
	if !hasSubstring(removed, hosbahEnrollmentKey) || !hasSubstring(removed, "human-added-key") {
		t.Fatalf("uninstall touched an unmarked line: %q", removed)
	}
	if hasSubstring(removed, testEd25519Key) {
		t.Fatalf("uninstall left the marked key behind: %q", removed)
	}
}

func TestRemoveAuthorizedKeyNoop(t *testing.T) {
	marker := "# hosbah-ssh-key:SHA256:abc"
	content := "ssh-rsa AAAA someone-elses-key\n"
	next, changed := removeAuthorizedKey(content, marker)
	if changed || next != content {
		t.Fatalf("nothing to remove: got changed=%v next=%q", changed, next)
	}
}

func TestUpsertThenRemoveRoundTrips(t *testing.T) {
	marker := "# hosbah-ssh-key:SHA256:abc"
	base := "ssh-rsa AAAA base-key\n"
	installed, _ := upsertAuthorizedKey(base, marker, testEd25519Key)
	removed, changed := removeAuthorizedKey(installed, marker)
	if !changed {
		t.Fatal("want changed")
	}
	if removed != base {
		t.Fatalf("got %q want %q", removed, base)
	}
}

func hasSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func countOccurrences(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
			i += len(sub) - 1
		}
	}
	return n
}
