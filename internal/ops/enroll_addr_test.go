package ops

import "testing"

func TestServerAddressPrefersReported(t *testing.T) {
	for _, c := range []struct{ reported, peer, want string }{
		{"10.10.10.120", "10.10.10.100", "10.10.10.120"},
		{"", "10.10.10.100", "10.10.10.100"},
		{"127.0.0.1", "203.0.113.5", "203.0.113.5"},
		{"not-an-ip", "203.0.113.5", "203.0.113.5"},
		{"0.0.0.0", "203.0.113.5", "203.0.113.5"},
	} {
		if got := serverAddress(c.reported, c.peer); got != c.want {
			t.Errorf("serverAddress(%q, %q) = %q, want %q", c.reported, c.peer, got, c.want)
		}
	}
}
