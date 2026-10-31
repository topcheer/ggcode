package im

import (
	"os"
	"strings"
	"testing"
)

// #4698: the #4644 key-retry wait must be ctx-aware - a raw Sleep held the
// serial syncer pipeline up to 2.4s per bad event even after cancellation,
// stalling every room behind it.
func TestIssue4698_KeyRetryCtxAware(t *testing.T) {
	data, err := os.ReadFile("matrix_adapter.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	i := strings.Index(text, "key-retry aborted by ctx cancellation")
	if i < 0 {
		t.Fatal("ctx cancellation abort missing from the retry loop (#4698)")
	}
	// The select must sit INSIDE the retry loop, before the decrypt call.
	region := text[max(0, i-600) : i+400]
	if !strings.Contains(region, "case <-ctx.Done():") || !strings.Contains(region, "case <-time.After(800 * time.Millisecond):") {
		t.Fatal("retry wait must be a select over ctx.Done and time.After (#4698)")
	}
	if !strings.Contains(region, "for attempt := 0; attempt < 3") {
		t.Fatal("bounded retry loop shape must be preserved (#4644 semantics)")
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
