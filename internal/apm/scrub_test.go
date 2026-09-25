package apm

import "testing"

func TestIsSensitiveKey(t *testing.T) {
	cases := map[string]bool{
		"password":           true,
		"user.password":      true,
		"Authorization":      true,
		"http.authorization": true,
		"api_token":          true,
		"secret_key":         true,
		"cookie":             true,
		"Set-Cookie":         true,
		"http.route":         false,
		"custom.safe":        false,
		"http.status_code":   false,
	}
	for k, want := range cases {
		if got := isSensitiveKey(k); got != want {
			t.Errorf("isSensitiveKey(%q) = %v, want %v", k, got, want)
		}
	}
}

func TestScrubAttrs_DropsSensitiveAndCapsSize(t *testing.T) {
	in := map[string]any{
		"password": "hunter2",
		"token":    "abc",
		"safe":     "ok",
		"long":     stringOfLen(maxAttrValueLen + 100),
	}
	out := scrubAttrs(in)
	if _, ok := out["password"]; ok {
		t.Fatal("password should be dropped")
	}
	if _, ok := out["token"]; ok {
		t.Fatal("token should be dropped")
	}
	if out["safe"] != "ok" {
		t.Fatalf("safe value should survive: %+v", out)
	}
	if s, ok := out["long"].(string); !ok || len(s) != maxAttrValueLen {
		t.Fatalf("long value should be capped to %d, got %v", maxAttrValueLen, out["long"])
	}
}

func TestScrubAttrs_CapsCount(t *testing.T) {
	in := map[string]any{}
	for i := 0; i < maxAttrs+10; i++ {
		in[stringOfLen(1)+string(rune('a'+i%26))+string(rune(i))] = i
	}
	out := scrubAttrs(in)
	if len(out) > maxAttrs {
		t.Fatalf("expected at most %d attrs, got %d", maxAttrs, len(out))
	}
}

func TestScrubAttrs_EmptyInputIsNil(t *testing.T) {
	if scrubAttrs(nil) != nil {
		t.Fatal("expected nil for empty input")
	}
	if scrubAttrs(map[string]any{}) != nil {
		t.Fatal("expected nil for empty map")
	}
}

func stringOfLen(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}
