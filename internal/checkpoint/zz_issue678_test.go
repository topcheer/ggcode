package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue678_RevertRestoresAllFilesAtTargetMoment: the #678 scenario —
// cp1 edits f1, cp2 edits f2, cp3 edits f1 again. Revert(cp2) must restore
// EVERY file to its state just before cp2 ran (f1 at cp1's NewContent,
// f2 at its pre-cp2 content), not leave f1 stranded at its cp3 state.
func TestIssue678_RevertRestoresAllFilesAtTargetMoment(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "f1.txt")
	f2 := filepath.Join(dir, "f2.txt")
	if err := os.WriteFile(f1, []byte("orig1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("orig2"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(50)
	m.StartRun("run-678")
	_ = m.Save(f1, "orig1", "edit1", "edit_file")    // cp1
	cp2 := m.Save(f2, "orig2", "edit2", "edit_file") // cp2
	_ = m.Save(f1, "edit1", "edit3", "edit_file")    // cp3 edits f1 AGAIN

	if _, err := m.Revert(cp2.ID); err != nil {
		t.Fatalf("Revert failed: %v", err)
	}

	// f2 must be back to pre-cp2 content.
	b, err := os.ReadFile(f2)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "orig2" {
		t.Fatalf("f2 must be restored to pre-cp2 content %q, got %q", "orig2", string(b))
	}

	// #678 core: f1 must be at its cp1 state (edit1), NOT stranded at cp3's
	// edit3 — the pre-fix bug left f1 at cp3's state while deleting cp3.
	b1, err := os.ReadFile(f1)
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != "edit1" {
		t.Fatalf("#678: f1 must be restored to its state at cp2's moment (cp1's new content %q), got %q — mixed state persists", "edit1", string(b1))
	}

	// Truncation: only cp1 survives.
	if cps := m.List(); len(cps) != 1 || cps[0].FilePath != f1 {
		t.Fatalf("expected only cp1 to survive, got %d checkpoints", len(cps))
	}
}

// TestIssue678_RevertCorrectionIsHonest: the Correction record must list
// exactly the files actually written back — every unique file in the
// truncated span, each restored for real.
func TestIssue678_RevertCorrectionIsHonest(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "f1.txt")
	f2 := filepath.Join(dir, "f2.txt")
	for _, f := range []string{f1, f2} {
		if err := os.WriteFile(f, []byte("orig"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	m := NewManager(50)
	m.StartRun("run-678b")
	_ = m.Save(f1, "orig", "e1", "edit_file")
	cp2 := m.Save(f2, "orig", "e2", "edit_file")
	_ = m.Save(f1, "e1", "e3", "edit_file")

	if _, err := m.Revert(cp2.ID); err != nil {
		t.Fatalf("Revert failed: %v", err)
	}

	corrs := m.RecentCorrections()
	if len(corrs) != 1 {
		t.Fatalf("expected exactly 1 correction, got %d", len(corrs))
	}
	c := corrs[0]
	if len(c.Files) != 2 {
		t.Fatalf("correction must report exactly the 2 files actually reverted, got %v", c.Files)
	}
	got := map[string]bool{}
	for _, f := range c.Files {
		got[f] = true
	}
	if !got[f1] || !got[f2] {
		t.Fatalf("correction must include both f1 and f2, got %v", c.Files)
	}
	// Both files must genuinely be back at the revert-moment state.
	for _, f := range []string{f1, f2} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// f1 was at "e1" (cp1's new content) at cp2's moment; f2 at "orig".
		want := "orig"
		if f == f1 {
			want = "e1"
		}
		if string(b) != want {
			t.Fatalf("%s must be at %q after revert, got %q", f, want, string(b))
		}
	}
}

// TestIssue678_RevertStillUndoableAfterwards: after Revert(cp2), the
// surviving cp1 must still allow a single-step Undo (restore f1 to orig) —
// the pre-fix bug deleted the remediation records.
func TestIssue678_RevertStillUndoableAfterwards(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "f1.txt")
	f2 := filepath.Join(dir, "f2.txt")
	if err := os.WriteFile(f1, []byte("orig1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("orig2"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(50)
	m.StartRun("run-678c")
	_ = m.Save(f1, "orig1", "edit1", "edit_file")
	cp2 := m.Save(f2, "orig2", "edit2", "edit_file")
	_ = m.Save(f1, "edit1", "edit3", "edit_file")

	if _, err := m.Revert(cp2.ID); err != nil {
		t.Fatalf("Revert failed: %v", err)
	}

	if _, err := m.Undo("user"); err != nil {
		t.Fatalf("Undo after Revert must still work (remediation preserved), got: %v", err)
	}
	b, err := os.ReadFile(f1)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "orig1" {
		t.Fatalf("f1 must be back to orig1 after Undo, got %q", string(b))
	}
}

// TestIssue678_RevertFileCreationInSpan: a checkpoint in the truncated span
// that CREATED a file (existed=false) must remove the file on revert when no
// earlier checkpoint for that file survives; and must restore its NewContent
// when an earlier checkpoint does survive.
func TestIssue678_RevertFileCreationInSpan(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "f1.txt")
	fnew := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(f1, []byte("orig1"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(50)
	m.StartRun("run-678d")
	cp1 := m.Save(f1, "orig1", "edit1", "edit_file")
	_ = m.SaveWithExistence(fnew, "", "created", "write_file", false) // cp2 creates fnew

	if _, err := m.Revert(cp1.ID); err != nil {
		t.Fatalf("Revert failed: %v", err)
	}

	// Reverting past BOTH checkpoints: f1 back to orig1, fnew removed.
	if b, _ := os.ReadFile(f1); string(b) != "orig1" {
		t.Fatalf("f1 must be orig1, got %q", string(b))
	}
	if _, err := os.Stat(fnew); !os.IsNotExist(err) {
		t.Fatalf("created file must be removed when reverting past its creation checkpoint")
	}
}

// TestIssue678_RevertNoEarlierCheckpointRestoresOldContent: reverting the
// FIRST checkpoint of a file (no earlier record) writes back its OldContent.
func TestIssue678_RevertNoEarlierCheckpointRestoresOldContent(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "f1.txt")
	if err := os.WriteFile(f1, []byte("pre-existing"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(50)
	m.StartRun("run-678e")
	cp1 := m.SaveWithExistence(f1, "pre-existing", "edited", "edit_file", true)
	_ = m.Save(f1, "edited", "edited2", "edit_file")

	if _, err := m.Revert(cp1.ID); err != nil {
		t.Fatalf("Revert failed: %v", err)
	}
	if b, _ := os.ReadFile(f1); string(b) != "pre-existing" {
		t.Fatalf("f1 must be restored to pre-existing, got %q", string(b))
	}
	if cps := m.List(); len(cps) != 0 {
		t.Fatalf("all checkpoints must be truncated, got %d", len(cps))
	}
}

// TestIssue678_RevertFailureKeepsHistory: when a restore write fails, the
// checkpoint history must stay intact so the caller can retry — no partial
// truncation stranding un-restored files (#678 mixed-state prevention).
func TestIssue678_RevertFailureKeepsHistory(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "f1.txt")
	if err := os.WriteFile(f1, []byte("orig1"), 0644); err != nil {
		t.Fatal(err)
	}
	// A "file" that is actually a directory: writing content into it fails.
	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0755); err != nil {
		t.Fatal(err)
	}

	m := NewManager(50)
	m.StartRun("run-678f")
	cp1 := m.Save(f1, "orig1", "edit1", "edit_file")
	cp2 := m.SaveWithExistence(blocked, "", "x", "edit_file", true)

	if _, err := m.Revert(cp1.ID); err == nil {
		t.Fatal("Revert must fail when a file in the span cannot be written")
	} else if !strings.Contains(err.Error(), blocked) {
		t.Fatalf("error should name the failing file %q: %v", blocked, err)
	}

	// History intact: both checkpoints still present, retryable.
	cps := m.List()
	if len(cps) != 2 {
		t.Fatalf("history must be preserved on restore failure, got %d checkpoints", len(cps))
	}
	if cps[0].ID != cp1.ID || cps[1].ID != cp2.ID {
		t.Fatal("checkpoint order/content must be unchanged after failed Revert")
	}
}
