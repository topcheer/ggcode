//go:build !windows

package agent

import "syscall"

func diskUsageOS(path string) (free, total uint64, ok bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, false
	}

	free = uint64(stat.Bavail) * uint64(stat.Bsize)

	total = uint64(stat.Blocks) * uint64(stat.Bsize)
	return free, total, true
}
