//go:build goolm

package a2a

import (
	"os"
	"path/filepath"
	"testing"
)

// #4500: the bare "test." substring made ordinary files like latest.go /
// contest.go flip HasTests to a false true in the agent card. The precise
// suffix conditions must still catch real test files.
func TestIssue4500_HasTestsNoFalsePositiveFromPlainNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"latest.go", "contest.go", "protest.go", "main.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package x\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if _, hasTests := scanWorkspaceSignals(dir); hasTests {
		t.Fatal("plain file names (latest.go etc.) must not set HasTests")
	}

	dir2 := t.TempDir()
	for _, name := range []string{"main.go", "zz_issue4500_test.go"} {
		if err := os.WriteFile(filepath.Join(dir2, name), []byte("package x\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if _, hasTests := scanWorkspaceSignals(dir2); !hasTests {
		t.Fatal("a real _test.go file must set HasTests")
	}
}

// #4501: Discover() must return a private copy - in-place mutation of the
// returned slice must not leak into the cache (and via it into every later
// Discover/CachedInstances call).
func TestIssue4501_DiscoverReturnsCopyNotCacheBacking(t *testing.T) {
	r := &Registry{}
	r.SetCachedInstances([]InstanceInfo{{ID: "a"}, {ID: "b"}})

	out, err := r.Discover()
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(out))
	}
	// Caller-side in-place mutation (sort swap / field edit).
	out[0].ID = "MUTATED"

	again, _ := r.Discover()
	cached := r.CachedInstances()
	if again[0].ID == "MUTATED" || cached[0].ID == "MUTATED" {
		t.Fatalf("mutation leaked into cache: again=%v cached=%v", again[0].ID, cached[0].ID)
	}
}
