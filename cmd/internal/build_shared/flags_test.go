package build_shared

import (
	"os/exec"
	"strings"
	"testing"
)

func TestLinkerFlags(t *testing.T) {
	revision, err := exec.Command("git", "rev-parse", "--verify", "HEAD^{commit}").Output()
	if err != nil {
		t.Skip("git checkout is not available")
	}
	for _, debug := range []bool{false, true} {
		flags := LinkerFlags("1.15.0-alpha.13", debug)
		if !strings.Contains(flags, "constant.Version=1.15.0-alpha.13") || !strings.Contains(flags, "constant.Commit="+strings.TrimSpace(string(revision))) {
			t.Errorf("LinkerFlags(debug=%v) = %s", debug, flags)
		}
	}
}
