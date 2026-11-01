package build_shared

import (
	"os/exec"
	"strings"
)

func LinkerFlags(version string, debug bool) string {
	flags := []string{
		"-X github.com/sagernet/sing-box/constant.Version=" + version,
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
