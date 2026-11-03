package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIssue557_InformationalPromptWithVerbInIdentifier(t *testing.T) {
	if !isInformationalRequest("What does the updateHandler function do?") {
		t.Error("identifier fragment 'updateHandler' must not count as action verb 'update'")
	}
	if !isInformationalRequest("How does the deleteEntry helper work?") {
		t.Error("identifier fragment 'deleteEntry' must not count as action verb 'delete'")
	}
}

func TestIssue557_ExecutablePromptStillGated(t *testing.T) {

	if isInformationalRequest("Please update the config file and fix the tests.") {
		t.Error("real action-verb prompt misclassified as informational")
	}
	if got := detectActions("please update and fix"); len(got) == 0 {
		t.Error("detectActions lost all verbs after word-boundary switch")
	}
	if got := detectActions("what does updateHandler do"); len(got) != 0 {
		t.Errorf("detectActions counted identifier fragments: %v", got)
	}
}

func TestIssue557_ReadHashAbsEditRel(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := newReadHashTracker()
	tr.recordReadHash(file)

	if err := os.WriteFile(file, []byte("package main // changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if got := tr.validateContentAtEdit("a.go", 0); got == "" {
		t.Error("relative edit form skipped content-hash expiry (abs/rel mismatch, #557)")
	}
}

func TestIssue557_ReadHashSameFormStillWorks(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "b.go")
	os.WriteFile(file, []byte("x\n"), 0o644)
	tr := newReadHashTracker()
	tr.recordReadHash(file)
	os.WriteFile(file, []byte("y\n"), 0o644)
	if got := tr.validateContentAtEdit(file, 0); got == "" {
		t.Error("same-form lookup regressed")
	}

	tr.recordReadHash(file)
	tr.recordWriteHash("b.go")
	os.WriteFile(file, []byte("z\n"), 0o644)
	if got := tr.validateContentAtEdit(file, 0); got != "" {
		t.Error("recordWriteHash with relative form failed to clear absolute-keyed hash")
	}
}
