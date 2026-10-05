package memory

import (
	"strings"
	"testing"
)

// STALE / Implicit Conflict (arXiv:2605.06527): a single stale entry with no
// live contradicting sibling bypasses ArbitrateInline and must still be
// flagged at injection time instead of serving as an unflagged false premise.

func TestAnnotateStaleInlineBrokenPath(t *testing.T) {
	workingDir := t.TempDir()
	am := &AutoMemory{dir: t.TempDir(), projectRoot: workingDir}

	inline := []MemoryEntry{{
		Key:     "stale-entry",
		Content: "Use the config loader from internal/config/loader.go to parse settings.",
	}}
	am.annotateStaleInline(inline)

	if !strings.Contains(inline[0].Content, "[memory-stale]") {
		t.Errorf("expected [memory-stale] annotation, got: %q", inline[0].Content)
	}
	if !strings.Contains(inline[0].Content, "verify against the current workspace") {
		t.Error("annotation should instruct verification")
	}
}

func TestAnnotateStaleInlineValidPath(t *testing.T) {
	workingDir := t.TempDir()
	am := &AutoMemory{dir: t.TempDir(), projectRoot: workingDir}

	inline := []MemoryEntry{{Key: "ok", Content: "no path references here at all"}}
	am.annotateStaleInline(inline)

	if inline[0].Content != "no path references here at all" {
		t.Errorf("expected clean content, got: %q", inline[0].Content)
	}
}

func TestAnnotateStaleInlineGlobalNoop(t *testing.T) {
	// Global memory has no projectRoot: the check must degrade to a no-op
	// instead of resolving repo-style paths against HOME (guaranteed false
	// positives).
	am := &AutoMemory{dir: t.TempDir()} // projectRoot == ""

	inline := []MemoryEntry{{
		Key:     "global-stale",
		Content: "See internal/agent/foo.go for details.",
	}}
	am.annotateStaleInline(inline)

	if inline[0].Content != "See internal/agent/foo.go for details." {
		t.Errorf("global instance must not annotate, got: %q", inline[0].Content)
	}
}

func TestAnnotateStaleInlineCap(t *testing.T) {
	workingDir := t.TempDir()
	am := &AutoMemory{dir: t.TempDir(), projectRoot: workingDir}

	inline := make([]MemoryEntry, maxStaleInlineAnnotations+3)
	for i := range inline {
		inline[i] = MemoryEntry{
			Key:     "stale-" + strings.Repeat("a", i), // distinct keys
			Content: "references deleted/path/number" + strings.Repeat("0", i+1) + ".go somewhere",
		}
	}
	am.annotateStaleInline(inline)

	annotated := 0
	for _, e := range inline {
		if strings.Contains(e.Content, "[memory-stale]") {
			annotated++
		}
	}
	if annotated != maxStaleInlineAnnotations {
		t.Errorf("expected cap at %d annotations, got %d", maxStaleInlineAnnotations, annotated)
	}
}

func TestProjectAutoMemorySetsRoot(t *testing.T) {
	workingDir := t.TempDir()
	am := NewProjectAutoMemory(workingDir)
	if am == nil {
		t.Fatal("expected non-nil project AutoMemory")
	}
	if am.projectRoot != workingDir {
		t.Errorf("expected projectRoot %q, got %q", workingDir, am.projectRoot)
	}
}
