//go:build windows

package restart

import (
	"os"
	"strings"
	"testing"
)

// TestIssue2775NoDelayedExpansion pins #2775: the generated .cmd restart
// script must not enable delayed expansion. The template never uses !var!
// syntax, but with `setlocal enabledelayedexpansion` cmd.exe treats every
// bare `!` in spliced args/paths as special - silently eaten or expanded -
// while winEscape only handles `"` and `%`. Removing the flag is the
// minimal fix: no delayed-expansion feature is lost, and `!` passes
// through literally.
func TestIssue2775NoDelayedExpansion(t *testing.T) {
	req := Request{
		PID:     4242,
		Binary:  `C:\Users\hello!\ggcode.exe`,
		WorkDir: `C:\Users\hello!\proj`,
		Args:    []string{`--prompt`, `"hello world!"`},
	}
	path, err := writePlatformScript(req)
	if err != nil {
		t.Fatalf("writePlatformScript: %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	script := string(data)

	if strings.Contains(script, "enabledelayedexpansion") {
		t.Fatal("script enables delayed expansion - every ! in spliced args/paths is eaten or !var!-expanded while winEscape does not escape it (#2775)")
	}

	// The literal ! must survive into the script text verbatim.
	if !strings.Contains(script, `hello world!`) {
		t.Fatalf("literal ! lost from spliced arg: %q", script)
	}
	if !strings.Contains(script, `hello!\ggcode.exe`) {
		t.Fatalf("literal ! lost from binary path: %q", script)
	}
}
