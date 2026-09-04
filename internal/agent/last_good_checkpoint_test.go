package agent

import (
	"strings"
	"testing"
)

func TestLastGoodCheckpoint_BasicLifecycle(t *testing.T) {
	c := newLastGoodCheckpoint()

	// Before any verification, no guidance.
	if g := c.revertGuidance(); g != "" {
		t.Fatalf("expected empty guidance before baseline, got: %s", g)
	}

	// Record some edits before first verify.
	c.recordFileEdit("/foo/bar.go", "")
	c.recordFileEdit("/foo/baz.go", "")

	// First verify passes -- snapshot taken.
	c.recordVerifyPass()
	if len(c.lastGoodFiles) != 2 {
		t.Fatalf("expected 2 lastGoodFiles after pass, got %d", len(c.lastGoodFiles))
	}
	// Since-last-good should be empty after a pass.
	if len(c.filesModifiedSinceLastGood) != 0 {
		t.Fatalf("expected 0 filesModifiedSinceLastGood after pass, got %d", len(c.filesModifiedSinceLastGood))
	}

	// Now make more edits that introduce regressions.
	c.recordFileEdit("/foo/bar.go", "") // modify existing
	c.recordFileEdit("/foo/new.go", "") // new file

	// Verify fails -- no snapshot update.
	c.recordVerifyFail()

	// Should have guidance listing files modified since last good.
	g := c.revertGuidance()
	if g == "" {
		t.Fatal("expected non-empty guidance after fail with modified files")
	}
	if !strings.Contains(g, "/foo/bar.go") {
		t.Errorf("guidance should mention /foo/bar.go, got: %s", g)
	}
	if !strings.Contains(g, "/foo/new.go") {
		t.Errorf("guidance should mention /foo/new.go, got: %s", g)
	}
	if !strings.Contains(g, "git checkout") {
		t.Errorf("guidance should suggest git checkout, got: %s", g)
	}
}

func TestLastGoodCheckpoint_NoGuidanceAfterPass(t *testing.T) {
	c := newLastGoodCheckpoint()

	c.recordFileEdit("/a.go", "")
	c.recordVerifyPass()
	c.recordFileEdit("/b.go", "")

	// No verify fail yet, so no guidance needed.
	if g := c.revertGuidance(); g != "" {
		t.Fatalf("expected empty guidance without a fail cycle, got: %s", g)
	}
}

func TestLastGoodCheckpoint_Reset(t *testing.T) {
	c := newLastGoodCheckpoint()

	c.recordFileEdit("/a.go", "")
	c.recordVerifyPass()
	c.recordFileEdit("/b.go", "")
	c.recordVerifyFail()

	c.reset()

	if c.hasBaseline {
		t.Error("expected hasBaseline=false after reset")
	}
	if len(c.lastGoodFiles) != 0 {
		t.Errorf("expected 0 lastGoodFiles after reset, got %d", len(c.lastGoodFiles))
	}
	if len(c.filesModifiedSinceLastGood) != 0 {
		t.Errorf("expected 0 filesModifiedSinceLastGood after reset, got %d", len(c.filesModifiedSinceLastGood))
	}
}

func TestLastGoodCheckpoint_NewFilesOnly(t *testing.T) {
	c := newLastGoodCheckpoint()

	// First pass with no files.
	c.recordVerifyPass()

	// Add only new files.
	c.recordFileEdit("/new1.go", "")
	c.recordFileEdit("/new2.go", "")
	c.recordVerifyFail()

	g := c.revertGuidance()
	if g == "" {
		t.Fatal("expected non-empty guidance for new files")
	}
	if !strings.Contains(g, "/new1.go") {
		t.Errorf("guidance should mention /new1.go, got: %s", g)
	}
	// Should not suggest git checkout for new-only files (they weren't modified).
	if strings.Contains(g, "git checkout") {
		t.Errorf("should not suggest git checkout for new-only files, got: %s", g)
	}
	if !strings.Contains(g, "new") {
		t.Errorf("should label files as new, got: %s", g)
	}
}

func TestLastGoodCheckpoint_MaxFilesLimit(t *testing.T) {
	c := newLastGoodCheckpoint()
	c.recordVerifyPass()

	// Record more than checkpointMaxFiles modified files.
	for i := 0; i < checkpointMaxFiles+5; i++ {
		c.recordFileEdit("/file"+string(rune('a'+i))+".go", "")
	}
	c.recordVerifyFail()

	g := c.revertGuidance()
	if !strings.Contains(g, "more file(s)") {
		t.Errorf("expected truncation message for > %d files, got: %s", checkpointMaxFiles, g)
	}
}

func TestLastGoodCheckpoint_NilSafe(t *testing.T) {
	var c *lastGoodCheckpoint
	c.recordFileEdit("/a.go", "")
	c.recordVerifyPass()
	c.recordVerifyFail()
	c.reset()
	if g := c.revertGuidance(); g != "" {
		t.Fatalf("nil checkpoint should return empty guidance, got: %s", g)
	}
}
