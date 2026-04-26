package util

import (
	"testing"
)

func TestRelativizePaths(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		baseDir string
		want    string
	}{
		{
			name:    "empty text",
			text:    "",
			baseDir: "/Users/proj",
			want:    "",
		},
		{
			name:    "empty baseDir",
			text:    "/Users/proj/main.go",
			baseDir: "",
			want:    "/Users/proj/main.go",
		},
		{
			name:    "subpath gets relativized",
			text:    "reading /Users/proj/src/main.go",
			baseDir: "/Users/proj",
			want:    "reading ./src/main.go",
		},
		{
			name:    "exact workdir becomes dot",
			text:    "working in /Users/proj",
			baseDir: "/Users/proj",
			want:    "working in .",
		},
		{
			name:    "similar prefix NOT replaced (no false match)",
			text:    "reading /Users/proj-backup/main.go",
			baseDir: "/Users/proj",
			want:    "reading /Users/proj-backup/main.go",
		},
		{
			name:    "multiple occurrences",
			text:    "/Users/proj/a.go and /Users/proj/b.go",
			baseDir: "/Users/proj",
			want:    "./a.go and ./b.go",
		},
		{
			name:    "mix of exact and subpath",
			text:    "root=/Users/proj file=/Users/proj/src/main.go backup=/Users/proj-old/data",
			baseDir: "/Users/proj",
			want:    "root=. file=./src/main.go backup=/Users/proj-old/data",
		},
		{
			name:    "unrelated path unchanged",
			text:    "/etc/hosts and /tmp/file",
			baseDir: "/Users/proj",
			want:    "/etc/hosts and /tmp/file",
		},
		{
			name:    "trailing slash in baseDir is cleaned",
			text:    "file at /Users/proj/foo.go",
			baseDir: "/Users/proj/",
			want:    "file at ./foo.go",
		},
		{
			name:    "path with dots in dir name",
			text:    "/Users/my.project/src/main.go",
			baseDir: "/Users/my.project",
			want:    "./src/main.go",
		},
		{
			name:    "no false match on dir with underscore suffix",
			text:    "/Users/proj_test/data",
			baseDir: "/Users/proj",
			want:    "/Users/proj_test/data",
		},
		{
			name:    "no false match on dir with digit suffix",
			text:    "/Users/proj2/data",
			baseDir: "/Users/proj",
			want:    "/Users/proj2/data",
		},
		{
			name:    "baseDir at end of text",
			text:    "cd /Users/proj",
			baseDir: "/Users/proj",
			want:    "cd .",
		},
		{
			name:    "baseDir at start of text exact",
			text:    "/Users/proj",
			baseDir: "/Users/proj",
			want:    ".",
		},
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
