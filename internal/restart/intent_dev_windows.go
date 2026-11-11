//go:build windows

package restart

import (
	"os"

	"golang.org/x/sys/windows"
)

// statDevInode reports the Windows file identity from an open handle:
// the volume serial number as dev and the file index as inode. The file
// index is 128-bit on ReFS; folding it to 64 bits is fine for same-binary
// comparisons (same file always folds identically). When the filesystem
// does not support file IDs, ok=false and identity comparison falls back
// to path/revision matching in StartupVerifyNotice.
func statDevInode(path string) (dev, inode uint64, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	h := windows.Handle(f.Fd())
	var data windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &data); err != nil {
		return 0, 0, false
	}
	return uint64(data.VolumeSerialNumber),
		uint64(data.FileIndexHigh)<<32 | uint64(data.FileIndexLow), true
}
