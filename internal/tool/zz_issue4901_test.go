package tool

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

// TestIssue4901_EmptyDeviceGuardAtDispatch verifies that when no device
// resolves (neither passed nor found by the backend default), every
// device-requiring action fails with an actionable guard message at dispatch
// instead of exec'ing simctl/adb with a bare empty-string UDID.
func TestIssue4901_EmptyDeviceGuardAtDispatch(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("iOS backend is darwin-only")
	}
	tool := &MobileDeviceTool{ios: &iosBackend{}}

	// The machine under test has no booted simulator (or xcrun absent) ->
	// defaultDevice() returns "" for all three failure modes (#4901).
	for _, action := range []string{"launch", "screenshot", "list_apps", "logs"} {
		input, _ := json.Marshal(map[string]any{
			"action":      action,
			"platform":    "ios",
			"bundle_id":   "com.example.app",
			"description": "probe",
		})
		res, err := tool.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", action, err)
		}
		if !res.IsError {
			// A simulator IS booted on this machine: defaultDevice resolved a
			// real UDID, so the guard correctly did not fire. Env-dependent,
			// not a regression.
			t.Skipf("a booted iOS simulator exists on this host; guard not reachable for %s", action)
		}
		if !strings.Contains(res.Content, "no connected/booted device") {
			t.Fatalf("%s: expected the #4901 guard message, got: %s", action, res.Content)
		}
		// The misleading simctl error must NOT be what the agent sees.
		if strings.Contains(res.Content, "Invalid device") {
			t.Fatalf("%s: guard failed, simctl error leaked: %s", action, res.Content)
		}
	}
}
