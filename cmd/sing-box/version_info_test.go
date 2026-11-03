package main

import "testing"

func TestVersionHeader(t *testing.T) {
	got := formatVersionHeader("1.15.0-alpha.13-820679ca4-zero", "android", "arm64", "go1.26.10", "1792847141")
	want := "sing-box version 1.15.0-alpha.13-820679ca4-zero android arm64 with go1.26.10 Sat Oct 24 13:05:41 UTC 2026"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	for _, value := range []string{"", "bad", "-1", "9223372036854775807"} {
		got = formatVersionHeader("1.15.0-zero", "linux", "amd64", "go1.26", value)
		want = "sing-box version 1.15.0-zero linux amd64 with go1.26 build time unknown"
		if got != want {
			t.Errorf("timestamp %q: got %q, want %q", value, got, want)
		}
	}
}
