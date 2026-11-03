package tool

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestIssue4901_EmptyDeviceGuardAtDispatch(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("iOS backend is darwin-only")
	}
	tool := &MobileDeviceTool{ios: &iosBackend{}}

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

			t.Skipf("a booted iOS simulator exists on this host; guard not reachable for %s", action)
		}
		if !strings.Contains(res.Content, "no connected/booted device") {
			t.Fatalf("%s: expected the #4901 guard message, got: %s", action, res.Content)
		}

		if strings.Contains(res.Content, "Invalid device") {
			t.Fatalf("%s: guard failed, simctl error leaked: %s", action, res.Content)
		}
	}
}
