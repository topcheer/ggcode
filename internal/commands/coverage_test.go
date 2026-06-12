package commands

import (
	"testing"
)

func TestInvalidateDisabledCache(t *testing.T) {
	// Should not panic
	InvalidateDisabledCache()
}

func TestPersistEnabledState(t *testing.T) {
	// Should not panic — writes to state file
	PersistEnabledState("test-command", true)
	PersistEnabledState("test-command", false)
	InvalidateDisabledCache()
}

func TestLoaderCommandDirs(t *testing.T) {
	l := &Loader{}
	dirs := l.CommandDirs()
	// May be empty if no dirs configured, but should not panic
	_ = dirs
}

func TestLoaderList(t *testing.T) {
	l := &Loader{}
	cmds := l.List()
	// May be empty if no commands loaded
	_ = cmds
}
