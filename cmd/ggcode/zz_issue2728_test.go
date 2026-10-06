package main

// #2728 regression: WebUI unbind must only delete THIS daemon's bindings.
//
// Scenario (issue): im_bindings.json holds qq bound to /otherproj (a remote
// daemon's, in use) and /ggcode (this daemon). The old unbind deleted the
// FIRST adapter-name match - with Go map iteration order, that could be
// /otherproj, silently killing the remote workspace's binding.
//
// unbindBelongsToWorkspace encodes the ownership decision: exact workspace
// match, or legacy entries with an empty workspace (pre-v1.3.84 predates
// workspace scoping - only whichever daemon encounters them can manage them).

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
		{"different path same tail", "/volumes/new/ggcode", false}, // exact match, not prefix
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := unbindBelongsToWorkspace(tc.binding, daemonWS); got != tc.want {
				t.Fatalf("unbindBelongsToWorkspace(%q, %q) = %v, want %v", tc.binding, daemonWS, got, tc.want)
			}
		})
	}
}
