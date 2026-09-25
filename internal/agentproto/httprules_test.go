package agentproto

import "testing"

// Redirects.

func TestRedirectRuleValidateAccepts(t *testing.T) {
	for _, r := range []RedirectRule{
		{From: "/old", To: "/new"},
		{From: "/old/*", To: "https://example.com/new"},
		{From: "/", To: "https://example.com/", Code: 301},
		{From: "/api", To: "https://example.com/api", Code: 307, PreserveQuery: true},
	} {
		if err := (HTTPRules{Redirects: []RedirectRule{r}}).Validate(); err != nil {
			t.Errorf("expected %+v to be valid, got %v", r, err)
		}
	}
}

func TestRedirectRuleValidateRejects(t *testing.T) {
	cases := []RedirectRule{
		{From: "old", To: "/new"},                              // not absolute
		{From: "/old?x=1", To: "/new"},                         // no query strings in a path matcher
		{From: "/old", To: ""},                                 // empty target
		{From: "/old", To: "javascript:alert(1)"},              // not http/https and not a path
		{From: "/old", To: "ftp://example.com/x"},              // wrong scheme
		{From: "/old", To: "https://example.com/x\r\nEvil: 1"}, // header injection via CRLF
		{From: "/old", To: "/new", Code: 200},                  // not a redirect code
		{From: "/old", To: "not a url and not a path"},
	}
	for _, r := range cases {
		if err := (HTTPRules{Redirects: []RedirectRule{r}}).Validate(); err == nil {
			t.Errorf("expected %+v to be rejected", r)
		}
	}
}

func TestRedirectRuleEffectiveCode(t *testing.T) {
	if got := (RedirectRule{}).EffectiveCode(); got != 302 {
		t.Errorf("default code = %d, want 302", got)
	}
	if got := (RedirectRule{Code: 308}).EffectiveCode(); got != 308 {
		t.Errorf("explicit code = %d, want 308", got)
	}
}

func TestTooManyRedirects(t *testing.T) {
	var rs []RedirectRule
	for i := 0; i <= MaxRedirects; i++ {
		rs = append(rs, RedirectRule{From: "/a", To: "/b"})
	}
	if err := (HTTPRules{Redirects: rs}).Validate(); err == nil {
		t.Error("expected too-many-redirects to be rejected")
	}
}

// Headers.

func TestHeaderRuleValidateAccepts(t *testing.T) {
	for _, h := range []HeaderRule{
		{Action: "set", Name: "Strict-Transport-Security", Value: "max-age=63072000"},
		{Action: "add", Name: "X-Frame-Options", Value: "DENY"},
		{Action: "remove", Name: "Server"},
	} {
		if err := (HTTPRules{Headers: []HeaderRule{h}}).Validate(); err != nil {
			t.Errorf("expected %+v to be valid, got %v", h, err)
		}
	}
}

func TestHeaderRuleValidateRejects(t *testing.T) {
	cases := []HeaderRule{
		{Action: "bogus", Name: "X-Test", Value: "1"},
		{Action: "set", Name: "X: evil", Value: "1"},                          // colon not a valid token char
		{Action: "set", Name: "X-Test\r\nSet-Cookie", Value: "1"},             // CRLF in the name
		{Action: "set", Name: "X-Test", Value: "line1\r\nSet-Cookie: evil=1"}, // header injection via value
		{Action: "set", Name: "", Value: "1"},
	}
	for _, h := range cases {
		if err := (HTTPRules{Headers: []HeaderRule{h}}).Validate(); err == nil {
			t.Errorf("expected %+v to be rejected", h)
		}
	}
}

func TestHeaderRuleRemoveIgnoresValue(t *testing.T) {
	// A "remove" rule's Value is never used by the agent, but must not itself be allowed to smuggle
	// something dangerous through even though Caddy would never render it.
	h := HeaderRule{Action: "remove", Name: "Server", Value: "irrelevant\r\nX: y"}
	if err := (HTTPRules{Headers: []HeaderRule{h}}).Validate(); err != nil {
		t.Errorf("remove should ignore Value's content, got %v", err)
	}
}

