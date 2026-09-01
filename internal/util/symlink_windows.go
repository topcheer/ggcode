package util

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SafeSymlink creates a symbolic link from oldname to newname.
// On Windows, symlinks require Developer Mode or elevated privileges,
// so this falls back to:
//   - directory junction (mklink /J) for directories
//   - file copy for regular files
func SafeSymlink(oldname, newname string) error {
	err := os.Symlink(oldname, newname)
	if err == nil {
		return nil
	}
	// #1405-B: EEXIST (target already there - a leftover compat file from
	// a previous run) must PROPAGATE, not fall into the copy fallback:
	// os.Create in copyFile truncates the existing file - silent data
	// loss. Only privilege-related failures (EPERM - symlinks need
	// SeCreateSymbolicLinkPrivilege) take the junction/copy fallback.
	// Matches the Unix version, which propagates EEXIST. ENOENT is a real
	// error too - the source is missing and copying would only report a
	// misleading failure later.
	if errors.Is(err, os.ErrExist) || errors.Is(err, fs.ErrNotExist) {
		return err
	}

	// Check if source is a directory
	info, err := os.Stat(oldname)
	if err != nil {
		return fmt.Errorf("symlink fallback: stat %s: %w", oldname, err)
	}

	if info.IsDir() {
		// Try directory junction via mklink /J
		return junctionDir(oldname, newname)
	}

	// Fallback: copy the file
	return copyFile(oldname, newname)
}

// junctionDir creates a Windows directory junction (similar to symlink for dirs).
// #923: junctions need NO privilege (unlike symlinks) - this previously
// fell back to recursively COPYING the tree (no link semantics: source
// updates never propagated; deleting the copy left the source untouched).
func junctionDir(oldname, newname string) error {
	absOld, err := filepath.Abs(oldname)
	if err != nil {
		return fmt.Errorf("junction: abs path: %w", err)
	}
	absNew, err := filepath.Abs(newname)
	if err != nil {
		return fmt.Errorf("junction: abs path: %w", err)
	}

	// Refuse to destroy an existing NON-EMPTY target (the old code's
	// silent os.Remove could delete real files).
	if entries, err := os.ReadDir(absNew); err == nil && len(entries) > 0 {
		return fmt.Errorf("junction: target %s not empty", absNew)
	}
	_ = os.Remove(absNew)

	return cmdJunction(absOld, absNew)
}

// cmdJunction creates a real junction via cmd /c mklink /J (#923).
func cmdJunction(target, link string) error {
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mklink /J %s %s: %v: %s", link, target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode())
}
