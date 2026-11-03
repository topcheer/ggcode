package tool

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssue3114GCNeverDeletesSystemProfile(t *testing.T) {

	home, err := os.MkdirTemp("", "issue3114-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Cleanup(func() {
		os.RemoveAll(home) //nolint:errcheck // best-effort; async logger may still hold it briefly
	})
	t.Setenv("GGCODE_BROWSER_PROFILE_TTL_DAYS", "0")
	root := browserProfilesRoot()
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}

	sys := filepath.Join(root, "system")
	if err := os.MkdirAll(sys, 0755); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(sys, past, past); err != nil {
		t.Fatal(err)
	}

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

func TestIssue3114LockfilePath(t *testing.T) {
	got := lockfilePath(filepath.Join("u", "p"))
	want := filepath.Join(filepath.Join("u", "p"), "lockfile")
	if got != want {
		t.Fatalf("lockfilePath = %q", got)
	}
}
