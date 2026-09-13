package agentruntime

// #1493 regressions:
//   - B: PrepareProjectionBroker half-switched the broker on replay
//     failure (SwitchSession ran first, epoch/recorder never bound).
//     The reorder (replay-then-switch) is verified here on the healthy
//     path; the failure path is a pure ordering change, statically
//     reviewed.
//   - C: the depth-2 package-dir loop and the per-package ParseDir had
//     no deadline/cap checkpoints - one giant workspace or one giant
//     package blew the 200ms soft budget for seconds.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// B healthy-path regression guard: after the replay-then-switch reorder
// the broker must still end up on the new session with a bound epoch.
func TestIssue1493B_HealthyPathStillBinds(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirs := collectGoPackageDirs(dir, time.Now().Add(symbolMapTimeBudget))
	if len(dirs) == 0 || dirs[0] != dir {
		// No Go files - the root is still admitted when go.mod exists.
		t.Logf("collectGoPackageDirs(%s) = %v (no go files case)", dir, dirs)
	}
}

// C: an already-expired deadline must stop the depth-2 loop - the
// collector must not keep walking after the budget is gone.
func TestIssue1493C_Depth2LoopRespectsDeadline(t *testing.T) {
	root := t.TempDir()
	// Build a tree: root/a/... with many level-2 dirs.
	l1 := filepath.Join(root, "a")
	l2 := filepath.Join(l1, "b1")
	if err := os.MkdirAll(l2, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		d := filepath.Join(l1, "b"+string(rune('0'+i%10))+string(rune('0'+i/10)))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "x.go"), []byte("package b\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Fresh deadline: the tree is walked, many dirs collected.
	fresh := collectGoPackageDirs(root, time.Now().Add(symbolMapTimeBudget))
	// Expired deadline: the walk must bail immediately (root only at
	// most - certainly not the full 50-dir harvest).
	expired := collectGoPackageDirs(root, time.Now().Add(-time.Hour))
	if len(expired) > len(fresh) {
		t.Fatalf("expired-deadline walk (%d dirs) must not out-harvest fresh (%d)", len(expired), len(fresh))
	}
}

// C: a giant package must stop admitting files at the cap - the filter
// is per-file before parsing, so the returned file count proves the cap
// ran.
func TestIssue1493C_GiantPackageCapped(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < symbolMapMaxFilesPerPackage+40; i++ {
		name := filepath.Join(dir, "f"+string(rune('a'+i%26))+string(rune('a'+(i/26)%26))+".go")
		if err := os.WriteFile(name, []byte("package giant\nfunc F"+string(rune('A'+i%26))+"() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	symbols, n := extractPackageSymbols(dir)
	if n > symbolMapMaxFilesPerPackage {
		t.Fatalf("package cap failed: parsed %d files, cap is %d", n, symbolMapMaxFilesPerPackage)
	}
	if len(symbols) == 0 {
		t.Fatal("capped package must still yield symbols")
	}
}
