package constant

import (
	"runtime/debug"
	"strings"
)

var Version = "unknown"

// Commit may be set via -ldflags when VCS build information is disabled.
var Commit string

func init() {
	Version = strings.TrimSuffix(Version, "-with-at")
	if !strings.HasSuffix(Version, "-zero") {
		Version += "-zero"
	}
}

// PanelVersion adds the source revision to the Clash API version without
// changing Version, which is also used by clients and other APIs.
func PanelVersion() string {
	commit := Commit
	if commit == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			commit = sourceRevision(info)
		}
	}
	return panelVersion(Version, commit)
}

// A binary embedding sing-box as a dependency can have its own VCS revision;
// only use the main module's revision when it is the sing-box source itself.
func sourceRevision(info *debug.BuildInfo) string {
	if info == nil || info.Main.Path != "github.com/sagernet/sing-box" {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return ""
}

func panelVersion(version, commit string) string {
	commit = strings.ToLower(commit)
	if len(commit) < 9 || strings.Trim(commit, "0123456789abcdef") != "" {
		return version
	}
	base, zero := strings.CutSuffix(version, "-zero")
	// Some builds already put a short git hash at the end of the version.
	// Replace it with the consistent nine-character hash instead of duplicating it.
	if index := strings.LastIndexByte(base, '-'); index >= 0 {
		previous := strings.ToLower(base[index+1:])
		if len(previous) >= 7 && strings.HasPrefix(commit, previous) {
			base = base[:index]
		}
	}
	base += "-" + commit[:9]
	if zero {
		base += "-zero"
	}
	return base
}
