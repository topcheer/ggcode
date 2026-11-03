package util

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func AtomicWriteFile(path string, data []byte, defaultMode os.FileMode) error {
	writePath := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
		writePath = resolved
	}

	mode := defaultMode
	if info, err := os.Stat(writePath); err == nil {
		mode = info.Mode().Perm()
	}

	dir := filepath.Dir(writePath)
	tmp, err := os.CreateTemp(dir, ".ggcode-tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("syncing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, writePath); err != nil {
		cleanup()
		return fmt.Errorf("renaming temp file into place: %w", err)
	}
	return nil
}

func SweepStaleTempFiles(dir string, maxAge time.Duration) (int, error) {
	matches, err := filepath.Glob(filepath.Join(dir, ".ggcode-tmp-*"))
	if err != nil {
		return 0, fmt.Errorf("globbing temp files in %s: %w", dir, err)
	}
	if len(matches) == 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, m := range matches {
		info, statErr := os.Stat(m)
		if statErr != nil {
			continue
		}
		if info.IsDir() || !info.ModTime().Before(cutoff) {
			continue
		}
		if rmErr := os.Remove(m); rmErr != nil {
			if debugLogFn != nil {
				debugLogFn("util", "sweep: removing stale temp %s: %v", m, rmErr)
			}
			continue
		}
		removed++
		if debugLogFn != nil {
			debugLogFn("util", "swept stale atomic-write temp: %s", m)
		}
	}
	return removed, nil
}
