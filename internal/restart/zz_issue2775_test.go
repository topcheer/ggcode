//go:build windows

package restart

import (
	"os"
	"strings"
	"testing"
)

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

	if !strings.Contains(script, `hello world!`) {
		t.Fatalf("literal ! lost from spliced arg: %q", script)
	}
	if !strings.Contains(script, `hello!\ggcode.exe`) {
		t.Fatalf("literal ! lost from binary path: %q", script)
	}
}
