package wailskit

import (
	"testing"
)

func TestGetUsageInfoNoConfig(t *testing.T) {
	res := (&ChatBridge{}).GetUsageInfo()
	if res.Error == "" {
		t.Fatalf("expected soft error for empty config, got %+v", res)
	}
	if res.Vendor != "" && res.Error == "" {
		t.Fatalf("unexpected vendor resolution without config: %+v", res)
	}
}
