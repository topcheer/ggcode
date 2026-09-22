package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendCwdSentinelPosix(t *testing.T) {
	got := appendCwdSentinel("ls -la", "sh")
	if !strings.HasSuffix(got, `printf '%s\n' "__GGCODE_CWD__$(pwd -P)"`) {
		t.Fatalf("unexpected posix sentinel: %q", got)
	}
	if !strings.HasPrefix(got, "ls -la\n") {
		t.Fatalf("sentinel must be newline-separated: %q", got)
	}
}

func TestAppendCwdSentinelPowerShell(t *testing.T) {
	got := appendCwdSentinel("Get-ChildItem", "powershell")
	if !strings.HasSuffix(got, `Write-Output "__GGCODE_CWD__$(Get-Location)"`) {
		t.Fatalf("unexpected powershell sentinel: %q", got)
	}
	if !strings.HasPrefix(got, "Get-ChildItem\n") {
		t.Fatalf("sentinel must be newline-separated: %q", got)
	}
}

func TestAppendCwdSentinelNewlineSeparated(t *testing.T) {
	// A command ending in '&' must not turn into "& ;" (POSIX syntax error).
	got := appendCwdSentinel("longtask &", "bash")
	if strings.Contains(got, "& ;") || strings.Contains(got, "&;") {
		t.Fatalf("ampersand mangled: %q", got)
	}
	lines := strings.Split(got, "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "printf") {
		t.Fatalf("sentinel must start on its own line: %q", last)
	}
}

func TestExtractCwdMarker(t *testing.T) {
	out := "line1\nline2\n__GGCODE_CWD__/tmp/build\ntail\n__GGCODE_CWD__/tmp/final\n"
	cleaned, dir, ok := extractCwdMarker(out)
	if !ok {
		t.Fatal("marker not found")
	}
	if dir != "/tmp/final" {
		t.Fatalf("want last marker dir /tmp/final, got %q", dir)
	}
	if strings.Contains(cleaned, "__GGCODE_CWD__") {
		t.Fatalf("marker leaked into cleaned output: %q", cleaned)
	}
	if !strings.Contains(cleaned, "line1") || !strings.Contains(cleaned, "tail") {
		t.Fatalf("real output lost: %q", cleaned)
	}
}

func TestExtractCwdMarkerAbsent(t *testing.T) {
	cleaned, dir, ok := extractCwdMarker("normal output\n")
	if ok || dir != "" {
		t.Fatalf("unexpected marker: ok=%v dir=%q", ok, dir)
	}
	if cleaned != "normal output\n" {
		t.Fatalf("output mutated without marker: %q", cleaned)
	}
}

func TestLastCwdMarkerLine(t *testing.T) {
	got := lastCwdMarkerLine([]string{"a", "__GGCODE_CWD__/x", "noise", "__GGCODE_CWD__/y "})
	if got != "/y" {
		t.Fatalf("want /y, got %q", got)
	}
	if lastCwdMarkerLine([]string{"a", "b"}) != "" {
		t.Fatal("expected empty for marker-free lines")
	}
}

func TestStripCwdMarkerLines(t *testing.T) {
	got := stripCwdMarkerLines([]string{"keep", "__GGCODE_CWD__/x", "also keep"})
	if len(got) != 2 || got[0] != "keep" || got[1] != "also keep" {
		t.Fatalf("unexpected result: %q", got)
	}
}

func TestAdoptableCwd(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := adoptableCwd(root, sub); got != filepath.Clean(sub) {
		t.Fatalf("want %q, got %q", filepath.Clean(sub), got)
	}
	if got := adoptableCwd(root, filepath.Join(root, "missing")); got != "" {
		t.Fatalf("missing dir must be rejected, got %q", got)
	}
	if got := adoptableCwd(root, "/etc"); got != "" {
		t.Fatalf("dir outside workspace must be rejected, got %q", got)
	}
	if got := adoptableCwd(root, "relative/path"); got != "" {
		t.Fatalf("relative dir must be rejected, got %q", got)
	}
	if got := adoptableCwd("", sub); got == "" {
		t.Fatalf("no-workspace mode should accept valid absolute dir, got %q", got)
	}
	if got := adoptableCwd(root, ""); got != "" {
		t.Fatalf("empty dir must be rejected, got %q", got)
	}
}

func TestWorkingDirForCommand(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	rc := RunCommand{WorkingDir: root, cwdState: newShellCwdState()}
	if got := rc.workingDirForCommand(); got != root {
		t.Fatalf("fresh state should fall back to WorkingDir: got %q", got)
	}
	rc.cwdState.set(sub)
	if got := rc.workingDirForCommand(); got != filepath.Clean(sub) {
		t.Fatalf("persisted cwd not adopted: got %q", got)
	}
	rc.cwdState.set(filepath.Join(root, "gone"))
	if got := rc.workingDirForCommand(); got != root {
		t.Fatalf("vanished dir should fall back to WorkingDir: got %q", got)
	}
	// Zero-value tool (nil state) must stay disabled.
	rc2 := RunCommand{WorkingDir: root}
	if got := rc2.workingDirForCommand(); got != root {
		t.Fatalf("nil state must disable persistence: got %q", got)
	}
}

func TestShellCwdStateNilSafety(t *testing.T) {
	var s *shellCwdState
	if s.get() != "" {
		t.Fatal("nil get must be empty")
	}
	s.set("/tmp") // must not panic
	if s.get() != "" {
		t.Fatal("nil set must be a no-op")
	}
}
