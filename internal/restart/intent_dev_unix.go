//go:build unix

package restart

import (
	"os"
	"syscall"
)

// statDevInode reports the unix file identity (device, inode) for path.
// ok=false when the file cannot be stat'ed or the platform does not expose
// syscall.Stat_t.
func statDevInode(path string) (dev, inode uint64, ok bool) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, 0, false
	}
	if sys, isStat := st.Sys().(*syscall.Stat_t); isStat {
		return uint64(sys.Dev), uint64(sys.Ino), true
	}
	return 0, 0, false
}
