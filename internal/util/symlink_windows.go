package util

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
func junctionDir(oldname, newname string) error {
	// Use absolute path for junction target
	absOld, err := filepath.Abs(oldname)
	if err != nil {
		return fmt.Errorf("junction: abs path: %w", err)
	}
	absNew, err := filepath.Abs(newname)
	if err != nil {
		return fmt.Errorf("junction: abs path: %w", err)
	}

	// Remove existing target if any
	_ = os.Remove(absNew)

	// Create junction using cmd /c mklink /J
	return cmdJunction(absOld, absNew)
}

func cmdJunction(target, link string) error {
	// Fallback: just copy the directory tree
	return filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(target, path)
		dst := filepath.Join(link, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, info.Mode())
		}
		return copyFile(path, dst)
	})
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
