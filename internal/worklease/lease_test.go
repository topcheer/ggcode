package worklease

import (
	"errors"
	"testing"
	"time"
)

var baseTime = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func newMgr(t *testing.T) *Manager {
	t.Helper()
	return &Manager{Dir: t.TempDir(), Now: func() time.Time { return baseTime }}
}

func TestAcquireAndActive(t *testing.T) {
	m := newMgr(t)
	lease, conflicts, err := m.Acquire("r256", KindWorktree, "round", []string{"internal/tool/", "README.md"}, DefaultTTL)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts on empty registry: %+v", conflicts)
	}
	if lease.Owner != "r256" || len(lease.Scopes) != 2 {
		t.Fatalf("bad lease: %+v", lease)
	}
	if lease.ExpiresAt != baseTime.Add(DefaultTTL) {
		t.Fatalf("expiry = %v, want %v", lease.ExpiresAt, baseTime.Add(DefaultTTL))
	}
	active, err := m.Active()
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if len(active) != 1 || active[0].ID != lease.ID {
		t.Fatalf("active = %+v, want exactly the new lease", active)
	}
}

func TestExpirySelfRecovery(t *testing.T) {
	m := newMgr(t)
	if _, _, err := m.Acquire("a", KindDomain, "", []string{"pkg/"}, time.Hour); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// Crash of owner "a": clock advances past the TTL.
	m.Now = func() time.Time { return baseTime.Add(2 * time.Hour) }
	active, _ := m.Active()
	if len(active) != 0 {
		t.Fatalf("expired lease still active: %+v", active)
	}
	_, conflicts, err := m.Acquire("b", KindDomain, "", []string{"pkg/x.go"}, DefaultTTL)
	if err != nil {
		t.Fatalf("Acquire after expiry: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("stale lease not recovered, conflicts: %+v", conflicts)
	}
}

func TestConflictMatrix(t *testing.T) {
	cases := []struct {
		name         string
		mine, theirs string
		wantConflict bool
	}{
		{"exact same file", "a.go", "a.go", true},
		{"dir covers file", "internal/tool/", "internal/tool/edit.go", true},
		{"file inside dir", "internal/tool/edit.go", "internal/tool/", true},
		{"nested dirs", "internal/", "internal/tool/", true},
		{"sibling dirs", "internal/tool/", "internal/tui/", false},
		{"disjoint files", "internal/x/a.go", "internal/y/a.go", false},
		{"glob matches file", "*.go", "main.go", true},
		{"scoped glob under dir", "internal/x/*.go", "internal/x/sub/a.go", true},
		{"glob misses sibling", "internal/x/*.go", "internal/y/a.go", false},
		{"dir vs glob overlap", "internal/x/", "internal/x/*.go", true},
		{"dot prefix normalized", "./internal/tool/", "internal/tool/", true},
		{"workspace root covers all", ".", "anything/else.go", true},
		{"trailing slash marks a distinct dir", "README.md/", "README.md", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMgr(t)
			if _, _, err := m.Acquire("other", KindDomain, "", []string{tc.theirs}, DefaultTTL); err != nil {
				t.Fatalf("seed acquire: %v", err)
			}
			_, conflicts, err := m.Acquire("me", KindDomain, "", []string{tc.mine}, DefaultTTL)
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			if got := len(conflicts) > 0; got != tc.wantConflict {
				t.Fatalf("overlap(%q, %q) = %v, conflicts %+v", tc.mine, tc.theirs, got, conflicts)
			}
		})
	}
}

func TestOwnLeaseRefresh(t *testing.T) {
	m := newMgr(t)
	for i := 0; i < 2; i++ {
		_, conflicts, err := m.Acquire("me", KindDomain, "", []string{"pkg/"}, DefaultTTL)
		if err != nil {
			t.Fatalf("Acquire #%d: %v", i, err)
		}
		if len(conflicts) != 0 {
			t.Fatalf("Acquire #%d: own lease reported as conflict: %+v", i, conflicts)
		}
	}
	active, _ := m.Active()
	if len(active) != 1 {
		t.Fatalf("re-acquire should replace, got %d leases", len(active))
	}
}