// Basic auth.

var okHash = "$2a$10$abcdefghijklmnopqrstuuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ" // 53 chars after cost

func TestBasicAuthRuleValidateAccepts(t *testing.T) {
	b := &BasicAuthRule{Enabled: true, Users: []BasicAuthUser{{Username: "alice", BcryptHash: okHash}}}
	if err := (HTTPRules{BasicAuth: b}).Validate(); err != nil {
		t.Errorf("expected a valid basic auth rule to pass, got %v", err)
	}
}

func TestBasicAuthRuleDisabledSkipsValidation(t *testing.T) {
	// A disabled rule is not validated at all (e.g. it may have been left with stale/incomplete data
	// from before it was turned off).
	b := &BasicAuthRule{Enabled: false}
	if err := (HTTPRules{BasicAuth: b}).Validate(); err != nil {
		t.Errorf("a disabled basic auth rule should never fail validation, got %v", err)
	}
}

func TestBasicAuthRuleRejectsPlaintextLookingValue(t *testing.T) {
	b := &BasicAuthRule{Enabled: true, Users: []BasicAuthUser{{Username: "alice", BcryptHash: "hunter2"}}}
	if err := (HTTPRules{BasicAuth: b}).Validate(); err == nil {
		t.Error("a non-bcrypt value must never pass as a password hash")
	}
}

func TestBasicAuthRuleRejectsNoUsers(t *testing.T) {
	b := &BasicAuthRule{Enabled: true}
	if err := (HTTPRules{BasicAuth: b}).Validate(); err == nil {
		t.Error("enabled basic auth with no users should be rejected")
	}
}

func TestBasicAuthRuleRejectsDuplicateUsernames(t *testing.T) {
	b := &BasicAuthRule{Enabled: true, Users: []BasicAuthUser{
		{Username: "alice", BcryptHash: okHash}, {Username: "alice", BcryptHash: okHash},
	}}
	if err := (HTTPRules{BasicAuth: b}).Validate(); err == nil {
		t.Error("duplicate usernames should be rejected")
	}
}

func TestBasicAuthRuleRejectsBadPathPrefix(t *testing.T) {
	b := &BasicAuthRule{Enabled: true, PathPrefix: "not-absolute",
		Users: []BasicAuthUser{{Username: "alice", BcryptHash: okHash}}}
	if err := (HTTPRules{BasicAuth: b}).Validate(); err == nil {
		t.Error("a non-absolute path prefix should be rejected")
	}
}

func TestBasicAuthRuleRejectsBadExemptPath(t *testing.T) {
	b := &BasicAuthRule{Enabled: true, ExemptPaths: []string{"relative"},
		Users: []BasicAuthUser{{Username: "alice", BcryptHash: okHash}}}
	if err := (HTTPRules{BasicAuth: b}).Validate(); err == nil {
		t.Error("a non-absolute exempt path should be rejected")
	}
}

func TestBasicAuthRuleRejectsBadUsername(t *testing.T) {
	b := &BasicAuthRule{Enabled: true, Users: []BasicAuthUser{{Username: "al ice", BcryptHash: okHash}}}
	if err := (HTTPRules{BasicAuth: b}).Validate(); err == nil {
		t.Error("a username with whitespace should be rejected")
	}
}

func TestTooManyBasicAuthUsers(t *testing.T) {
	var users []BasicAuthUser
	for i := 0; i <= MaxBasicAuthUsers; i++ {
		users = append(users, BasicAuthUser{Username: "u" + string(rune('a'+i%26)) + string(rune(i)), BcryptHash: okHash})
	}
	b := &BasicAuthRule{Enabled: true, Users: users}
	if err := (HTTPRules{BasicAuth: b}).Validate(); err == nil {
		t.Error("too many basic auth users should be rejected")
	}
}

