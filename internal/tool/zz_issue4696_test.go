package tool

import (
	"os"
	"strings"
	"testing"
)

// #4696: the active-window set_window_bounds path must chain
// getactivewindow -> windowmove %1 before the coordinates - the bare
// getactivewindow base made the X coordinate parse as the next command
// name (Unknown command), so the platform-default path always failed.
func TestIssue4696_ActiveWindowChainFixed(t *testing.T) {
	data, err := os.ReadFile("desktop_control_linux.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, `base := []string{"getactivewindow"}`+"\n") {
		t.Fatal("bare getactivewindow base must be gone (#4696)")
	}
	if !strings.Contains(text, `base := []string{"getactivewindow", "windowmove", "%1"}`) {
		t.Fatal("active-window base must chain windowmove %1 (#4696)")
	}
}
