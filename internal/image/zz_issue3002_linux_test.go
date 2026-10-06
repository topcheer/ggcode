//go:build linux

package image

// Regression probe for #3002: when every candidate screenshot tool fails, a
// pre-existing stale file at rawPath (e.g. an old capture at the same
// OutputPath) must NOT be mistaken for this round's output. The old gate
// `lastErr != nil && !fileExists(rawPath)` swallowed the tool error whenever
// the stale file existed and finalized the OLD image as success (#1259 mode).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue3002_AllToolsFailStaleFileMustError pins the regression: all
// candidates fail while rawPath already exists (stale bytes from a previous
// run) -> the helper must report failure, never success.
func TestIssue3002_AllToolsFailStaleFileMustError(t *testing.T) {
	rawPath := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(rawPath, []byte("stale-bytes-from-previous-run"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Unknown tool name exercises the `default` failure branch; the stale
	// file at rawPath is exactly what the old gate keyed on.
	succeeded, lastErr := runLinuxToolCandidates([]string{"probe-3002-no-such-tool"}, rawPath, ScreenshotOptions{})
	if succeeded {
		t.Fatal("#3002: unknown tool must never report success")
	}
	if lastErr == nil {
		t.Fatal("#3002: all tools failed but lastErr is nil; stale file would leak through")
	}
	if !strings.Contains(lastErr.Error(), "unsupported screenshot tool") {
		t.Fatalf("expected unsupported-tool error, got: %v", lastErr)
	}
}

// TestIssue3002_UnsupportedToolErrorNames pins the actionable error shape.
func TestIssue3002_UnsupportedToolErrorNames(t *testing.T) {
	_, err := runLinuxToolCandidates([]string{"definitely-not-grim"}, "/tmp/x3002.png", ScreenshotOptions{})
	if err == nil || !strings.Contains(err.Error(), "definitely-not-grim") {
		t.Fatalf("error must name the unsupported tool, got: %v", err)
	}
}
