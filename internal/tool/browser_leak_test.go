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
		{"default", false},
		{"", true},
		{"$(date +%H%M%S)", true},
		{"btnfix-$(date +%H%M%S)", true},
		{"has space", true},
		{"../escape", true},
		{"a/b", true},
		{".hidden-start", true},
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

	for i, name := range []string{"p1", "p2", "p3", "p4", "p5"} {
		b.profiles[name] = &browserProfile{
			lastUsed:    base.Add(time.Duration(i) * time.Minute),
			allocCancel: func() {},
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

	if err := os.Symlink("Mac-Studio.local-54991", filepath.Join(dir, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	if pid := singletonLockPID(dir); pid != 54991 {
		t.Errorf("expected pid 54991, got %d", pid)
	}

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

	if processAlive(4194304) {
		t.Log("pid 4194304 unexpectedly alive on this system; skipping strict assert")
	}
}

func TestGCStaleBrowserProfiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GGCODE_BROWSER_PROFILE_TTL_DAYS", "1")
	root := browserProfilesRoot()
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}

	deadOld := filepath.Join(root, "dead-old")
	if err := os.MkdirAll(deadOld, 0755); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(deadOld, past, past); err != nil {
		t.Fatal(err)
	}

	deadYoung := filepath.Join(root, "dead-young")
	if err := os.MkdirAll(deadYoung, 0755); err != nil {
		t.Fatal(err)
	}

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

// helper: a profile whose teardown is observable.
func newObservedProfile(lastUsed time.Time) (*browserProfile, *bool, *bool) {
	allocCancelled, tabCancelled := false, false
	p := &browserProfile{
		lastUsed:    lastUsed,
		allocCancel: func() { allocCancelled = true },
		tabs:        map[string]*browserTab{"default": {cancel: func() { tabCancelled = true }}},
	}
	return p, &allocCancelled, &tabCancelled
}

// Agents almost never call action=close, so an idle browser (and, on an
// animated / 3D page, its 100%-CPU SwiftShader GPU helper) used to outlive
// its last call until the LRU cap or process exit. The reaper ends it after
// the idle timeout.
func TestReapIdleBrowserProfiles(t *testing.T) {
	t.Setenv("GGCODE_BROWSER_IDLE_SECS", "")
	b := &Browser{profiles: make(map[string]*browserProfile)}
	now := time.Now()

	idleP, allocCancelled, tabCancelled := newObservedProfile(now.Add(-defaultBrowserIdleTimeout - time.Minute))
	freshP, freshAlloc, _ := newObservedProfile(now.Add(-10 * time.Second))
	b.profiles["idle"] = idleP
	b.profiles["fresh"] = freshP

	b.reapIdleBrowserProfiles(now)

	if _, ok := b.profiles["idle"]; ok {
		t.Fatal("idle profile must be removed")
	}
	if !*allocCancelled || !*tabCancelled {
		t.Fatalf("idle profile must be torn down (tab cancel=%v, allocator cancel=%v)", *tabCancelled, *allocCancelled)
	}
	if _, ok := b.profiles["fresh"]; !ok {
		t.Fatal("recently used profile must survive")
	}
	if *freshAlloc {
		t.Fatal("recently used profile must not be cancelled")
	}
	if idleP.tabs != nil {
		t.Fatal("torn-down profile must have no tabs left")
	}
}

func TestReapIdleBrowserProfilesDisabledByEnv(t *testing.T) {
	t.Setenv("GGCODE_BROWSER_IDLE_SECS", "0")
	b := &Browser{profiles: make(map[string]*browserProfile)}
	now := time.Now()
	_, allocCancelled, _ := newObservedProfile(now.Add(-time.Hour))
	p := &browserProfile{lastUsed: now.Add(-time.Hour), allocCancel: func() { *allocCancelled = true }}
	b.profiles["idle"] = p

	b.reapIdleBrowserProfiles(now)

	if _, ok := b.profiles["idle"]; !ok || *allocCancelled {
		t.Fatal("GGCODE_BROWSER_IDLE_SECS=0 must disable idle reaping")
	}
}

func TestReapIdleBrowserProfilesEnvOverride(t *testing.T) {
	t.Setenv("GGCODE_BROWSER_IDLE_SECS", "3600") // one hour: a 10-min idle profile stays
	b := &Browser{profiles: make(map[string]*browserProfile)}
	now := time.Now()
	p, allocCancelled, _ := newObservedProfile(now.Add(-10 * time.Minute))
	b.profiles["idle"] = p
	b.reapIdleBrowserProfiles(now)
	if _, ok := b.profiles["idle"]; !ok || *allocCancelled {
		t.Fatal("10-minute idle profile must survive a 1-hour threshold")
	}

	t.Setenv("GGCODE_BROWSER_IDLE_SECS", "60") // one minute: a 10-min idle profile goes
	b.reapIdleBrowserProfiles(now)
	if _, ok := b.profiles["idle"]; ok || !*allocCancelled {
		t.Fatal("10-minute idle profile must be reaped under a 1-minute threshold")
	}
}

func TestBrowserIdleTimeoutEnvParsing(t *testing.T) {
	t.Setenv("GGCODE_BROWSER_IDLE_SECS", "")
	if got := browserIdleTimeout(); got != defaultBrowserIdleTimeout {
		t.Fatalf("default = %s, want %s", got, defaultBrowserIdleTimeout)
	}
	for in, want := range map[string]time.Duration{"0": 0, "90": 90 * time.Second, "-5": defaultBrowserIdleTimeout, "abc": defaultBrowserIdleTimeout, " 120 ": 120 * time.Second} {
		t.Setenv("GGCODE_BROWSER_IDLE_SECS", in)
		if got := browserIdleTimeout(); got != want {
			t.Errorf("env %q -> %s, want %s", in, got, want)
		}
	}
}

// Idle accounting runs from the end of an action, so a long action is not
// reaped mid-flight by the idle reaper.
func TestTouchProfileAfterAction(t *testing.T) {
	b := &Browser{profiles: make(map[string]*browserProfile)}
	old := time.Now().Add(-time.Hour)
	p, _, _ := newObservedProfile(old)
	b.profiles["default"] = p

	b.touchProfile("default")
	if !p.lastUsed.After(old.Add(30 * time.Minute)) {
		t.Fatal("touchProfile must refresh lastUsed")
	}
	b.touchProfile("does-not-exist") // must not panic / create anything
	if len(b.profiles) != 1 {
		t.Fatal("touchProfile must not create profiles")
	}
}
