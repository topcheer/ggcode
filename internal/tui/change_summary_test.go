package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/diff"
)

func TestShortenPath(t *testing.T) {
	tests := []struct {
		name       string
		absPath    string
		workingDir string
		want       string
	}{
		{
			name:       "relative to working dir",
			absPath:    "/home/user/project/src/main.go",
			workingDir: "/home/user/project",
			want:       "src/main.go",
		},
		{
			name:       "outside working dir",
			absPath:    "/etc/passwd",
			workingDir: "/home/user/project",
			want:       "/etc/passwd",
		},
		{
			name:       "empty working dir",
			absPath:    "/some/path/file.go",
			workingDir: "",
			want:       "/some/path/file.go",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortenPath(tt.absPath, tt.workingDir)
			if got != tt.want {
				t.Errorf("shortenPath(%q, %q) = %q, want %q", tt.absPath, tt.workingDir, got, tt.want)
			}
		})
	}
}

func TestPluralFile(t *testing.T) {
	if pluralFile(1) != "file" {
		t.Errorf("pluralFile(1) = %q, want %q", pluralFile(1), "file")
	}
	if pluralFile(2) != "files" {
		t.Errorf("pluralFile(2) = %q, want %q", pluralFile(2), "files")
	}
}

func TestSortStrings(t *testing.T) {
	input := []string{"c", "a", "b", "d"}
	got := sortStrings(input)
	want := []string{"a", "b", "c", "d"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("sortStrings = %v, want %v", got, want)
			break
		}
	}
	// Verify original is unchanged
	if input[0] != "c" {
		t.Errorf("sortStrings modified the input slice")
	}
}

func TestDiffCountChanges(t *testing.T) {
	added, deleted := diff.CountChanges("a\nb\nc\n", "a\nx\nc\n")
	// 1 line changed = 1 addition + 1 deletion (line-level diff)
	if added == 0 && deleted == 0 {
		t.Errorf("CountChanges returned 0,0 for different content")
	}
}

// TestShortenPathBoundary pins #1361: prefix matches need real path
// boundaries - /Users/alicebox must not render as ~box (alice's home), and
// a working-dir file named ..hidden.go must not be misjudged as outside.
func TestShortenPathBoundary(t *testing.T) {
	t.Setenv("HOME", "/Users/alice")

	// Same-prefix sibling of home: must NOT shorten.
	if got := shortenPath("/Users/alicebox/x.go", ""); got != "/Users/alicebox/x.go" {
		t.Errorf("sibling dir shortened to nonexistent path: %q", got)
	}
	// Real home child: still shortens.
	if got := shortenPath("/Users/alice/proj/main.go", ""); got != "~/proj/main.go" {
		t.Errorf("home child not shortened: %q", got)
	}

	// Dot-prefixed file inside the working dir: relative, not absolute.
	if got := shortenPath("/work/repo/..hidden.go", "/work/repo"); got != "..hidden.go" {
		t.Errorf("dotfile inside working dir misjudged as outside: %q", got)
	}
	// A REAL parent escape still falls through to home/absolute display.
	if got := shortenPath("/work/other/x.go", "/work/repo"); got != "/work/other/x.go" {
		t.Errorf("outside path not kept absolute: %q", got)
	}
}
