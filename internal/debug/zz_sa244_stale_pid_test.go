package debug

// #3339 (sa-244 runtime audit): the stale-PID log sweep had zero test
// coverage despite a real regression history (v1.3.167: the sweep ran
// before defaultLogDir was set and silently did nothing). These tests pin
// the two behaviors users can feel:
//   - dead-PID files (including rotation suffixes) are removed
//   - PID-reuse collision survivors are removed by the 14d age backstop
//     (liveness alone kept them forever - ~15 old-PID file sets observed)
//
// Init()'s call order itself is not tested here: Init is sync.Once-guarded
// and cannot be re-entered within one process.

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
		{"ggcode-agent-a1b2.log", 0},  // non-numeric pid
		{"other-agent-12345.log", 0},  // wrong prefix
		{"ggcode-agent.log", 0},       // no pid segment
		{"ggcode-agent-12345", 12345}, // suffix-less also parses (implementation accepts; harmless)
		{"notes.txt", 0},              // unrelated
	}
	for _, c := range cases {
		if got := extractPidFromLogName(c.name); got != c.want {
			t.Errorf("extractPidFromLogName(%q) = %d, want %d", c.name, got, c.want)
		}
	}
}

// withSweepDir points defaultLogDir at a temp dir for the duration of a test.
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
	// 1<<22 exceeds Linux pid_max (4194304) and macOS/Windows ranges, so the
	// PID is guaranteed dead without spawning a child process.
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
	// A PID that is alive (our own test process serves as a guaranteed-live
	// PID distinct from the selfPid skip only if we use a different process;
	// simplest reliable live non-self PID: reuse os.Getpid()+1 is racy. Use
	// the test binary's own PID via a *file that pretends its PID is our
	// parent? Instead: launch a short-lived child and use its PID.
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Skip("cannot spawn child process: ", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	live := cmd.Process.Pid

	// Fresh file with a live PID: must survive (recent, alive).
	fresh := filepath.Join(dir, "ggcode-agent-"+itoa(live)+".log")
	if err := os.WriteFile(fresh, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Aged ROTATION sibling of the live PID: same PID parses out, PID is
	// alive, but the file is ancient - exactly the #3339 collision-survivor
	// shape (a recycled PID keeping dead-ggcode files alive forever).
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
