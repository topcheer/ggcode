package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/session"
)

func TestPersistUserMessageRemoval_NilPersistSessionNoPanic(t *testing.T) {
	b := &ChatBridge{}

	b.mu.Lock()
	b.persistSession = nil
	b.mu.Unlock()

	b.persistUserMessageRemoval()
}

func TestPersistUserMessageRemoval_DeletedSessionGuardStillWorks(t *testing.T) {
	b := &ChatBridge{}
	ses := &session.Session{ID: "gone"}
	b.mu.Lock()
	b.persistSession = ses
	b.deletedSessions = map[string]struct{}{"gone": {}}
	b.mu.Unlock()

	b.persistUserMessageRemoval()
}
