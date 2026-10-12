package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// r10 instruction-driven forgetting (sa-160): ForgetKey hides from prompt
// injection, RestoreKey reverses it, GarbageCollect is the only physical
// deleter (#779 doctrine).

func newForgetTestMemory(t *testing.T) *AutoMemory {
	t.Helper()
	return &AutoMemory{dir: t.TempDir()}
}

func TestForgetKey_GhostKeyErrors(t *testing.T) {
	am := newForgetTestMemory(t)
	if err := am.ForgetKey("no-such-key"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want not-found error, got %v", err)
	}
}

func TestForgetKey_RemovesFromPromptInjection(t *testing.T) {
	am := newForgetTestMemory(t)
	if err := am.SaveMemoryWithSource("build-process", "use make verify-ci", "test"); err != nil {
		t.Fatal(err)
	}
	if err := am.SaveMemoryWithSource("other-note", "keep me", "test"); err != nil {
		t.Fatal(err)
	}
	if err := am.ForgetKey("build-process"); err != nil {
		t.Fatalf("ForgetKey: %v", err)
	}
	inline, index, err := am.LoadForPrompt()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range inline {
		if e.Key == "build-process" {
			t.Fatalf("forgotten key still injected inline: %+v", e)
		}
	}
	for _, k := range index {
		if k == "other-note" || strings.HasPrefix(k, "other-note") {
			// sanity: survivor stays visible
		}
		if strings.HasPrefix(k, "build-process") {
			t.Fatalf("forgotten key still in index list: %q", k)
		}
	}
}

func TestForgetKey_Idempotent(t *testing.T) {
	am := newForgetTestMemory(t)
	if err := am.SaveMemoryWithSource("k1", "v", "test"); err != nil {
		t.Fatal(err)
	}
	if err := am.ForgetKey("k1"); err != nil {
		t.Fatal(err)
	}
	if err := am.ForgetKey("k1"); err != nil {
		t.Fatalf("second ForgetKey must be idempotent, got %v", err)
	}
}

func TestRestoreKey_RevivesInjection(t *testing.T) {
	am := newForgetTestMemory(t)
	if err := am.SaveMemoryWithSource("revive-me", "content", "test"); err != nil {
		t.Fatal(err)
	}
	if err := am.ForgetKey("revive-me"); err != nil {
		t.Fatal(err)
	}
	if err := am.RestoreKey("revive-me"); err != nil {
		t.Fatalf("RestoreKey: %v", err)
	}
	inline, index, err := am.LoadForPrompt()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range inline {
		if e.Key == "revive-me" {
			found = true
		}
	}
	for _, k := range index {
		if strings.HasPrefix(k, "revive-me") {
			found = true
		}
	}
	if !found {
		t.Fatal("restored key not visible again (neither inline nor index)")
	}
}

func TestRestoreKey_NotForgottenErrors(t *testing.T) {
	am := newForgetTestMemory(t)
	if err := am.RestoreKey("never-forgotten"); err == nil || !strings.Contains(err.Error(), "not forgotten") {
		t.Fatalf("want not-forgotten error, got %v", err)
	}
}

func TestListForgotten_OldestFirst(t *testing.T) {
	am := newForgetTestMemory(t)
	for _, k := range []string{"b-key", "a-key"} {
		if err := am.SaveMemoryWithSource(k, "v", "test"); err != nil {
			t.Fatal(err)
		}
	}
	// Mark a-key first so age ordering is deterministic.
	if err := am.ForgetKey("a-key"); err != nil {
		t.Fatal(err)
	}
	if err := am.ForgetKey("b-key"); err != nil {
		t.Fatal(err)
	}
	got := am.ListForgotten()
	if len(got) != 2 || !strings.HasPrefix(got[0], "a-key") || !strings.HasPrefix(got[1], "b-key") {
		t.Fatalf("want [a-key, b-key] oldest-first, got %v", got)
	}
}

func TestGarbageCollect_DigestsForgotten(t *testing.T) {
	am := newForgetTestMemory(t)
	if err := am.SaveMemoryWithSource("doomed", "v", "test"); err != nil {
		t.Fatal(err)
	}
	if err := am.SaveMemoryWithSource("keeper", "v", "test"); err != nil {
		t.Fatal(err)
	}
	if err := am.ForgetKey("doomed"); err != nil {
		t.Fatal(err)
	}
	stats := am.GarbageCollect()
	if stats.ForgottenRemoved != 1 {
		t.Fatalf("want 1 forgotten removed, got %+v", stats)
	}
	if _, err := os.Stat(filepath.Join(am.dir, "doomed.md")); !os.IsNotExist(err) {
		t.Fatal("forgotten file still on disk after GC")
	}
	if _, err := os.Stat(filepath.Join(am.dir, "keeper.md")); err != nil {
		t.Fatalf("keeper must survive GC: %v", err)
	}
	// After digestion, restoring must report not-forgotten (mark is gone).
	if err := am.RestoreKey("doomed"); err == nil {
		t.Fatal("RestoreKey after GC digestion must error (nothing to restore)")
	}
}
