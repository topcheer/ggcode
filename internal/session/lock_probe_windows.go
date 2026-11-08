//go:build windows

package session

import "os"

// ProbeLockHolderPID Windows 桩:会话锁探测(unix 版用 flock;windows 版
// webui 锁定态标识暂不区分,返回 0=未锁定)。真正的锁竞争拒绝仍由
// TryAcquireSessionLock 的 windows 实现在切换时兜底。
func ProbeLockHolderPID(storeDir, sessionID string) int {
	return 0
}

var _ = os.OpenFile // keep parity with unix imports if extended later
