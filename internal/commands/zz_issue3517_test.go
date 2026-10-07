package commands

// #3517 probe: a corrupt skill_usage.json must NOT be read as "empty, nil"
// (RecordUsage would then rewrite it and erase the history silently), and
// saves must be atomic so a crash can never produce a truncated file.

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
		t.Fatal(err) // truncated JSON, no closing brace
	}

	// RecordUsage must FAIL (short-circuit), not overwrite with an empty map.
	// (#3517: unique name so the package-level debounce table from earlier
	// tests cannot short-circuit before the load.)
	if err := RecordUsage("deploy-corrupt-3517"); err == nil {
		t.Fatal("RecordUsage must fail on a corrupt store, not silently rewrite it")
	}
	// The damaged file must be quarantined aside (renamed, never deleted),
	// and the quarantine copy still carries the history bytes.
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
	// The saved file is valid JSON the next load can parse.
	if _, err := loadUsageLocked(); err != nil {
		t.Fatalf("reload after atomic save failed: %v", err)
	}
}
