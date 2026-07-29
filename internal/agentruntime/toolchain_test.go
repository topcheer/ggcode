package agentruntime

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectToolchain_GoProject(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module x\n")

	resetToolchainCache()
	versions := detectToolchain(root)
	if len(versions) == 0 {
		t.Skip("go not installed — skipping live probe")
	}
	// If go is installed, we should detect it
	found := false
	for _, v := range versions {
		if strings.HasPrefix(v, "Go: ") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Go toolchain detection in Go project, got %v", versions)
	}
}

func TestDetectToolchain_NoMarkers(t *testing.T) {
	root := t.TempDir()
	// Empty dir — no project markers
	versions := detectToolchain(root)
	if len(versions) != 0 {
		t.Errorf("expected no toolchain detection for empty dir, got %v", versions)
	}
}

func TestDetectToolchain_NonExistentDir(t *testing.T) {
	versions := detectToolchain("/nonexistent/path/that/does/not/exist")
	if len(versions) != 0 {
		t.Errorf("expected no results for non-existent dir, got %v", versions)
	}
}

func TestDetectToolchain_EmptyDir(t *testing.T) {
	versions := detectToolchain("")
	if len(versions) != 0 {
		t.Errorf("expected no results for empty workingDir, got %v", versions)
	}
}

func TestDetectToolchain_NodeProject(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"name":"x"}`)

	resetToolchainCache()
	versions := detectToolchain(root)
	// Node/npm may or may not be installed; just verify no crash
	for _, v := range versions {
		if !strings.Contains(v, ": ") {
			t.Errorf("malformed toolchain line: %q", v)
		}
	}
}

func TestDetectToolchain_CacheWorks(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module x\n")

	// Ensure go is available; if not, skip
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}

	resetToolchainCache()
	v1 := detectToolchain(root)
	v2 := detectToolchain(root)
	if len(v1) != len(v2) {
		t.Errorf("cache should return same results: v1=%v v2=%v", v1, v2)
	}
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Errorf("cache mismatch at %d: %q vs %q", i, v1[i], v2[i])
		}
	}

	// Verify cache was populated
	toolchainCacheMu.RLock()
	cached, ok := toolchainCache["go"]
	toolchainCacheMu.RUnlock()
	if !ok || cached == "" {
		t.Errorf("expected go version to be cached, got %q", cached)
	}
}

func TestRunVersionCmd_NotFound(t *testing.T) {
	resetToolchainCache()
	result := runVersionCmd("definitely_not_a_real_binary_xyz123", []string{"--version"})
	if result != "" {
		t.Errorf("expected empty result for non-existent binary, got %q", result)
	}
}

func TestToolchainSection_Empty(t *testing.T) {
	root := t.TempDir()
	section := toolchainSection(root)
	if section != "" {
		t.Errorf("expected empty section for project with no markers, got %q", section)
	}
}

func TestToolchainSection_GoProject(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module x\n")

	resetToolchainCache()
	section := toolchainSection(root)
	if section == "" {
		t.Skip("go not installed")
	}
	if !strings.Contains(section, "## Toolchain") {
		t.Errorf("expected section header, got %q", section)
	}
	if !strings.Contains(section, "Go:") {
		t.Errorf("expected Go version in section, got %q", section)
	}
}

func TestToolchainHasMarker(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module x\n")

	if !toolchainHasMarker(root, []string{"go.mod", "Cargo.toml"}) {
		t.Error("expected go.mod marker to be found")
	}
	if toolchainHasMarker(root, []string{"Cargo.toml", "package.json"}) {
		t.Error("expected no match for absent markers")
	}
}

// TestDetectToolchain_DoesNotProbeIrrelevantTools verifies that a pure Go
// project doesn't trigger probes for Node/Python/Rust.
func TestDetectToolchain_DoesNotProbeIrrelevantTools(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module x\n")

	resetToolchainCache()
	detectToolchain(root)

	toolchainCacheMu.RLock()
	defer toolchainCacheMu.RUnlock()

	// go should be cached (if installed), but node/python/cargo must NOT be
	for _, bin := range []string{"node", "npm", "python3", "cargo"} {
		if _, ok := toolchainCache[bin]; ok {
			// Only fail if the cache has a non-empty result — an empty cache
			// entry means the binary was probed but not found, which shouldn't
			// happen either.
			t.Errorf("binary %q should not have been probed for Go project", bin)
		}
	}
}
