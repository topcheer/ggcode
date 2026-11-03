package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue3517_CorruptFileDoesNotEraseHistory(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	dir := filepath.Join(tmpDir, ".ggcode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "skill_usage.json")
	if err := os.WriteFile(path, []byte(`{"deploy": {"usage_count": 42,`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RecordUsage("deploy-corrupt-3517"); err == nil {
		t.Fatal("RecordUsage must fail on a corrupt store, not silently rewrite it")
	}
	entries, _ := os.ReadDir(dir)
	quarantined := ""
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "skill_usage.json.corrupt-") {
			quarantined = filepath.Join(dir, e.Name())
		}
	}
	if quarantined == "" {
		t.Fatal("corrupt store must be quarantined as skill_usage.json.corrupt-*")
	}
	qdata, err := os.ReadFile(quarantined)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(qdata), "usage_count") {
		t.Fatalf("quarantined history must keep its bytes, got %q", string(qdata))
	}
}

func TestIssue3517_SaveIsAtomicNoTmpResidue(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	if err := RecordUsage("deploy"); err != nil {
		t.Fatalf("RecordUsage error = %v", err)
	}
	dir := filepath.Join(tmpDir, ".ggcode")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("atomic save left tmp residue: %s", e.Name())
		}
	}
	if _, err := loadUsageLocked(); err != nil {
		t.Fatalf("reload after atomic save failed: %v", err)
	}
}
