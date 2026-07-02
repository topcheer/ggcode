package tool

import (
	"os"
	"testing"
)

// withTestHome isolates HOME so tests don't pollute ~/.ggcode/todos/.
func withTestHome(t *testing.T) {
	t.Helper()
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpHome)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })
}
