//go:build !windows

package knight

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/topcheer/ggcode/internal/debug"
)

type instanceLock struct {
	file *os.File
	path string
}

func tryAcquireLock(projDir string) *instanceLock {
	lockDir := filepath.Join(projDir, ".ggcode")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		debug.Log("knight", "lock: cannot create dir %s: %v", lockDir, err)
		return nil
	}

	lockPath := filepath.Join(lockDir, "knight.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		debug.Log("knight", "lock: cannot open %s: %v", lockPath, err)
		return nil
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {

		pid := readLockPID(f)
		f.Close()
		debug.Log("knight", "lock: another instance already running (pid=%d)", pid)
		return nil
	}

	f.Truncate(0)
	f.Seek(0, 0)
	f.WriteString(strconv.Itoa(os.Getpid()))
	f.Sync()

	debug.Log("knight", "lock: acquired (path=%s)", lockPath)
	return &instanceLock{file: f, path: lockPath}
}

func (l *instanceLock) release() {
	if l.file != nil {
		syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
		l.file.Close()
		debug.Log("knight", "lock: released (path=%s)", l.path)
	}
}

func readLockPID(f *os.File) int {
	data, err := os.ReadFile(f.Name())
	if err != nil {
		return 0
	}
	return parseLockFileData(data)
}

func LockHeldBy(projDir string) (int, error) {
	lockPath := filepath.Join(projDir, ".ggcode", "knight.lock")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return 0, nil
	}
	pid := parseLockFileData(data)
	if pid <= 0 {
		return 0, nil
	}

	f, err := os.OpenFile(lockPath, os.O_RDWR, 0600)
	if err != nil {

		return 0, fmt.Errorf("cannot verify knight lock %s: %w", lockPath, err)
	}
	defer f.Close()

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {

		return pid, nil
	}

	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return 0, nil
}

func FormatLockMessage(pid int) string {
	if pid > 0 {
		return fmt.Sprintf("knight: skipped — another instance (PID %d) already running in this workspace", pid)
	}
	return "knight: skipped — another instance already running in this workspace"
}