func TestReleaseIdempotent(t *testing.T) {
	m := newMgr(t)
	if err := m.Release("nobody"); err != nil {
		t.Fatalf("release unknown owner: %v", err)
	}
	if _, _, err := m.Acquire("me", KindDomain, "", []string{"pkg/"}, DefaultTTL); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := m.Release("me"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	active, _ := m.Active()
	if len(active) != 0 {
		t.Fatalf("lease survived release: %+v", active)
	}
	if err := m.Release("me"); err != nil {
		t.Fatalf("second release must stay idempotent: %v", err)
	}
}

func TestHeartbeatExtends(t *testing.T) {
	m := newMgr(t)
	if _, _, err := m.Acquire("me", KindDomain, "", []string{"pkg/"}, time.Hour); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// Halfway through the TTL the owner heartbeats for another hour.
	m.Now = func() time.Time { return baseTime.Add(30 * time.Minute) }
	latest, err := m.Heartbeat("me", time.Hour)
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if want := baseTime.Add(30 * time.Minute).Add(time.Hour); !latest.Equal(want) {
		t.Fatalf("expiry = %v, want %v", latest, want)
	}
	// 45min in: without the heartbeat the lease would be gone; with it,
	// still active until 13:30.
	m.Now = func() time.Time { return baseTime.Add(45 * time.Minute) }
	active, _ := m.Active()
	if len(active) != 1 {
		t.Fatalf("heartbeat did not extend lease: %+v", active)
	}
	if _, err := m.Heartbeat("ghost", time.Hour); !errors.Is(err, ErrNoLease) {
		t.Fatalf("unknown owner heartbeat err = %v, want ErrNoLease", err)
	}
}

func TestCrossManagerPersistence(t *testing.T) {
	dir := t.TempDir()
	m1 := &Manager{Dir: dir, Now: func() time.Time { return baseTime }}
	if _, _, err := m1.Acquire("a", KindWorktree, "", []string{".ggcode/worktrees/a/", "pkg/"}, DefaultTTL); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// A separate process (fresh Manager) must see the same registry and
	// flag overlapping domains.
	m2 := &Manager{Dir: dir, Now: func() time.Time { return baseTime }}
	active, err := m2.Active()
	if err != nil || len(active) != 1 {
		t.Fatalf("cross-process Active = %+v err %v", active, err)
	}
	conflicts, err := m2.Conflicts("b", []string{"pkg/x.go"})
	if err != nil {
		t.Fatalf("Conflicts: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].Owner != "a" {
		t.Fatalf("cross-process conflicts = %+v", conflicts)
	}
	// Own leases are invisible to own conflict checks.
	conflicts, err = m2.CheckPath("a", "pkg/x.go")
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("own CheckPath = %+v err %v", conflicts, err)
	}
}

func TestInvalidScopesRejected(t *testing.T) {
	m := newMgr(t)
	if _, _, err := m.Acquire("me", KindDomain, "", []string{"../escape", "", "   "}, DefaultTTL); err == nil {
		t.Fatal("Acquire with only invalid scopes must fail")
	}
	if _, _, err := m.Acquire("", KindDomain, "", []string{"pkg/"}, DefaultTTL); err == nil {
		t.Fatal("Acquire with empty owner must fail")
	}
	// A scope list mixing invalid and valid entries keeps the valid one.
	lease, _, err := m.Acquire("me", KindDomain, "", []string{"../escape", "pkg/"}, DefaultTTL)
	if err != nil || len(lease.Scopes) != 1 || lease.Scopes[0] != "pkg/" {
		t.Fatalf("mixed scopes = %+v err %v", lease, err)
	}
}

func TestFormatConflicts(t *testing.T) {
	m := newMgr(t)
	if _, _, err := m.Acquire("other", KindDomain, "", []string{"internal/tool/"}, DefaultTTL); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	conflicts, err := m.Conflicts("me", []string{"internal/tool/edit.go"})
	if err != nil || len(conflicts) == 0 {
		t.Fatalf("Conflicts = %+v err %v", conflicts, err)
	}
	out := FormatConflicts(conflicts)
	if !contains(out, "other") || !contains(out, "dir \"internal/tool/\"") || !contains(out, "expires") {
		t.Fatalf("FormatConflicts output missing details: %q", out)
	}
	if FormatConflicts(nil) != "" {
		t.Fatal("empty conflicts must format to empty string")
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
