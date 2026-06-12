package stream

import (
	"strings"
	"testing"
)

func TestCheckFFmpeg(t *testing.T) {
	check := CheckFFmpeg()

	// On this machine, ffmpeg should be available (installed via brew)
	if !check.Available {
		// Check that error message contains install hints
		if !strings.Contains(check.Error, "brew install") {
			t.Errorf("error should contain install hints, got: %s", check.Error)
		}
		t.Logf("ffmpeg not available (expected on CI): %s", check.Error)
		return
	}

	if check.Path == "" {
		t.Error("Path should not be empty when available")
	}
	if check.Version == "" {
		t.Error("Version should not be empty when available")
	}
	if check.Major < 4 {
		t.Errorf("major version %d < 4", check.Major)
	}
}

func TestCheckFFmpegInstallHint(t *testing.T) {
	hint := ffmpegInstallHint()
	platforms := []string{"macOS", "Ubuntu", "Fedora", "Arch", "Windows"}
	for _, p := range platforms {
		if !strings.Contains(hint, p) {
			t.Errorf("install hint should mention %s", p)
		}
	}
	if !strings.Contains(hint, "4.0") {
		t.Error("install hint should mention minimum version 4.0")
	}
}
