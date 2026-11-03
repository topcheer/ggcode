//go:build !unix && !windows

package util

func isLockBusy(err error) bool {
	return false
}
