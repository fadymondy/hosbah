package agentproto

import "testing"

// ValidBuildPath (MH-579 gap B) gates dockerfile_path/build_context before they are ever saved,
// and again once the agent can resolve the checkout's real path.
func TestValidBuildPath(t *testing.T) {
	for _, s := range []string{
		"Dockerfile", "docker/Dockerfile", "build/context", "a.b-c_d/e.f", "x",
	} {
		if !ValidBuildPath(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []string{
		"", "..", "../etc/passwd", "docker/../../etc/passwd", "/etc/passwd", "/Dockerfile",
		"docker\\Dockerfile", "a b/c", "a;rm -rf /", "a$(whoami)", "a`whoami`",
		string(make([]byte, 201)), // too long (all NUL bytes, also fails the charset)
	} {
		if ValidBuildPath(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}
