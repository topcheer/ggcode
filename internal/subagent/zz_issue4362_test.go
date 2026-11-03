package subagent

import (
	"os"
	"testing"
	"time"
)

func TestIssue4362LoadRefreshesDirMtime(t *testing.T) {
	dir := t.TempDir()
	store := &TemplateStore{dir: dir}

	tmpl := NamedAgentTemplate{Name: "reviewer", Description: "d", SystemPrompt: "p"}
	if err := store.Save(tmpl); err != nil {
		t.Fatal(err)
	}

	old := time.Now().Add(-120 * 24 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Load("reviewer"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.ModTime().Before(time.Now().Add(-time.Hour)) {
		t.Fatal("#4362 regression: Load did not refresh the store dir mtime - read-only templates remain sweep-vulnerable")
	}

	if _, err := store.Load("reviewer"); err != nil {
		t.Fatalf("second Load after touch failed: %v", err)
	}
}
