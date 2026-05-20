//go:build !windows

package util

import "os"

// SafeSymlink creates a symbolic link from oldname to newname.
// On non-Windows platforms, this is a direct wrapper around os.Symlink.
func SafeSymlink(oldname, newname string) error {
	return os.Symlink(oldname, newname)
}
