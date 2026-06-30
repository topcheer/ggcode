package util

import "testing"

func TestRelativizePaths(t *testing.T) {
	// RelativizePaths is now a pass-through — text is returned as-is.
	tests := []struct {
		name    string
		text    string
		baseDir string
		want    string
	}{
		{"empty text", "", "/Users/proj", ""},
		{"empty baseDir", "/Users/proj/main.go", "", "/Users/proj/main.go"},
		{"subpath unchanged", "reading /Users/proj/src/main.go", "/Users/proj", "reading /Users/proj/src/main.go"},
		{"exact workdir unchanged", "working in /Users/proj", "/Users/proj", "working in /Users/proj"},
		{"nil baseDir", "/Users/proj/main.go", "/Users/proj", "/Users/proj/main.go"},
		{"unrelated path unchanged", "/etc/hosts and /tmp/file", "/Users/proj", "/etc/hosts and /tmp/file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RelativizePaths(tt.text, tt.baseDir)
			if got != tt.want {
				t.Errorf("RelativizePaths(%q, %q)\ngot:  %q\nwant: %q", tt.text, tt.baseDir, got, tt.want)
			}
		})
	}
}
