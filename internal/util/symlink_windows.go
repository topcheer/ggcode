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

func SafeSymlink(oldname, newname string) error {
	err := os.Symlink(oldname, newname)
	if err == nil {
		return nil
	}

	if errors.Is(err, os.ErrExist) || errors.Is(err, fs.ErrNotExist) {
		return err
	}

	info, err := os.Stat(oldname)
	if err != nil {
		return fmt.Errorf("symlink fallback: stat %s: %w", oldname, err)
	}

	if info.IsDir() {

		return junctionDir(oldname, newname)
	}

	return copyFile(oldname, newname)
}

func junctionDir(oldname, newname string) error {
	absOld, err := filepath.Abs(oldname)
	if err != nil {
		return fmt.Errorf("junction: abs path: %w", err)
	}
	absNew, err := filepath.Abs(newname)
	if err != nil {
		return fmt.Errorf("junction: abs path: %w", err)
	}

	if entries, err := os.ReadDir(absNew); err == nil && len(entries) > 0 {
		return fmt.Errorf("junction: target %s not empty", absNew)
	}
	_ = os.Remove(absNew)

	return cmdJunction(absOld, absNew)
}

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

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}

	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode())
}
