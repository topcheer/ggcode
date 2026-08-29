//go:build windows

package runfile

// #1290: processExists must treat ERROR_ACCESS_DENIED as "alive" (same
// semantics as the Unix #799 EPERM fix) - runfile.go deletes port files of
// processes believed dead, and elevated/cross-user processes deny
// PROCESS_QUERY_LIMITED_INFORMATION while being perfectly alive.
//
// Runs only on Windows CI; on darwin/linux the build tag excludes this file
// and GOOS=windows go vet verifies compilation.

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
	// A PID that exited must be detected dead (the ACCESS_DENIED path itself
	// needs an elevated/protected victim and cannot be simulated here; the
	// self/exit paths guard the surrounding logic).
	p, err := os.StartProcess(`C:\Windows\System32\cmd.exe`, []string{`/c`, `exit`}, &os.ProcAttr{})
	if err != nil {
		t.Skipf("cannot spawn cmd.exe: %v", err)
	}
	_, _ = p.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !processExists(p.Pid) {
			return // dead correctly detected
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("exited PID %d still reported alive", p.Pid)
}
