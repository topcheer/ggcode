//go:build !unix && !windows

package util

func FileLock(lockPath string) (func(), error) {
	return func() {}, nil
}
