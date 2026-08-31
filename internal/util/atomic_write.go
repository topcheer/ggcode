package util

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteFile writes data to path via a temp file in the same directory,
// fsync's it, and renames it into place. If path already exists, the existing
// file's mode is preserved; otherwise defaultMode is used.
//
// This avoids two failure modes of os.WriteFile(path, data, mode):
//  1. A crash between O_TRUNC and the final write leaves the user's source
//     file truncated/empty.
//  2. The hard-coded mode silently downgrades 0755 scripts and 0600 secrets
//     to 0644.
//
// #1359: if path is a symlink, it is resolved to the final real target and
// THAT is written. os.Rename onto a symlink path replaces the link itself
// with a regular file - the real target never updates and the repo is left
// with a forked copy where the link used to be, while every read tool
// (ReadFile follows links) showed the agent the target's content.
//
// See locks.md S4.
func AtomicWriteFile(path string, data []byte, defaultMode os.FileMode) error {
	writePath := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
		writePath = resolved
	}
	// EvalSymlinks error (e.g. dangling link whose target does not exist):
	// fall through and write the link path itself - the rename replaces the
	// dangling link, which is the closest useful behavior (reads of it were
	// failing anyway).

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
