package main

import (
	"fmt"
	"strconv"
	"time"
)

// formatVersionHeader keeps the full build information on the first line for
// launchers that only display the first line of the version command.
func formatVersionHeader(version, goos, goarch, goVersion, buildTime string) string {
	stamp := "build time unknown"
	if seconds, err := strconv.ParseInt(buildTime, 10, 64); err == nil && seconds >= 0 && seconds <= 253402300799 {
		stamp = time.Unix(seconds, 0).UTC().Format("Mon Jan 2 15:04:05 MST 2006")
	}
	return fmt.Sprintf("sing-box version %s %s %s with %s %s", version, goos, goarch, goVersion, stamp)
}
