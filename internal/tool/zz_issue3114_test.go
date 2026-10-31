package tool

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #3114 probes: the profile GC must never delete (1) the cross-process
// persistent 'system' profile (Windows has zero live-process protection -
// no SingletonLock, processAlive stub false - so TTL expiry alone used to
// RemoveAll a LIVE Chrome's user-data-dir after 7 idle days), and on
// Windows (2) any profile dir holding Chromium's 'lockfile' (the Windows
// SingletonLock equivalent).

func TestIssue3114GCNeverDeletesSystemProfile(t *testing.T) {
	// Deliberately NOT t.TempDir(): the framework's RemoveAll cleanup races
	// the async debug.Log writer (gcStale's eviction path logs, and the
	// global logger flushes to $HOME/.ggcode on its own goroutine). With
	// t.TempDir the cleanup ran after HOME already pointed back here,
	// reliably unlinkat-failing on CI ("directory not empty") while the
	// test's own assertions all passed in 0.00s. A plain temp dir without
	// framework cleanup sidesteps the race; best-effort removal at the end
	// tolerates a late writer (the dir is under os.TempDir either way).
	home, err := os.MkdirTemp("", "issue3114-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Cleanup(func() {
		os.RemoveAll(home) //nolint:errcheck // best-effort; async logger may still hold it briefly
	})
	t.Setenv("GGCODE_BROWSER_PROFILE_TTL_DAYS", "0") // TTL 0: everything is "stale"
	root := browserProfilesRoot()
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	// system: mtime ancient, no lock of any kind, NOT in the live map -
	// every other guard is deliberately off; only the exemption saves it.
	sys := filepath.Join(root, "system")
	if err := os.MkdirAll(sys, 0755); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(sys, past, past); err != nil {
		t.Fatal(err)
	}
	// Control: a TTL-aged ordinary profile without any lock IS removed -
	// the exemption must not neuter the GC entirely.
	stale := filepath.Join(root, "stale-nolock")
	if err := os.MkdirAll(stale, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatal(err)
	}

	b := &Browser{profiles: make(map[string]*browserProfile)}
	b.gcStaleBrowserProfiles()

	if _, err := os.Stat(sys); err != nil {
		t.Fatalf("#3114: persistent 'system' profile was GC'd (live Chrome data destroyed): %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("control: unlocked TTL-aged profile should still be GC'd")
	}
}

// lockfilePath is the pure Windows-path helper; assert its shape so the
// Windows-only stat probe targets the right artifact.
func TestIssue3114LockfilePath(t *testing.T) {
	got := lockfilePath(filepath.Join("u", "p"))
	want := filepath.Join(filepath.Join("u", "p"), "lockfile")
	if got != want {
		t.Fatalf("lockfilePath = %q", got)
	}
}
