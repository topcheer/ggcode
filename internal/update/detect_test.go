package update

import (
	"runtime"
	"testing"
)

func TestPackageManagerHint(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/opt/homebrew/Cellar/ggcode/1.3.71/bin/ggcode", "brew"},
		{"/opt/homebrew/bin/ggcode", "brew"},
		{"/home/linuxbrew/.linuxbrew/bin/ggcode", "brew"},
		{"/home/user/scoop/apps/ggcode/current/ggcode.exe", "scoop"},
		{"/home/user/scoop/shims/ggcode.exe", "scoop"},
		{"C:\\Program Files\\ggcode\\ggcode.exe", "winget"},
		{"C:/Program Files/ggcode/ggcode.exe", "winget"},
		{"/home/user/AppData/Local/ggcode/ggcode.exe", "winget"},  // perUser winget (default)
		{"/usr/local/bin/ggcode", ""},                             // direct install — no hint
		{"/home/user/.local/share/ggcode/npm/v1.3.71/ggcode", ""}, // npm — no hint
	}
	for _, tt := range tests {
		got := PackageManagerHint(tt.path)
		if got != tt.want {
			t.Errorf("PackageManagerHint(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestFindOtherInstallsDoesNotPanic(t *testing.T) {
	// This test calls FindOtherInstalls which may discover real ggcode
	// binaries on the system and try to probe their version. Skip in CI
	// to avoid hanging on `ggcode version` (which may start the TUI).
	t.Skip("requires controlled environment without real ggcode installs")
}

func TestFormatOtherInstalls(t *testing.T) {
	// Empty
	if got := FormatOtherInstalls(nil); got != "" {
		t.Errorf("FormatOtherInstalls(nil) = %q, want empty", got)
	}

	// With items
	installs := []OtherInstall{
		{Path: "/opt/homebrew/bin/ggcode", Version: "v1.3.70", Source: "brew", Privileged: false},
		{Path: "/usr/bin/ggcode", Version: "", Source: "system", Privileged: true},
	}
	got := FormatOtherInstalls(installs)
	if got == "" {
		t.Fatal("expected non-empty output")
	}
	// Should contain both paths and the privileged marker
	want := "(admin)"
	if !contains(got, want) {
		t.Errorf("expected output to contain %q, got: %s", want, got)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestFindOtherInstallsSkipsSelf(t *testing.T) {
	// Find the current binary and ensure it's not in the results.
	currentPath := "/usr/local/bin/ggcode"
	if runtime.GOOS == "windows" {
		currentPath = `C:\Program Files\ggcode\ggcode.exe`
	}
	installs := FindOtherInstalls(currentPath)
	for _, inst := range installs {
		if inst.Path == currentPath {
			t.Errorf("FindOtherInstalls should skip the current binary, but found %s", inst.Path)
		}
	}
}
