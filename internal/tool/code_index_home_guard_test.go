package tool

// Home-directory guard for the code index: starting ggcode with the working
// directory set to the user's home used to walk the ENTIRE home tree
// (macOS Library/, go/pkg/mod, model caches - none of them on the
// project-shaped skip list), collect up to 50k foreign code files, and
// read+tokenize them all in doBuild - the observed OOM. Indexing is now
// structurally disabled there, and every trigger path (TUI startup,
// code_search lazy start, Search's lazyLoad) must stay a no-op.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodeIndexDisabledInHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory available")
	}

	m := NewCodeIndexManager(home)
	if !m.disabled {
		t.Fatalf("index manager for home directory (%s) must be disabled", home)
	}

	// Every build trigger is a no-op: no goroutine is spawned, no lock
	// file is created, no walk starts.
	m.StartBackgroundIndex()
	if m.building || m.started || m.ready {
		t.Fatalf("StartBackgroundIndex must be a no-op when disabled: building=%v started=%v ready=%v", m.building, m.started, m.ready)
	}

	// Search must return the actionable disabled error (not the generic
	// not-ready one) so the model reaches for grep instead of retrying.
	if _, err := m.Search("anything", 10); err == nil || err != errIndexDisabled {
		t.Fatalf("Search under disabled index must return errIndexDisabled, got %v", err)
	}

	// lazyLoad must not flip into building either.
	m.lazyLoad()
	if m.building {
		t.Fatal("lazyLoad must be a no-op when disabled")
	}
}

func TestCodeIndexEnabledInProjectSubdirOfHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory available")
	}
	// A project UNDER the home (the normal ~/projects/foo case) must keep
	// indexing - only the home directory ITSELF is excluded.
	m := NewCodeIndexManager(filepath.Join(home, "some-project"))
	if m.disabled {
		t.Fatal("project subdirectory of home must not be disabled")
	}
}

func TestIsHomeDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory available")
	}
	if !isHomeDirectory(home) {
		t.Fatalf("isHomeDirectory(%s) = false, want true", home)
	}
	if isHomeDirectory(filepath.Join(home, "sub")) {
		t.Fatal("subdirectory of home must not be reported as home")
	}
	if isHomeDirectory(filepath.Dir(home)) {
		t.Fatal("parent of home must not be reported as home")
	}
}
