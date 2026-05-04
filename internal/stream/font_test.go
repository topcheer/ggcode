package stream

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFindSystemFonts(t *testing.T) {
	fonts := FindSystemFonts()
	t.Logf("Found %d system fonts", len(fonts))

	// On any reasonable system there should be at least one font
	if len(fonts) == 0 {
		t.Log("WARNING: no system fonts found — rendering will use built-in Latin-only font")
	}

	// Check that results have valid paths
	for _, f := range fonts {
		if f.Path == "" {
			t.Error("font has empty path")
		}
		if f.Name == "" {
			t.Error("font has empty name")
		}
		t.Logf("  %s (CJK=%v): %s", f.Name, f.IsCJK, f.Path)
	}
}

func TestFindCJKFont(t *testing.T) {
	path := FindCJKFont()
	if path == "" {
		t.Log("No CJK font found on this system — CJK characters may render as boxes")
		// Not a test failure — CI may not have CJK fonts
		return
	}

	t.Logf("CJK font: %s", path)

	// Verify the file exists
	if _, err := os.Stat(path); err != nil {
		t.Errorf("CJK font path does not exist: %s: %v", path, err)
	}
}

func TestFindMonoFont(t *testing.T) {
	path := FindMonoFont()
	if path == "" {
		t.Log("No mono font found")
		return
	}
	t.Logf("Mono font: %s", path)
}

func TestFontDirectories(t *testing.T) {
	dirs := fontDirectories()
	if len(dirs) == 0 {
		t.Error("no font directories returned")
	}
	t.Logf("Font directories (%s): %v", runtime.GOOS, dirs)
}

func TestIsCJKFont(t *testing.T) {
	tests := []struct {
		filename string
		want     bool
	}{
		{"pingfangsc-regular.ttf", true},
		{"notosanscjk-regular.otf", true},
		{"heiti sc.ttc", true},
		{"arial.ttf", false},
		{"courier.ttf", false},
		{"simsun.ttc", true},
		{"msyh.ttf", true},
		{"dejavusansmono.ttf", false},
		{"lxgwmonocjk.ttf", true},
		{"sarasamonocjk.ttf", true},
	}

	for _, tt := range tests {
		got := isCJKFont(tt.filename)
		if got != tt.want {
			t.Errorf("isCJKFont(%q) = %v, want %v", tt.filename, got, tt.want)
		}
	}
}

func TestIsMonoFont(t *testing.T) {
	tests := []struct {
		filename string
		want     bool
	}{
		{"dejavusansmono.ttf", true},
		{"menlo-regular.ttf", true},
		{"consolas.ttf", true},
		{"courier new.ttf", true},
		{"firacode-regular.ttf", true},
		{"jetbrainsmono.ttf", true},
		{"arial.ttf", false},
		{"pingfang.ttc", false},
		{"times.ttf", false},
	}

	for _, tt := range tests {
		got := isMonoFont(tt.filename)
		if got != tt.want {
			t.Errorf("isMonoFont(%q) = %v, want %v", tt.filename, got, tt.want)
		}
	}
}

func TestReadFontFile(t *testing.T) {
	// Empty path
	_, err := ReadFontFile("")
	if err == nil {
		t.Error("expected error for empty path")
	}

	// Non-existent file
	_, err = ReadFontFile("/nonexistent/font.ttf")
	if err == nil {
		t.Error("expected error for non-existent file")
	}

	// Try to read an actual font if available
	cjkPath := FindCJKFont()
	if cjkPath != "" {
		data, err := ReadFontFile(cjkPath)
		if err != nil {
			t.Errorf("failed to read CJK font %s: %v", cjkPath, err)
		}
		if len(data) == 0 {
			t.Error("font data is empty")
		}
		t.Logf("Read %d bytes from %s", len(data), filepath.Base(cjkPath))
	}
}

func TestSearchCJKFonts(t *testing.T) {
	fonts := searchCJKFonts()
	t.Logf("CJK fonts found: %d", len(fonts))
	for _, f := range fonts {
		t.Logf("  %s", f)
	}
}

func TestScoreFont(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		minScore int // minimum expected score
	}{
		{"CJK mono", "SarasaMonoCJK-Regular.ttf", 150},
		{"CJK only", "PingFang-SC-Regular.ttf", 100},
		{"Mono only", "JetBrainsMono-Regular.ttf", 50},
		{"Neither", "Arial.ttf", 0},
	}

	for _, tt := range tests {
		f := FontSearchResult{Name: tt.filename, IsCJK: isCJKFont(strings.ToLower(tt.filename))}
		score := scoreFont(f)
		if score < tt.minScore {
			t.Errorf("scoreFont(%q) = %d, want >= %d", tt.filename, score, tt.minScore)
		}
	}
}

func TestSortResults(t *testing.T) {
	results := []FontSearchResult{
		{Name: "arial.ttf"},
		{Name: "PingFang.ttc", IsCJK: true},
		{Name: "Mono.ttf"},
	}
	sortResults(results)

	// CJK font should come first
	if !results[0].IsCJK {
		t.Errorf("expected CJK font first, got %q", results[0].Name)
	}
}