// Access rules (IP / bad bots).

func TestAccessRulesValidateAccepts(t *testing.T) {
	a := &AccessRules{IPAllow: []string{"10.0.0.0/8"}, IPDeny: []string{"192.168.1.1"}, BlockBadBots: true}
	if err := (HTTPRules{Access: a}).Validate(); err != nil {
		t.Errorf("expected valid access rules to pass, got %v", err)
	}
}

func TestAccessRulesValidateRejectsBadCIDR(t *testing.T) {
	for _, a := range []*AccessRules{
		{IPAllow: []string{"not-a-cidr"}},
		{IPDeny: []string{"10.0.0.0/40"}},
		{IPDeny: []string{"; rm -rf /"}},
		{IPAllow: []string{""}},
	} {
		if err := (HTTPRules{Access: a}).Validate(); err == nil {
			t.Errorf("expected %+v to be rejected", a)
		}
	}
}

func TestTooManyIPRules(t *testing.T) {
	var deny []string
	for i := 0; i <= MaxIPRules; i++ {
		deny = append(deny, "10.0.0.0/8")
	}
	if err := (HTTPRules{Access: &AccessRules{IPDeny: deny}}).Validate(); err == nil {
		t.Error("too many IP rules should be rejected")
	}
}

func TestValidCIDRAcceptsBareIP(t *testing.T) {
	if !ValidCIDR("10.0.0.1") {
		t.Error("a bare IPv4 address should be a valid CIDR entry")
	}
	if !ValidCIDR("::1") {
		t.Error("a bare IPv6 address should be a valid CIDR entry")
	}
}

func TestNormalizeCIDR(t *testing.T) {
	if got := NormalizeCIDR("10.0.0.1"); got != "10.0.0.1/32" {
		t.Errorf("NormalizeCIDR(bare IPv4) = %q, want .../32", got)
	}
	if got := NormalizeCIDR("::1"); got != "::1/128" {
		t.Errorf("NormalizeCIDR(bare IPv6) = %q, want .../128", got)
	}
	if got := NormalizeCIDR("10.0.0.0/8"); got != "10.0.0.0/8" {
		t.Errorf("NormalizeCIDR(already a CIDR) = %q, want unchanged", got)
	}
}

// HTTPRulesSetParams / Empty.

func TestHTTPRulesSetParamsValidate(t *testing.T) {
	good := HTTPRulesSetParams{TargetType: HTTPRuleTargetApp, TargetID: "550e8400-e29b-41d4-a716-446655440000", Rules: HTTPRules{}}
	if err := good.Validate(); err != nil {
		t.Errorf("expected valid params to pass, got %v", err)
	}
	bad := HTTPRulesSetParams{TargetType: "bogus", TargetID: "abc123"}
	if err := bad.Validate(); err == nil {
		t.Error("bad target_type should be rejected")
	}
	badID := HTTPRulesSetParams{TargetType: HTTPRuleTargetProxy, TargetID: "../../etc/passwd"}
	if err := badID.Validate(); err == nil {
		t.Error("bad target_id should be rejected")
	}
}

func TestHTTPRulesEmpty(t *testing.T) {
	if !(HTTPRules{}).Empty() {
		t.Error("a zero-value HTTPRules should be Empty")
	}
	if !(HTTPRules{BasicAuth: &BasicAuthRule{Enabled: false}}).Empty() {
		t.Error("a disabled (but present) basic auth rule should still count as Empty")
	}
	if (HTTPRules{Redirects: []RedirectRule{{From: "/a", To: "/b"}}}).Empty() {
		t.Error("a redirect makes HTTPRules non-empty")
	}
	if (HTTPRules{Access: &AccessRules{BlockBadBots: true}}).Empty() {
		t.Error("BlockBadBots makes HTTPRules non-empty")
	}
}
