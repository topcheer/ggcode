//go:build darwin

package tool

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func TestIssue4482_NegativeLinesNormalized(t *testing.T) {
	tool := &MobileDeviceTool{}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"logs","lines":-5}`))
	if err != nil {
		t.Fatalf("negative lines must not hard-error the tool, got err %v", err)
	}
	if !res.IsError {
		t.Logf("no-device environment: result content = %q", res.Content)
	}
}

func TestIssue4482_DefaultDeviceConcurrent(t *testing.T) {
	b := &iosBackend{xcrunPath: "/usr/bin/false", cachedUDID: "PRE-SEEDED-UDID"}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := b.defaultDevice(); got != "PRE-SEEDED-UDID" {
				t.Errorf("cached UDID must win, got %q", got)
			}
		}()
	}
	wg.Wait()
}
