package tool

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

	m.StartBackgroundIndex()
	if m.building || m.started || m.ready {
		t.Fatalf("StartBackgroundIndex must be a no-op when disabled: building=%v started=%v ready=%v", m.building, m.started, m.ready)
	}

	if _, err := m.Search("anything", 10); err == nil || err != errIndexDisabled {
		t.Fatalf("Search under disabled index must return errIndexDisabled, got %v", err)
	}

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

	if !sameDirPath(strings.ToLower(home), home) || !sameDirPath(strings.ToUpper(home), home) {
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			t.Fatalf("case-variant home spelling must match on %s (Lower/Upper of %s)", runtime.GOOS, home)
		}
	}
}
