package constant

import "strings"

var Version = "unknown"

func init() {
	Version = strings.TrimSuffix(Version, "-with-at")
	if !strings.HasSuffix(Version, "-zero") {
		Version += "-zero"
	}
}
