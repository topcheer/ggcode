package tool

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestValidateBrowserProfileName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"work", false},
		{"personal-2", false},
		{"qa_h1.x", false},
		{"default", false}, // caller handles reserved words before validating
		{"", true},
		{"$(date +%H%M%S)", true}, // the literal incident name
		{"btnfix-$(date +%H%M%S)", true},
		{"has space", true},
		{"../escape", true},
		{"a/b", true},
		{".hidden-start", true}, // must start alnum
		{"shell;rm", true},
	}
	for _, c := range cases {
		err := validateBrowserProfileName(c.name)
		if (err != nil) != c.wantErr {
			t.Errorf("validateBrowserProfileName(%q) err=%v, wantErr=%v", c.name, err, c.wantErr)
		}
	}
}

func TestEvictLRUBrowserProfiles(t *testing.T) {
	b := &Browser{profiles: make(map[string]*browserProfile)}
	base := time.Now().Add(-time.Hour)
	// Five fake profiles, distinct lastUsed.
	for i, name := range []string{"p1", "p2", "p3", "p4", "p5"} {
		b.profiles[name] = &browserProfile{
			lastUsed:    base.Add(time.Duration(i) * time.Minute),
			allocCancel: func() {}, // no-op; evict calls it
			tabs:        make(map[string]*browserTab),
		}
	}
	b.evictLRUBrowserProfiles()
	if len(b.profiles) != maxBrowserProfiles {
		t.Fatalf("expected %d profiles after eviction, got %d", maxBrowserProfiles, len(b.profiles))
	}
	if _, ok := b.profiles["p1"]; ok {
		t.Error("oldest profile p1 should have been evicted (5 > cap 4)")
	}
	for _, keep := range []string{"p2", "p3", "p4", "p5"} {
		if _, ok := b.profiles[keep]; !ok {
			t.Errorf("recent profile %s must survive", keep)
		}
	}
}

func TestSingletonLockPID(t *testing.T) {
	dir := t.TempDir()
	if pid := singletonLockPID(dir); pid != 0 {
		t.Errorf("missing SingletonLock should return 0, got %d", pid)
	}
	// Chrome format: <hostname>-<pid>
	if err := os.Symlink("Mac-Studio.local-54991", filepath.Join(dir, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	if pid := singletonLockPID(dir); pid != 54991 {
		t.Errorf("expected pid 54991, got %d", pid)
	}
	// Unparseable target.
	dir2 := t.TempDir()
	if err := os.Symlink("not-a-pid", filepath.Join(dir2, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	if pid := singletonLockPID(dir2); pid != 0 {
		t.Errorf("unparseable lock should return 0, got %d", pid)
	}
}

func TestProcessAlive(t *testing.T) {
	if processAlive(0) {
		t.Error("pid 0 is never a real owner")
	}
	if processAlive(os.Getpid()) != true {
		t.Error("own process must be alive")
	}
	// pid 2^22 range is beyond most systems' pid_max; signal 0 should fail.
	if processAlive(4194304) {
		t.Log("pid 4194304 unexpectedly alive on this system; skipping strict assert")
	}
}

func TestGCStaleBrowserProfiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GGCODE_BROWSER_PROFILE_TTL_DAYS", "1") // 24h TTL
	root := browserProfilesRoot()
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}

	// dead-old: no SingletonLock, mtime pushed past TTL -> removed.
	deadOld := filepath.Join(root, "dead-old")
	if err := os.MkdirAll(deadOld, 0755); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(deadOld, past, past); err != nil {
		t.Fatal(err)
	}

	// dead-young: no lock, recent mtime (< 24h TTL) -> kept.
	deadYoung := filepath.Join(root, "dead-young")
	if err := os.MkdirAll(deadYoung, 0755); err != nil {
		t.Fatal(err)
	}

	// owned-live: lock points at OUR pid -> kept even when old.
	// (symlink first: creating it would refresh the dir mtime)
	ownedLive := filepath.Join(root, "owned-live")
	if err := os.MkdirAll(ownedLive, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("host-"+strconv.Itoa(os.Getpid()), filepath.Join(ownedLive, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(ownedLive, past, past); err != nil {
		t.Fatal(err)
	}

	// owned-dead: lock points at an obviously dead pid, old mtime -> removed.
	ownedDead := filepath.Join(root, "owned-dead")
	if err := os.MkdirAll(ownedDead, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("host-4194304", filepath.Join(ownedDead, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(ownedDead, past, past); err != nil {
		t.Fatal(err)
	}

	b := &Browser{profiles: make(map[string]*browserProfile)}
	// Mark one profile as live in-memory: its dir must survive.
	liveMem := filepath.Join(root, "live-mem")
	if err := os.MkdirAll(liveMem, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(liveMem, past, past); err != nil {
		t.Fatal(err)
	}
	b.profiles["live-mem"] = &browserProfile{lastUsed: time.Now()}

	b.gcStaleBrowserProfiles()

	for _, gone := range []string{"dead-old", "owned-dead"} {
		if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Errorf("%s should have been GC'd", gone)
		}
	}
	for _, keep := range []string{"dead-young", "owned-live", "live-mem"} {
		if _, err := os.Stat(filepath.Join(root, keep)); err != nil {
			t.Errorf("%s must survive GC: %v", keep, err)
		}
	}
}

func TestGCStaleBrowserProfilesDisabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GGCODE_BROWSER_PROFILE_GC", "0")
	t.Setenv("GGCODE_BROWSER_PROFILE_TTL_DAYS", "0")
	root := browserProfilesRoot()
	if err := os.MkdirAll(filepath.Join(root, "stale"), 0755); err != nil {
		t.Fatal(err)
	}
	b := &Browser{profiles: make(map[string]*browserProfile)}
	b.gcStaleBrowserProfiles()
	if _, err := os.Stat(filepath.Join(root, "stale")); err != nil {
		t.Error("GC=0 must not remove anything")
	}
}
