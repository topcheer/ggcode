//go:build darwin

package tool

// Regression test for GitHub issue #4482:
// - Bug 1: logs lines < 1 (LLM-generated negative or JSON typo) passed
//   through unnormalized and made the tail-trim slice underflow panic in
//   both backends. Execute must normalize before touching a backend.
// - Bug 2: iosBackend.cachedUDID was read/written without a lock while
//   parallel tool-execution batches can call defaultDevice concurrently -
//   a Go memory model violation caught by -race. The mutex must hold.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func TestIssue4482_NegativeLinesNormalized(t *testing.T) {
	tool := &MobileDeviceTool{}
	// No backend/device available in CI: the call must still terminate with
	// an error Result instead of panicking on the negative slice bound.
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
