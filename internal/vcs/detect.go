package vcs

import (
	"os"
	"path/filepath"
)

func Detect(workingDir string) VCS {
	dir := workingDir
	for {

		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil {

			_ = info
			return Git{}
		}

		if _, err := os.Stat(filepath.Join(dir, ".hg")); err == nil {
			return Mercurial{}
		}

		if _, err := os.Stat(filepath.Join(dir, ".svn")); err == nil {
			return Subversion{}
		}

		if _, err := os.Stat(filepath.Join(dir, ".jj")); err == nil {

			return Jujutsu{}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil
}

func DetectOrGit(workingDir string) VCS {
	if v := Detect(workingDir); v != nil {
		return v
	}
	return Git{}
}
