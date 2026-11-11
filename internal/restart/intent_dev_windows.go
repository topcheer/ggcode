//go:build windows

package restart

import "os"

// devInode: Windows file IDs are not exposed through syscall.Stat_t in a
// portable form; both stay zero and the revision comparison carries the
// identity check instead.
func devInode(st os.FileInfo) (dev, inode uint64) {
	return 0, 0
}
