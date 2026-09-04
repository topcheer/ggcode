package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #423: the FIRST edit of a pre-existing git-tracked file must be classified
// as "modified" (git checkout rollback advice), not "(new)" (destructive
// removal advice). The old check — lastGoodFiles membership — only knew what
// was edited during the previous green cycle.
func TestCheckpointPreExistingFileNotNew(t *testing.T) {
	// #1469-A: pre-existing now means GIT-TRACKED (only tracked files can
	// be restored by git checkout) - the scenario needs a real repo.
	dir := initGitRepo(t)
	existing := filepath.Join(dir, "b.go")
	if err := os.WriteFile(existing, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	c := newLastGoodCheckpoint()
	// Green cycle with an edit to a.go.
	c.recordFileEdit(filepath.Join(dir, "a.go"), dir)
	c.recordVerifyPass()
	// First-ever edit to the PRE-EXISTING b.go, then a failing verify.
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

// #423 companion: genuinely NEW files (created by this run) keep the
// "(new)" classification and removal advice.
func TestCheckpointGenuinelyNewFileStaysNew(t *testing.T) {
	dir := t.TempDir()
	c := newLastGoodCheckpoint()
	c.recordFileEdit(filepath.Join(dir, "scratch_generated.go"), dir) // never existed
	c.recordVerifyPass()
	c.recordFileEdit(filepath.Join(dir, "another_new.go"), dir) // created after green
	c.recordVerifyFail()

	guidance := c.revertGuidance()
	if !strings.Contains(guidance, "(new)") {
		t.Errorf("new files should keep the (new) marker: %s", guidance)
	}
}
