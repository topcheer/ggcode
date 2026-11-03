package checkpoint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIssue574G_RevertRecordsCorrection(t *testing.T) {
	m := NewManager(10)
	m.StartRun("run-1")

	tmpDir := t.TempDir()
	file1 := filepath.Join(tmpDir, "file1.txt")
	file2 := filepath.Join(tmpDir, "file2.txt")

	if err := os.WriteFile(file1, []byte("initial1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("initial2"), 0644); err != nil {
		t.Fatal(err)
	}

	cp1 := m.Save(file1, "initial1", "edit1", "edit_file")
	_ = m.Save(file2, "initial2", "edit2", "edit_file")
	_ = m.Save(file1, "edit1", "edit3", "edit_file")

	if corrs := m.RecentCorrections(); corrs != nil {
		t.Fatalf("expected no corrections initially, got %d", len(corrs))
	}

	reverted, err := m.Revert(cp1.ID)
	if err != nil {
		t.Fatalf("Revert failed: %v", err)
	}
	if reverted.ID != cp1.ID {
		t.Fatalf("reverted wrong checkpoint: got %s, want %s", reverted.ID, cp1.ID)
	}

	got1, err := os.ReadFile(file1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got1) != "initial1" {
		t.Fatalf("file1 content after revert: got %q, want %q", string(got1), "initial1")
	}

	corrs := m.RecentCorrections()
	if corrs == nil || len(corrs) == 0 {
		t.Fatal("REGRESSION: Revert did not record a Correction (#574-G)")
	}

	corr := corrs[0]
	if len(corr.Files) != 2 {
		t.Errorf("correction files count: got %d, want 2 (file1 and file2 were affected)", len(corr.Files))
	}
	if corr.ToolCall != "edit_file" {
		t.Errorf("correction tool call: got %q, want edit_file", corr.ToolCall)
	}
	if corr.RunID != "run-1" {
		t.Errorf("correction run ID: got %q, want run-1", corr.RunID)
	}
}

func TestIssue574G_RevertSingleCheckpointRecordsCorrection(t *testing.T) {
	m := NewManager(10)
	m.StartRun("run-single")

	tmpDir := t.TempDir()
	file1 := filepath.Join(tmpDir, "file1.txt")

	if err := os.WriteFile(file1, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}
	cp1 := m.Save(file1, "initial", "edited", "edit_file")

	_, err := m.Revert(cp1.ID)
	if err != nil {
		t.Fatalf("Revert failed: %v", err)
	}

	corrs := m.RecentCorrections()
	if corrs == nil || len(corrs) == 0 {
		t.Fatal("REGRESSION: Revert did not record a Correction (#574-G)")
	}

	corr := corrs[0]
	if len(corr.Files) != 1 {
		t.Errorf("correction files count: got %d, want 1", len(corr.Files))
	}
	if corr.Files[0] != file1 {
		t.Errorf("correction file path: got %q, want %q", corr.Files[0], file1)
	}
}
