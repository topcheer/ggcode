//go:build windows

package knight

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/topcheer/ggcode/internal/debug"
	"golang.org/x/sys/windows"
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

	handle := windows.Handle(f.Fd())
	overlapped := windows.Overlapped{}
	err = windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
	if err != nil {
		pid := readLockPID(f)
		f.Close()
		debug.Log("knight", "lock: another instance already running (pid=%d)", pid)
		return nil
	}

	f.Truncate(0)
	f.Seek(lockPIDOffset, 0)
	f.WriteString(strconv.Itoa(os.Getpid()))
	f.Sync()

	debug.Log("knight", "lock: acquired (path=%s)", lockPath)
	return &instanceLock{file: f, path: lockPath}
}

func (l *instanceLock) release() {
	if l.file != nil {
		handle := windows.Handle(l.file.Fd())
		overlapped := windows.Overlapped{}
		windows.UnlockFileEx(handle, 0, 1, 0, &overlapped)
		l.file.Close()
		debug.Log("knight", "lock: released (path=%s)", l.path)
	}
}

func readLockPID(f *os.File) int {
	if f == nil {
		return 0
	}
	if pid := readPIDAt(f, lockPIDOffset); pid > 0 {
		return pid
	}
	return readPIDAt(f, 0)
}

func LockHeldBy(projDir string) (int, error) {
	lockPath := filepath.Join(projDir, ".ggcode", "knight.lock")
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0600)
	if err != nil {

		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()

	pid := readLockPID(f)
	if pid <= 0 {
		return 0, nil
	}

	handle := windows.Handle(f.Fd())
	overlapped := windows.Overlapped{}
	err = windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
	if err != nil {

		return pid, nil
	}

	windows.UnlockFileEx(handle, 0, 1, 0, &overlapped)
	return 0, nil
}

func FormatLockMessage(pid int) string {
	if pid > 0 {
		return fmt.Sprintf("knight: skipped — another instance (PID %d) already running in this workspace", pid)
	}
	return "knight: skipped — another instance already running in this workspace"
}
