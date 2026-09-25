package main

// #2763 regression: unbindBelongsToWorkspace must normalize both sides
// before comparing. Persisted bindings are normalized (EvalSymlinks+Clean)
// on save, but daemonWorkspace came raw from os.Getwd(). Under a symlinked
// PWD the raw == comparison never matched, so WebUI unbind always reported
// "no persisted binding" while bind/list (which normalize internally) kept
// working.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIssue2763UnbindMatchesSymlinkedWorkspace(t *testing.T) {
	// Real target dir + a symlink pointing at it.
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked-ws")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported here: %v", err)
	}
	// Persisted side is always normalized on save: that is target.
	binding := target
	// Daemon side: raw Getwd-style path through the symlink (NOT resolved).
	daemonWS := link

	if !unbindBelongsToWorkspace(binding, daemonWS) {
		t.Fatalf("symlinked daemon workspace %q must match normalized binding %q", daemonWS, binding)
	}
	// Reverse direction: binding stored through symlink form, daemon at target.
	if !unbindBelongsToWorkspace(daemonWS, binding) {
		t.Fatalf("normalized daemon workspace %q must match symlinked binding %q", binding, daemonWS)
	}
}

func TestIssue2763UnbindNonCleanPath(t *testing.T) {
	// Non-Clean daemon path (double slash, trailing slash) must still match.
	dir := t.TempDir()
	raw := filepath.Join(dir, "a") + "/../a/"
	if !unbindBelongsToWorkspace(filepath.Join(dir, "a"), raw) {
		t.Fatalf("non-clean daemon workspace %q must match clean binding", raw)
	}
}

func TestIssue2763UnbindStillRejectsOtherWorkspace(t *testing.T) {
	// Normalization must NOT loosen the ownership check: a different real
	// directory still must not match (#2728 protection).
	a := t.TempDir()
	b := t.TempDir()
	if unbindBelongsToWorkspace(a, b) {
		t.Fatalf("distinct workspaces %q vs %q must not match", a, b)
	}
}
