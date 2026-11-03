//go:build windows

package runfile

import (
	"os"
	"testing"
	"time"
)

func TestIssue1290_ProcessExistsBasics(t *testing.T) {
	if !processExists(os.Getpid()) {
		t.Fatal("current process must be reported alive")
	}
	if processExists(0) || processExists(-1) {
		t.Fatal("invalid PIDs must be dead")
	}

	p, err := os.StartProcess(`C:\Windows\System32\cmd.exe`, []string{`/c`, `exit`}, &os.ProcAttr{})
	if err != nil {
		t.Skipf("cannot spawn cmd.exe: %v", err)
	}
	_, _ = p.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !processExists(p.Pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("exited PID %d still reported alive", p.Pid)
}
