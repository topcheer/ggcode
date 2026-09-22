package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/session"
)

func TestConsumeSessionSourceDefaultsToResume(t *testing.T) {
	m := &Model{}
	if got := m.consumeSessionSource(); got != "resume" {
		t.Errorf("expected default source resume, got %q", got)
	}
}

func TestConsumeSessionSourceExplicitAndCleared(t *testing.T) {
	m := &Model{}
	m.SetNextSessionSource("startup")
	if got := m.consumeSessionSource(); got != "startup" {
		t.Errorf("expected startup, got %q", got)
	}
	// Consumed: the next call falls back to the default instead of
	// leaking the previous transition's source into the next one.
	if got := m.consumeSessionSource(); got != "resume" {
		t.Errorf("expected source to be consumed (resume default), got %q", got)
	}
}

func TestFireSessionHooksNilGuards(t *testing.T) {
	// No config, no session, no agent — must be a no-op, not a panic.
	m := &Model{}
	m.fireSessionStartHooks()
	m.fireSessionEndHooks(nil, "exit")

	m.config = nil
	m.fireSessionEndHooks(&session.Session{ID: "s1"}, "exit")
}
