package commands

import (
	"testing"
)

func TestInvalidateDisabledCache(t *testing.T) {
	InvalidateDisabledCache()
}

func TestPersistEnabledState(t *testing.T) {
	PersistEnabledState("test-command", true)
	PersistEnabledState("test-command", false)
	InvalidateDisabledCache()
}

func TestLoaderCommandDirs(t *testing.T) {
	l := &Loader{}
	dirs := l.CommandDirs()
	_ = dirs
}

func TestLoaderList(t *testing.T) {
	l := &Loader{}
	cmds := l.List()
	_ = cmds
}
