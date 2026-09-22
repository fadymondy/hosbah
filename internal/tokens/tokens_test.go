package tokens

import (
	"strings"
	"testing"
)

// New tokens get the hb*_ prefixes; tokens minted before the rename (cld_, cla_, cle_, cdh_) are
// still recognised as their family, and no prefix is accepted by another family.
func TestPrefixesNewAndPreRename(t *testing.T) {
	families := map[string][]string{"user": UserMCP, "admin": AdminMCP, "enrol": Enrol, "hook": Hook}
	cases := map[string]string{
		"hbu_0123": "user", "cld_0123": "user",
		"hba_0123": "admin", "cla_0123": "admin",
		"hbe_0123": "enrol", "cle_0123": "enrol",
		"hbh_0123": "hook", "cdh_0123": "hook",
		"xyz_0123": "",
	}
	for tok, want := range cases {
		for name, fam := range families {
			if got := HasPrefix(tok, fam); got != (name == want) {
				t.Errorf("HasPrefix(%s, %s) = %v", tok, name, got)
			}
		}
	}
	for _, p := range []string{PrefixUserMCP, PrefixAdminMCP, PrefixEnrol, PrefixHook} {
		plain, hash := New(p)
		if !strings.HasPrefix(plain, p) || Hash(plain) != hash || !strings.HasPrefix(p, "hb") {
			t.Errorf("New(%s) = %s", p, plain)
		}
	}
	// The prefix is part of what is hashed: an old token only matches its own stored hash.
	if Hash("cld_abc") == Hash("hbu_abc") {
		t.Error("a re-prefixed token must not match")
	}
}
