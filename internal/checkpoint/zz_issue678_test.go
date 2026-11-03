package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	_ = m.Save(f1, "orig1", "edit1", "edit_file")
	cp2 := m.Save(f2, "orig2", "edit2", "edit_file")
	_ = m.Save(f1, "edit1", "edit3", "edit_file")

	if _, err := m.Revert(cp2.ID); err != nil {
		t.Fatalf("Revert failed: %v", err)
	}

	b, err := os.ReadFile(f2)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "orig2" {
		t.Fatalf("f2 must be restored to pre-cp2 content %q, got %q", "orig2", string(b))
	}

	b1, err := os.ReadFile(f1)
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != "edit1" {
		t.Fatalf("#678: f1 must be restored to its state at cp2's moment (cp1's new content %q), got %q — mixed state persists", "edit1", string(b1))
	}

	if cps := m.List(); len(cps) != 1 || cps[0].FilePath != f1 {
		t.Fatalf("expected only cp1 to survive, got %d checkpoints", len(cps))
	}
}

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
	for _, f := range []string{f1, f2} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		want := "orig"
		if f == f1 {
			want = "e1"
		}
		if string(b) != want {
			t.Fatalf("%s must be at %q after revert, got %q", f, want, string(b))
		}
	}
}

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
	_ = m.SaveWithExistence(fnew, "", "created", "write_file", false)

	if _, err := m.Revert(cp1.ID); err != nil {
		t.Fatalf("Revert failed: %v", err)
	}

	if b, _ := os.ReadFile(f1); string(b) != "orig1" {
		t.Fatalf("f1 must be orig1, got %q", string(b))
	}
	if _, err := os.Stat(fnew); !os.IsNotExist(err) {
		t.Fatalf("created file must be removed when reverting past its creation checkpoint")
	}
}

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

func TestIssue678_RevertFailureKeepsHistory(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "f1.txt")
	if err := os.WriteFile(f1, []byte("orig1"), 0644); err != nil {
		t.Fatal(err)
	}
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

	cps := m.List()
	if len(cps) != 2 {
		t.Fatalf("history must be preserved on restore failure, got %d checkpoints", len(cps))
	}
	if cps[0].ID != cp1.ID || cps[1].ID != cp2.ID {
		t.Fatal("checkpoint order/content must be unchanged after failed Revert")
	}
}
