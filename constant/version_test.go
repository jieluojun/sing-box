package constant

import (
	"runtime/debug"
	"testing"
)

func TestPanelVersion(t *testing.T) {
	const commit = "820679ca49caa62bc0e3e631dd351289cccb2140"
	tests := []struct{ version, commit, want string }{
		{"1.15.0-alpha.13-zero", commit, "1.15.0-alpha.13-820679ca4-zero"},
		{"1.15.0-alpha.13-820679c-zero", commit, "1.15.0-alpha.13-820679ca4-zero"},
		{"1.15.0-alpha.13-820679ca4-zero", commit, "1.15.0-alpha.13-820679ca4-zero"},
		{"1.15.0-zero", commit, "1.15.0-820679ca4-zero"},
		{"1.15.0", commit, "1.15.0-820679ca4"},
		{"1.15.0-alpha.13-zero", "", "1.15.0-alpha.13-zero"},
		{"1.15.0-alpha.13-zero", "not-a-hash", "1.15.0-alpha.13-zero"},
		{"1.15.0-alpha.13-zero", "820679c", "1.15.0-alpha.13-zero"},
	}
	for _, tt := range tests {
		if got := panelVersion(tt.version, tt.commit); got != tt.want {
			t.Errorf("panelVersion(%q, %q) = %q, want %q", tt.version, tt.commit, got, tt.want)
		}
	}
}

func TestPanelVersionSourceRevision(t *testing.T) {
	info := &debug.BuildInfo{
		Main:     debug.Module{Path: "github.com/sagernet/sing-box"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "820679ca49caa62bc0e3e631dd351289cccb2140"}},
	}
	if got := sourceRevision(info); got != info.Settings[0].Value {
		t.Fatalf("sourceRevision = %q", got)
	}
	info.Main.Path = "example.com/other-app"
	if got := sourceRevision(info); got != "" {
		t.Fatalf("external application's revision must not be used: %q", got)
	}
	if got := sourceRevision(nil); got != "" {
		t.Fatalf("nil build info: %q", got)
	}
}
