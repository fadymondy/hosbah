package agent

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// testOpContext is an OpContext that only needs to run real commands and collect log lines.
func testOpContext(t *testing.T) (*OpContext, *[]string) {
	t.Helper()
	var lines []string
	return &OpContext{
		Ctx:  context.Background(),
		OpID: "test-op",
		log:  func(_, line string) { lines = append(lines, line) },
		progress: func(int, int) {
		},
	}, &lines
}

/*
Regression for the dpkg file conflict that failed the first deploy onto a Debian 13 server
carrying Docker CE: Debian's docker-buildx and Docker's own docker-buildx-plugin both own
/usr/libexec/docker/cli-plugins/docker-buildx, so apt-get exits 100. dpkg-query says the
package is missing (it is — under that name), but the capability is there.

satisfiedPackages must drop a package whose probe command already succeeds.
*/
func TestSatisfiedPackagesDropsWorkingCapability(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("probes are POSIX commands")
	}
	oc, lines := testOpContext(t)

	// "git" is chosen because the probe (`git --version`) is available wherever these tests run.
	got := satisfiedPackages(oc, []string{"git"})
	if len(got) != 0 {
		t.Fatalf("git works here, so it must not be handed to apt-get; got %v", got)
	}
	if !strings.Contains(strings.Join(*lines, "\n"), "already works") {
		t.Errorf("the skip should be explained in the operation log, got %v", *lines)
	}
}

// A package with no capability probe, or one whose probe fails, still has to be installed —
// the filter must not swallow genuinely missing tools.
func TestSatisfiedPackagesKeepsGenuinelyMissing(t *testing.T) {
	oc, _ := testOpContext(t)

	// No entry in buildCapability at all: nothing is known about it, so it passes through.
	if got := satisfiedPackages(oc, []string{"ca-certificates"}); len(got) != 1 {
		t.Fatalf("a package with no probe must pass through, got %v", got)
	}

	// A probe that cannot possibly succeed must leave its package in the list.
	const fake = "cloudy-test-absent-tool"
	buildCapability[fake] = []string{"cloudy-test-absent-tool-binary", "--version"}
	t.Cleanup(func() { delete(buildCapability, fake) })
	if got := satisfiedPackages(oc, []string{fake}); len(got) != 1 || got[0] != fake {
		t.Fatalf("a failing probe must keep its package, got %v", got)
	}
}

// Every package build.tools installs should have a probe, otherwise the conflict above can
// come back for the other one.
func TestBuildPackagesAllHaveCapabilityProbes(t *testing.T) {
	for _, p := range buildPackages {
		if _, ok := buildCapability[p]; !ok {
			t.Errorf("buildPackages has %q with no entry in buildCapability", p)
		}
	}
}
