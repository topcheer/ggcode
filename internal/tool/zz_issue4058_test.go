package tool

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4058MaximizeIdempotentOnAllPlatforms(t *testing.T) {
	checks := []struct {
		file  string
		must  []string
		notIn []string
	}{
		{"desktop_control_linux.go",
			[]string{"windowstate", "--add", "--maximized"},
			[]string{"--toggle"}},
		{"desktop_control_darwin.go",
			[]string{"set fullscreen of front window to true"},
			[]string{"to not fullscreen of front window"}},
		{"desktop_control_windows.go",
			[]string{"pShowWindow.Call(uintptr(h), swMaximize)"},
			[]string{"pIsIconic"}},
	}
	for _, c := range checks {
		src, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatalf("read %s: %v", c.file, err)
		}
		s := string(src)
		for _, want := range c.must {
			if !strings.Contains(s, want) {
				t.Errorf("%s must contain %q (idempotent maximize, #4058)", c.file, want)
			}
		}
		for _, banned := range c.notIn {
			if strings.Contains(s, banned) {
				t.Errorf("%s must not contain %q (toggle/un-maximize semantics or dead code, #4058)", c.file, banned)
			}
		}
	}
}

func TestIssue4058SchemaDocumentsIdempotency(t *testing.T) {
	desc := DesktopControlTool{}.Description()
	if !strings.Contains(desc, "maximize_window is idempotent") {
		t.Errorf("description must document maximize_window idempotency, got: %s", desc)
	}
}
