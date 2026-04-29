package update

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRestartArgs(t *testing.T) {
	svc := NewService("v1.0.0", "/tmp/ggcode", "", t.TempDir())

	// No config, no resume
	args := svc.restartArgs("")
	if len(args) != 0 {
		t.Errorf("expected empty args, got %v", args)
	}

	// With config
	svc.ConfigPath = "/tmp/ggcode.yaml"
	args = svc.restartArgs("")
	if len(args) != 2 || args[0] != "--config" {
		t.Errorf("expected --config arg, got %v", args)
	}

	// With resume
	args = svc.restartArgs("session-123")
	found := false
	for i, a := range args {
		if a == "--resume" && i+1 < len(args) && args[i+1] == "session-123" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected --resume session-123 in args, got %v", args)
	}
}

func TestResolveTargetPaths(t *testing.T) {
	tmp := t.TempDir()
	svc := NewService("v1.0.0", filepath.Join(tmp, "ggcode"), "", t.TempDir())

	paths, restart, err := svc.resolveTargetPaths("v1.1.0")
	if err != nil {
		t.Fatalf("resolveTargetPaths error: %v", err)
	}
	if len(paths) < 1 {
		t.Error("expected at least 1 path")
	}
	if restart == "" {
		t.Error("expected non-empty restart path")
	}
}

func TestResolveTargetPaths_EmptyExec(t *testing.T) {
	svc := NewService("v1.0.0", "", "", t.TempDir())
	_, _, err := svc.resolveTargetPaths("v1.1.0")
	if err == nil {
		t.Error("expected error for empty exec path")
	}
}

func TestHelperBinaryName(t *testing.T) {
	name := helperBinaryName()
	if runtime.GOOS == "windows" {
		if name != "ggcode-update-helper.exe" {
			t.Errorf("expected .exe suffix, got %s", name)
		}
	} else {
		if name != "ggcode-update-helper" {
			t.Errorf("expected ggcode-update-helper, got %s", name)
		}
	}
}

func TestUniquePaths(t *testing.T) {
	tests := []struct {
		input    []string
		expected int
	}{
		{[]string{"/a", "/b", "/a"}, 2},
		{[]string{"/a", "  /a  "}, 1},
		{[]string{"", "  ", "/a"}, 1},
		{[]string{}, 0},
		{nil, 0},
	}
	for _, tt := range tests {
		got := uniquePaths(tt.input)
		if len(got) != tt.expected {
			t.Errorf("uniquePaths(%v): expected %d, got %d (%v)", tt.input, tt.expected, len(got), got)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	got := firstNonEmpty("", "  ", "hello", "world")
	if got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	got = firstNonEmpty()
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	got = firstNonEmpty("", "  ")
	if got != "" {
		t.Errorf("expected empty for whitespace-only, got %q", got)
	}
}

func TestMustGetwd(t *testing.T) {
	wd := mustGetwd()
	if wd == "" {
		t.Error("expected non-empty working directory")
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "sub", "dst.txt")

	if err := os.WriteFile(src, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst, 0755); err != nil {
		t.Fatalf("copyFile error: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if string(data) != "content" {
		t.Errorf("expected 'content', got %q", string(data))
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("Stat error: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("expected mode 0755, got %o", info.Mode().Perm())
	}
}

func TestCopyFile_MissingSrc(t *testing.T) {
	err := copyFile("/nonexistent/file.txt", "/tmp/dst.txt", 0644)
	if err == nil {
		t.Error("expected error for missing source")
	}
}

func TestVersionStringOrDev(t *testing.T) {
	got := versionStringOrDev("")
	if got == "" {
		t.Error("expected non-empty for empty input (should use version.Display())")
	}
	got = versionStringOrDev("v1.2.3")
	if got != "v1.2.3" {
		t.Errorf("expected 'v1.2.3', got %q", got)
	}
}

func TestDetectWrapperKind(t *testing.T) {
	tests := []struct {
		path     string
		envVal   string
		expected string
	}{
		{"", "npm", "npm"},
		{"", "python", "python"},
		{"/home/.cache/ggcode/npm/v1.0.0/linux-x64/ggcode", "", "npm"},
		{"/home/.cache/ggcode/python/v1.0.0/linux-x64/ggcode", "", "python"},
		{"/usr/local/bin/ggcode", "", wrapperKindNative},
	}
	for _, tt := range tests {
		if tt.envVal != "" {
			t.Setenv("GGCODE_WRAPPER_KIND", tt.envVal)
		} else {
			t.Setenv("GGCODE_WRAPPER_KIND", "")
		}
		got := detectWrapperKind(tt.path)
		if got != tt.expected {
			t.Errorf("detectWrapperKind(%q, env=%q) = %q, want %q", tt.path, tt.envVal, got, tt.expected)
		}
	}
}

func TestParseReleaseVersion(t *testing.T) {
	tests := []struct {
		input    string
		ok       bool
		expected [3]int
	}{
		{"v1.2.3", true, [3]int{1, 2, 3}},
		{"1.2.3", true, [3]int{1, 2, 3}},
		{"v0.0.1", true, [3]int{0, 0, 1}},
		{"invalid", false, [3]int{}},
		{"v1.2", false, [3]int{}},
		{"v1.2.3.4", false, [3]int{}},
		{"v1.2.x", false, [3]int{}},
	}
	for _, tt := range tests {
		got, ok := parseReleaseVersion(tt.input)
		if ok != tt.ok {
			t.Errorf("parseReleaseVersion(%q) ok = %v, want %v", tt.input, ok, tt.ok)
		}
		if ok && got != tt.expected {
			t.Errorf("parseReleaseVersion(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestIsComparableRelease(t *testing.T) {
	if !isComparableRelease("v1.2.3") {
		t.Error("expected v1.2.3 to be comparable")
	}
	if isComparableRelease("dev") {
		t.Error("expected 'dev' to not be comparable")
	}
}

func TestHttpClient(t *testing.T) {
	svc := NewService("v1.0.0", "/tmp/ggcode", "", t.TempDir())
	client := svc.httpClient()
	if client == nil {
		t.Error("expected non-nil client")
	}
}
