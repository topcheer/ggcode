//go:build unix

package restart

import (
	"os"
	"syscall"
)

func devInode(st os.FileInfo) (dev, inode uint64) {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return uint64(sys.Dev), uint64(sys.Ino)
	}
	return 0, 0
}
