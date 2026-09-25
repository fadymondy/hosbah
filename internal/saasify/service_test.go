package saasify

import "testing"

func TestValidSlug(t *testing.T) {
	valid := []string{"a", "alpha", "alpha-product", "a1-2b", "abcdefghijklmnopqrstuvwxyz012345"} // 32 chars
	for _, s := range valid {
		if !validSlug(s) {
			t.Errorf("validSlug(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"",                                 // empty
		"-leading-dash",                    // must not start with a dash
		"Has-Upper",                        // must be lowercase
		"has space",                        // no spaces
		"has_underscore",                   // no underscores
		"abcdefghijklmnopqrstuvwxyz0123456", // 33 chars, too long
		"emoji😀",
	}
	for _, s := range invalid {
		if validSlug(s) {
			t.Errorf("validSlug(%q) = true, want false", s)
		}
	}
}

func TestClip(t *testing.T) {
	if got := clip("short", 10); got != "short" {
		t.Errorf("clip(short, 10) = %q, want unchanged", got)
	}
	if got := clip("a-very-long-app-name", 8); got != "a-very-l" {
		t.Errorf("clip(...) = %q, want the first 8 bytes", got)
	}
	if got := clip("", 5); got != "" {
		t.Errorf("clip(\"\", 5) = %q, want empty", got)
	}
}

func TestNewUUIDLooksLikeAUUIDAndIsUnique(t *testing.T) {
	a, b := newUUID(), newUUID()
	if a == b {
		t.Fatal("newUUID produced the same id twice")
	}
	for _, id := range []string{a, b} {
		if len(id) != 36 {
			t.Fatalf("newUUID() = %q, want 36 characters", id)
		}
		if id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
			t.Fatalf("newUUID() = %q, dashes are not where a UUID puts them", id)
		}
		if id[14] != '4' {
			t.Fatalf("newUUID() = %q, want a version-4 UUID (14th char '4')", id)
		}
	}
}
