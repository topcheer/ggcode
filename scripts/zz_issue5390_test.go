package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// #5390: review-coverage.sh mark/prune did an unlocked read-modify-write on
// the ledger through a FIXED .tmp path, so concurrent marks raced: both read
// the same base, last mv won, the loser's mark was silently lost (observed as
// "diff-empty backflow"). The fix serializes the whole RMW behind an
// mkdir-based spin lock and writes through unique mktemp temps (flock is
// Linux-only; dev seats are macOS).
//
// Behavioral pin: 10 parallel `mark` invocations of 10 distinct tracked
// files against a fresh ledger must leave all 10 entries present (the
// issue's acceptance hammer), status must count them, and no stray
// lock/tmp artifacts may remain. Pre-fix, the parallel marks lose most
// entries (verified 4-9 of 10 lost across local runs).
func TestIssue5390ParallelMarksAllSurvive(t *testing.T) {
	script := script5390(t)

	// The script cds to `git rev-parse --show-toplevel` and keeps its
	// ledger in .ggcode/ there, so drive it from a scratch git repo.
	// git ls-files reads the index, so staging is enough - no commit
	// (and thus no committer identity) is needed.
	repo := t.TempDir()
	run5390(t, repo, "git", "init", "-q")
	// The script writes its ledger under .ggcode/ at the repo root; the
	// product always has this directory (epoch file lives there), but a
	// fresh fixture repo does not - create it or every mkdir of the lock
	// dir fails and the spin loop times out.
	if err := os.MkdirAll(filepath.Join(repo, ".ggcode"), 0755); err != nil {
		t.Fatal(err)
	}
	var files []string
	for i := 0; i < 10; i++ {
		p := filepath.Join("pkg", "f"+string(rune('a'+i))+".go")
		files = append(files, p)
		full := filepath.Join(repo, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package pkg\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	args := append([]string{"add", "."}, files...)
	run5390(t, repo, "git", args...)

	var wg sync.WaitGroup
	errs := make(chan error, len(files))
	for _, p := range files {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			cmd := exec.Command(script, "mark", p)
			cmd.Dir = repo
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- &testErr5390{p, err, string(out)}
			}
		}(p)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("parallel mark failed: %v", e)
	}

	ledger := filepath.Join(repo, ".ggcode", "review-ledger.e1.txt")
	data, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("ledger missing after parallel marks: %v", err)
	}
	got := string(data)
	for _, p := range files {
		if !strings.Contains(got, "\t"+p+"\n") {
			t.Errorf("#5390 regression: mark of %s lost under parallel contention", p)
		}
	}
	if n := len(strings.Split(strings.TrimSpace(got), "\n")); n != len(files) {
		t.Errorf("ledger has %d entries, want %d (content:\n%s)", n, len(files), got)
	}

	// No stray lock dir or tmp leftovers may survive a clean run.
	entries, _ := os.ReadDir(filepath.Join(repo, ".ggcode"))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".lockd") || strings.Contains(e.Name(), ".tmp") {
			t.Errorf("stray artifact left behind: %s", e.Name())
		}
	}

	// status must reflect the 10 marks (acceptance: status/next still
	// correct after the hammer).
	out := run5390(t, repo, script, "status")
	if !strings.Contains(out, "reviewed=10 (100%)") {
		t.Errorf("status after 10 parallel marks = %q, want reviewed=10 (100%%)", out)
	}
}

// Concurrent prune must not drop concurrently-earned marks either: the same
// fixed-tmp RMW race existed between mark and prune.
func TestIssue5390PruneKeepsConcurrentMarks(t *testing.T) {
	script := script5390(t)
	repo := t.TempDir()
	run5390(t, repo, "git", "init", "-q")
	// The script writes its ledger under .ggcode/ at the repo root; the
	// product always has this directory (epoch file lives there), but a
	// fresh fixture repo does not - create it or every mkdir of the lock
	// dir fails and the spin loop times out.
	if err := os.MkdirAll(filepath.Join(repo, ".ggcode"), 0755); err != nil {
		t.Fatal(err)
	}
	var files []string
	for i := 0; i < 8; i++ {
		p := filepath.Join("pkg", "g"+string(rune('a'+i))+".go")
		files = append(files, p)
		full := filepath.Join(repo, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package pkg\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	args := append([]string{"add", "."}, files...)
	run5390(t, repo, "git", args...)

	var wg sync.WaitGroup
	errs := make(chan error, len(files)+2)
	for _, p := range files {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			cmd := exec.Command(script, "mark", p)
			cmd.Dir = repo
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- &testErr5390{p, err, string(out)}
			}
		}(p)
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(script, "prune")
			cmd.Dir = repo
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- &testErr5390{"prune", err, string(out)}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("concurrent mark/prune failed: %v", e)
	}

	ledger := filepath.Join(repo, ".ggcode", "review-ledger.e1.txt")
	data, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("ledger missing: %v", err)
	}
	got := string(data)
	for _, p := range files {
		if !strings.Contains(got, "\t"+p+"\n") {
			t.Errorf("#5390 regression: mark of %s lost to concurrent prune", p)
		}
	}
}

type testErr5390 struct {
	path string
	err  error
	out  string
}

func (e *testErr5390) Error() string {
	return e.path + ": " + e.err.Error() + " (" + e.out + ")"
}

func script5390(t *testing.T) string {
	t.Helper()
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skipf("not in a git repo: %v", err)
	}
	script := filepath.Join(strings.TrimSpace(string(root)), "scripts", "dev", "review-coverage.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("review-coverage.sh missing: %v", err)
	}
	return script
}

func run5390(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v in %s: %v\n%s", name, args, dir, err, out)
	}
	return string(out)
}
