//go:build windows

package daemon

import (
	"fmt"
	"os"
	"syscall"

	"github.com/topcheer/ggcode/internal/util"
	"golang.org/x/sys/windows"
)

const (
	lockFileExclusive       = 0x00000002
	lockFileFailImmediately = 0x00000001
)

func flockNonBlocking(f *os.File) error {
	const sentinelOffset = 1 << 40
	var ol windows.Overlapped
	ol.OffsetHigh = uint32(sentinelOffset >> 32)
	ol.Offset = uint32(sentinelOffset & 0xFFFFFFFF)
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		lockFileExclusive|lockFileFailImmediately,
		0,
		1,
		0,
		&ol,
	)
}

func openPIDFile(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(
		p,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

func newBackgroundSysProcAttr() *syscall.SysProcAttr {

	return &syscall.SysProcAttr{
		CreationFlags: 0x08000000 | syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}

func checkProcessAlive(proc *os.Process) error {
	if proc == nil || proc.Pid <= 0 {
		return fmt.Errorf("invalid process")
	}

	if !util.IsProcessAlive(proc.Pid) {
		return fmt.Errorf("process %d is not running", proc.Pid)
	}
	return nil
}
