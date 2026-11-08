package session

import (
	"os"
	"path/filepath"
	"testing"
)

// 钉测(锁探测,webui locked_by_pid 数据源):
// 1) 无锁会话 → 试锁成功即释放,返回 0
// 2) 他实例持锁(flock 已被占) → 返回锁文件中的持有者 PID
// 3) storeDir 不存在 → 0(探测失败不报错)
func TestProbeLockHolderPID(t *testing.T) {
	dir := t.TempDir()

	if got := ProbeLockHolderPID(dir, "sess-free"); got != 0 {
		t.Fatalf("unlocked session: want 0, got %d", got)
	}

	// 模拟他实例持锁:TryAcquireSessionLock 成功后持有不放
	holder, err := TryAcquireSessionLock(dir, "sess-held")
	if err != nil {
		t.Fatal(err)
	}
	if !holder.Acquired() {
		t.Fatal("setup: holder lock not acquired")
	}
	t.Cleanup(func() { holder.Release() })

	if got := ProbeLockHolderPID(dir, "sess-held"); got != os.Getpid() {
		t.Fatalf("held session: want holder pid %d, got %d", os.Getpid(), got)
	}

	if got := ProbeLockHolderPID(filepath.Join(dir, "nonexistent"), "x"); got != 0 {
		t.Fatalf("missing storeDir: want 0, got %d", got)
	}
}
