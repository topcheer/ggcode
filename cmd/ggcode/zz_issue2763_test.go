package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIssue2763UnbindMatchesSymlinkedWorkspace(t *testing.T) {

	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked-ws")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported here: %v", err)
	}

	binding := target

	daemonWS := link

	if !unbindBelongsToWorkspace(binding, daemonWS) {
		t.Fatalf("symlinked daemon workspace %q must match normalized binding %q", daemonWS, binding)
	}

	if !unbindBelongsToWorkspace(daemonWS, binding) {
		t.Fatalf("normalized daemon workspace %q must match symlinked binding %q", binding, daemonWS)
	}
}

func TestIssue2763UnbindNonCleanPath(t *testing.T) {

	dir := t.TempDir()
	raw := filepath.Join(dir, "a") + "/../a/"
	if !unbindBelongsToWorkspace(filepath.Join(dir, "a"), raw) {
		t.Fatalf("non-clean daemon workspace %q must match clean binding", raw)
	}
}

func TestIssue2763UnbindStillRejectsOtherWorkspace(t *testing.T) {

	a := t.TempDir()
	b := t.TempDir()
	if unbindBelongsToWorkspace(a, b) {
		t.Fatalf("distinct workspaces %q vs %q must not match", a, b)
	}
}
