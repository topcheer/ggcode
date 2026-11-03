package main

import "testing"

func TestUnbindBelongsToWorkspace(t *testing.T) {
	const daemonWS = "/ggcode"
	cases := []struct {
		name, binding string
		want          bool
	}{
		{"exact own workspace", "/ggcode", true},
		{"other workspace untouched", "/otherproj", false},
		{"legacy empty workspace is ours", "", true},
		{"different path same tail", "/volumes/new/ggcode", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := unbindBelongsToWorkspace(tc.binding, daemonWS); got != tc.want {
				t.Fatalf("unbindBelongsToWorkspace(%q, %q) = %v, want %v", tc.binding, daemonWS, got, tc.want)
			}
		})
	}
}
