package oauthstate

import "testing"

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/en/t/team":               "/en/t/team",
		"/ar?x=1#y":                "/ar?x=1#y",
		"/":                        "/",
		"":                         "",
		"//evil.example":           "",
		`/\evil.example`:           "",
		"https://evil.example/":    "",
		"javascript:alert(1)":      "",
		"evil.example":             "",
		"/%2F%2Fevil.example":      "",
		"/%5cevil.example":         "",
		`/en/\x`:                   "",
		"/en/\r\nSet-Cookie: x=1":  "",
		"/en" + string(rune(0)):    "",
		"/en" + string(rune(0x7f)): "",
	} {
		if got := SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}
