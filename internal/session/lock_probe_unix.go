//go:build !windows

package session

import (
	"os"
	"syscall"
)

// ProbeLockHolderPID 非侵入探测会话锁持有者:non-blocking flock 试锁,成功=无锁
// (立即释放返回 0);失败=他实例持有,读锁文件 PID 返回。
// webui /api/sessions 锁定态标识用——只读语义,不改变任何锁状态。
func ProbeLockHolderPID(storeDir, sessionID string) int {
	lockPath := LockFilePath(storeDir, sessionID)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return 0
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return readLockPIDFromFile(f)
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN) // 试锁成功即释放
	return 0
}
