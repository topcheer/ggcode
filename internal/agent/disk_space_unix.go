//go:build !windows

package agent

import "syscall"

// diskUsageOS returns (free bytes, total bytes, ok) for the filesystem
// containing path, using the portable syscall.Statfs.
func diskUsageOS(path string) (free, total uint64, ok bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, false
	}
	// Bavail: free blocks available to unprivileged user (excludes reserved)
	free = uint64(stat.Bavail) * uint64(stat.Bsize)
	// Blocks * Bsize = total filesystem size
	total = uint64(stat.Blocks) * uint64(stat.Bsize)
	return free, total, true
}
