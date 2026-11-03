package build_shared

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestBuildTimestamp(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1792847141")
	if got := buildTimestamp(); got != "1792847141" {
		t.Fatalf("SOURCE_DATE_EPOCH: got %q", got)
	}
	if flags := LinkerFlags("1.15.0", true); !strings.Contains(flags, "constant.BuildTime=1792847141") {
		t.Fatalf("missing timestamp in flags: %s", flags)
	}
	t.Setenv("SOURCE_DATE_EPOCH", "invalid")
	before := time.Now().Unix()
	value, err := strconv.ParseInt(buildTimestamp(), 10, 64)
	if err != nil || value < before || value > time.Now().Unix() {
		t.Fatalf("invalid SOURCE_DATE_EPOCH should use current time: %d, %v", value, err)
	}
}
