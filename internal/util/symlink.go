//go:build !windows

package util

import "os"

func SafeSymlink(oldname, newname string) error {
	return os.Symlink(oldname, newname)
}
