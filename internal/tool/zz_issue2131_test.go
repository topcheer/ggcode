package tool

// #2131 regression: the #1687 dirLike shape checks (".", "..", trailing
// slash, glob chars) missed BARE directory names - "git add -- config"
// stages everything under config/, but files=["config"] matched none of
// the four shapes and silently bypassed the sensitive-file index scan.
// Bare names are now stat-resolved (CWD and repo dir).

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitAddArgsDirLike(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		dir   string
		files []string
		want  bool
	}{
		{"dot", repo, []string{"."}, true},
		{"dotdot", repo, []string{".."}, true},
		{"trailing slash", repo, []string{"config/"}, true},
		{"glob", repo, []string{"*.go"}, true},
		// #2131: the bare directory name resolved against the repo dir.
		{"bare dir via repo dir", repo, []string{"config"}, true},
		{"plain file not dir-like", repo, []string{"main.go"}, false},
		{"missing path not dir-like", repo, []string{"no-such-thing"}, false},
		{"empty list", repo, nil, false},
	}
	for _, c := range cases {
		if got := gitAddArgsDirLike(c.dir, c.files); got != c.want {
			t.Errorf("%s: gitAddArgsDirLike(%q, %v) = %v, want %v", c.name, c.dir, c.files, got, c.want)
		}
	}
}
