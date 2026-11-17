package wailskit

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// #5750 flare-quarantine guard for the #5483/#2741 TempDir teardown family.
//
// TestIssue2741_LoadSessionBumpsGenerationOnInstall failed exactly once on
// CI (run 43854925211 attempt 1, 2026-11-16T11:21:35Z) when a bridge-spawned
// writer recreated an entry under $HOME/.ggcode between t.TempDir's RemoveAll
// readdir and its final unlinkat:
//
//	TempDir RemoveAll cleanup: unlinkat .../001/.ggcode: directory not empty
//
// Local 10x reruns never reproduce it, the CI re-run was green, and nothing
// similar has fired since 8b8feb704 - but each single-shot costs a full
// red-run investigation. Rather than per-instance re-triage, the family's
// HOME fixture now removes with bounded late-writer grace: a racing writer
// is waited out (loudly logged, test stays green) while a writer that never
// stops still fails the test with the surviving paths listed, so the
// real-leak signal is preserved.

const (
	issue5750RemoveAttempts  = 6
	issue5750InitialBackoff  = 25 * time.Millisecond
	issue5750MaxSurvivorList = 16
)

type issue5750RemovalOutcome struct {
	ok        bool
	retries   int
	survivors []string
	lastErr   error
}

// issue5750RemoveTreeWithGrace removes dir, retrying while a late writer (a
// goroutine that outlived both the test body and bridge.Close) keeps
// recreating entries inside it. Total worst-case wait with the production
// constants is ~1.6s; the green path still costs a single RemoveAll.
func issue5750RemoveTreeWithGrace(dir string, attempts int, backoff time.Duration) issue5750RemovalOutcome {
	var out issue5750RemovalOutcome
	for i := 0; i < attempts; i++ {
		err := os.RemoveAll(dir)
		if err == nil {
			if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
				out.ok = true
				out.retries = i
				return out
			}
			err = fmt.Errorf("directory recreated right after removal")
		}
		out.lastErr = err
		out.survivors = issue5750ListEntries(dir, issue5750MaxSurvivorList)
		if i == attempts-1 {
			break
		}
		time.Sleep(backoff)
		backoff *= 2
	}
	return out
}

// issue5750ListEntries lists up to max surviving paths under dir (relative),
// for the triage log line when removal fails or needed retries.
func issue5750ListEntries(dir string, max int) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return nil
		}
		if len(out) < max {
			out = append(out, strings.TrimPrefix(p, dir+string(os.PathSeparator)))
		}
		if len(out) >= max {
			return fs.SkipAll
		}
		return nil
	})
	return out
}

// issue5750TempHome is t.TempDir()+t.Setenv("HOME", ...) for the bridge
// teardown family, with late-writer-tolerant cleanup instead of t.TempDir's
// single RemoveAll shot. Cleanup order is LIFO, so the removal runs while
// HOME still points into the doomed tree: late writers keep targeting the
// scratch dir instead of the developer's real home.
func issue5750TempHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "issue5750-home-")
	if err != nil {
		t.Fatalf("issue #5750 temp home: %v", err)
	}
	t.Setenv("HOME", dir)
	t.Cleanup(func() {
		out := issue5750RemoveTreeWithGrace(dir, issue5750RemoveAttempts, issue5750InitialBackoff)
		switch {
		case !out.ok:
			t.Errorf("issue #5750: temp HOME %s survived %d removal attempts (last error: %v); late writer never stopped, surviving entries: %v",
				dir, issue5750RemoveAttempts, out.lastErr, out.survivors)
		case out.retries > 0:
			t.Logf("issue #5750 flare guard: teardown of the temp HOME raced a late writer (survivors seen: %v); clean after %d retries - async-component-vs-cleanup family shape, see #5483/#5750",
				out.survivors, out.retries)
		}
	})
	return dir
}

// The rescue contract: while the tree is unremovable (here: read-only
// parent, standing in for the CI late-writer window), removal fails and
// retries; once the interference stops (permissions restored mid-grace),
// removal converges and reports the retries it needed.
func TestIssue5750TeardownGraceRetriesUntilRemovable(t *testing.T) {
	issue5750SkipIfChmodIneffective(t)
	base := t.TempDir()
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = os.Chmod(home, 0o700)
	}()
	out := issue5750RemoveTreeWithGrace(home, issue5750RemoveAttempts, issue5750InitialBackoff)
	if !out.ok {
		t.Fatalf("grace removal never converged: lastErr=%v survivors=%v", out.lastErr, out.survivors)
	}
	if out.retries == 0 {
		t.Fatal("read-only window must force at least one failed attempt before convergence")
	}
}

// The real-leak contract: a tree that never becomes removable must still
// surface as a failure with the surviving entries listed - the grace window
// must not become a silent leak swallower.
func TestIssue5750UnremovableHomeStillSurfaces(t *testing.T) {
	issue5750SkipIfChmodIneffective(t)
	base := t.TempDir()
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatal(err)
	}
	out := issue5750RemoveTreeWithGrace(home, 3, time.Millisecond)
	_ = os.Chmod(home, 0o700)
	if out.ok {
		t.Fatal("never-removable tree must not be swallowed by the grace window")
	}
	if out.lastErr == nil {
		t.Fatal("failed removal must record the last error for triage")
	}
	if len(out.survivors) == 0 {
		t.Fatal("failed removal must list surviving entries for triage")
	}
	_ = os.RemoveAll(base)
}

func issue5750SkipIfChmodIneffective(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("read-only-dir semantics are not portable to windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions; interference would not fail removal")
	}
}

// Anchor: ChatBridge must drain the session cron scheduler at Close and on
// InitAgent rebind - the patrol goroutine and armed timers are the provable
// async survivors of the teardown family.
func TestIssue5750CronSchedulerDrainedAtCloseAndRebind(t *testing.T) {
	b, err := os.ReadFile("chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.Contains(src, "cronSched := b.cronScheduler") || !strings.Contains(src, "cronSched.Shutdown()") {
		t.Fatal("#5750: ChatBridge.Close must drain the session cron scheduler (patrol goroutine + armed timers)")
	}
	if !strings.Contains(src, "prev.Shutdown()") {
		t.Fatal("#5750: InitAgent must shut down the previous cron scheduler before rebinding (per-rebind patrol leak)")
	}
}
