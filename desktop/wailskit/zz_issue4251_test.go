package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/session"
)

// zz_issue4251_test.go - regression test for #4251: persistUserMessageRemoval
// dereferenced ses.ID (deletedSessions map lookup) BEFORE the nil check. When
// ClearCurrentSession leaves persistSession nil while a draining
// vision-fallback run still calls this function (stop + new-session sequence),
// the goroutine panicked. The fix orders the nil check before the map access.
func TestPersistUserMessageRemoval_NilPersistSessionNoPanic(t *testing.T) {
	b := &ChatBridge{}

	b.mu.Lock()
	b.persistSession = nil
	b.mu.Unlock()

	b.persistUserMessageRemoval()
}

// Non-nil snapshot must keep the normal guard behavior: a session recorded in
// deletedSessions causes an early return with no store call (tombstone skipped).
func TestPersistUserMessageRemoval_DeletedSessionGuardStillWorks(t *testing.T) {
	b := &ChatBridge{}
	ses := &session.Session{ID: "gone"}
	b.mu.Lock()
	b.persistSession = ses
	b.deletedSessions = map[string]struct{}{"gone": {}}
	b.mu.Unlock()

	b.persistUserMessageRemoval()
}
