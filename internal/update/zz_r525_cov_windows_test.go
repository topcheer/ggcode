package update

import (
	"os"
	"path/filepath"
	"testing"
)

func touchWindowsExe(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "ggcode", "ggcode.exe")
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDetectDualScopeWindowsBothPresent(t *testing.T) {
	base := t.TempDir()
	userDir := filepath.Join(base, "user")
	machineDir := filepath.Join(base, "machine")
	wantUser := touchWindowsExe(t, userDir)
	wantMachine := touchWindowsExe(t, machineDir)
	t.Setenv("LOCALAPPDATA", userDir)
	t.Setenv("ProgramFiles", machineDir)

	got := DetectDualScopeWindows()
	if got == nil {
		t.Fatal("expected dual-scope conflict, got nil")
	}
	if got.UserPath != wantUser || got.MachinePath != wantMachine {
		t.Fatalf("paths = %+v, want user=%s machine=%s", got, wantUser, wantMachine)
	}
}

func TestDetectDualScopeWindowsOnlyUserPresent(t *testing.T) {
	base := t.TempDir()
	userDir := filepath.Join(base, "user")
	touchWindowsExe(t, userDir)
	t.Setenv("LOCALAPPDATA", userDir)
	t.Setenv("ProgramFiles", filepath.Join(base, "no-machine"))

	if got := DetectDualScopeWindows(); got != nil {
		t.Fatalf("expected nil for single-scope, got %+v", got)
	}
}

func TestDetectDualScopeWindowsLocalAppDataFallback(t *testing.T) {
	base := t.TempDir()
	fallback := touchWindowsExe(t, filepath.Join(base, "AppData", "Local"))
	touchWindowsExe(t, filepath.Join(base, "pf"))
	t.Setenv("LOCALAPPDATA", "") // exercises detect.go:37-38 fallback
	t.Setenv("ProgramFiles", filepath.Join(base, "pf"))
	t.Setenv("HOME", base)

	got := DetectDualScopeWindows()
	if got == nil {
		t.Fatal("expected conflict via LOCALAPPDATA fallback")
	}
	if got.UserPath != fallback {
		t.Fatalf("UserPath = %q, want %q", got.UserPath, fallback)
	}
}
