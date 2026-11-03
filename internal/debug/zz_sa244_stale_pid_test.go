package debug

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractPidFromLogName(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{"ggcode-agent-12345.log", 12345},
		{"ggcode-agent-12345.log.1", 12345},
		{"ggcode-agent-12345.log.10", 12345},
		{"ggcode-openai-99999.log.2", 99999},
		{"ggcode-agent-a1b2.log", 0},
		{"other-agent-12345.log", 0},
		{"ggcode-agent.log", 0},
		{"ggcode-agent-12345", 12345},
		{"notes.txt", 0},
	}
	for _, c := range cases {
		if got := extractPidFromLogName(c.name); got != c.want {
			t.Errorf("extractPidFromLogName(%q) = %d, want %d", c.name, got, c.want)
		}
	}
}

func withSweepDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := defaultLogDir
	defaultLogDir = dir
	t.Cleanup(func() { defaultLogDir = old })
	return dir
}

func TestCleanupStaleLogsRemovesDeadPidFiles(t *testing.T) {
	dir := withSweepDir(t)

	dead := 1 << 22
	main := filepath.Join(dir, "ggcode-agent-"+itoa(dead)+".log")
	rot := main + ".7"
	for _, p := range []string{main, rot} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cleanupStaleLogs()

	for _, p := range []string{main, rot} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("dead-PID file %s must be removed, stat err=%v", p, err)
		}
	}
}

func TestCleanupStaleLogsKeepsSelfFiles(t *testing.T) {
	dir := withSweepDir(t)
	self := filepath.Join(dir, "ggcode-agent-"+itoa(os.Getpid())+".log")
	if err := os.WriteFile(self, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cleanupStaleLogs()

	if _, err := os.Stat(self); err != nil {
		t.Fatalf("own log must survive the sweep: %v", err)
	}
}

func TestCleanupStaleLogsAgeCapsPidCollisionSurvivors(t *testing.T) {
	dir := withSweepDir(t)

	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Skip("cannot spawn child process: ", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	live := cmd.Process.Pid

	fresh := filepath.Join(dir, "ggcode-agent-"+itoa(live)+".log")
	if err := os.WriteFile(fresh, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	aged := fresh + ".3"
	if err := os.WriteFile(aged, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-15 * 24 * time.Hour)
	if err := os.Chtimes(aged, old, old); err != nil {
		t.Fatal(err)
	}

	cleanupStaleLogs()

	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh live-PID file must survive: %v", err)
	}
	if _, err := os.Stat(aged); !os.IsNotExist(err) {
		t.Errorf("aged collision-survivor file must be removed by age backstop, stat err=%v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
