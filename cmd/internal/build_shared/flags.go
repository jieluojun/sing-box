package build_shared

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func LinkerFlags(version string, debug bool) string {
	flags := []string{
		"-X github.com/sagernet/sing-box/constant.Version=" + version,
		"-X github.com/sagernet/sing-box/constant.BuildTime=" + buildTimestamp(),
		"-X runtime.godebugDefault=multipathtcp=0,tlssha1=1",
		"-checklinkname=0",
	}
	// Both libbox and boxdd disable automatic VCS stamping, so carry the
	// checked-out source revision separately from their semantic version.
	if revision, err := exec.Command("git", "rev-parse", "--verify", "HEAD^{commit}").Output(); err == nil {
		flags = append(flags, "-X github.com/sagernet/sing-box/constant.Commit="+strings.TrimSpace(string(revision)))
	}
	if !debug {
		flags = append(flags, "-s", "-w", "-buildid=")
	}
	return strings.Join(flags, " ")
}

// SOURCE_DATE_EPOCH preserves reproducible builds when the build environment
// explicitly supplies a fixed timestamp. Otherwise record the current time.
func buildTimestamp() string {
	if value := os.Getenv("SOURCE_DATE_EPOCH"); value != "" {
		if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
			return strconv.FormatInt(seconds, 10)
		}
	}
	return strconv.FormatInt(time.Now().Unix(), 10)
}
