package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckpointPreExistingFileNotNew(t *testing.T) {

	dir := initGitRepo(t)
	existing := filepath.Join(dir, "b.go")
	if err := os.WriteFile(existing, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	c := newLastGoodCheckpoint()

	c.recordFileEdit(filepath.Join(dir, "a.go"), dir)
	c.recordVerifyPass()

	c.recordFileEdit(existing, dir)
	c.recordVerifyFail()

	guidance := c.revertGuidance()
	if guidance == "" {
		t.Fatal("expected revert guidance")
	}
	if !strings.Contains(guidance, existing) {
		t.Fatalf("guidance should list %s, got: %s", existing, guidance)
	}
	if strings.Contains(guidance, existing+" (new)") {
		t.Errorf("pre-existing file must NOT be classified as (new): %s", guidance)
	}
	if !strings.Contains(guidance, "git checkout") {
		t.Error("guidance should recommend git checkout rollback for modified files")
	}
	if strings.Contains(guidance, "consider removing") {
		t.Error("destructive removal advice must not appear for pre-existing files")
	}
}

func TestCheckpointGenuinelyNewFileStaysNew(t *testing.T) {
	dir := t.TempDir()
	c := newLastGoodCheckpoint()
	c.recordFileEdit(filepath.Join(dir, "scratch_generated.go"), dir)
	c.recordVerifyPass()
	c.recordFileEdit(filepath.Join(dir, "another_new.go"), dir)
	c.recordVerifyFail()

	guidance := c.revertGuidance()
	if !strings.Contains(guidance, "(new)") {
		t.Errorf("new files should keep the (new) marker: %s", guidance)
	}
}
