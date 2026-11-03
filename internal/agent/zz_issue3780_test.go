package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue3780_AbsoluteGlobProbe(t *testing.T) {
	e := newWFEngineWithSpec(t, `{"steps":[{"id":"gen","artifact_glob":"**/cov.out"}]}`)
	dir := filepath.Dir(e.loadDir)
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	e.startedAt = time.Now().Add(-2 * time.Second)
	fresh := filepath.Join(out, "cov.out")
	if err := os.WriteFile(fresh, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fresh, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}

	if !e.globFreshOnDisk("**/cov.out") {
		t.Error("relative ** glob must still probe fresh artifact")
	}

	if !e.globFreshOnDisk(filepath.ToSlash(fresh)) {
		t.Error("absolute pattern must match the absolute walked path")
	}
	absSuper := filepath.ToSlash(filepath.Join(dir, "**", "cov.out"))
	if !e.globFreshOnDisk(absSuper) {
		t.Error("absolute superglob pattern must probe fresh artifact")
	}

	if e.globFreshOnDisk(filepath.ToSlash(filepath.Join(dir, "nope.out"))) {
		t.Error("non-existent absolute path must not ground")
	}
}

func TestIssue3780_RecentAttemptsReturnsNewest(t *testing.T) {
	e := &workflowEngine{}
	base := time.Now()
	e.traceMu.Lock()
	for i := 1; i <= 5; i++ {
		e.trace = append(e.trace, StepAttempt{
			StepID:  "s",
			Command: "cmd-" + string(rune('0'+i)),
			At:      base.Add(time.Duration(i) * time.Second),
		})

		e.trace = append(e.trace, StepAttempt{StepID: "other", Command: "x", At: base})
	}
	e.traceMu.Unlock()
	att := e.recentAttempts("s", 3)
	if len(att) != 3 {
		t.Fatalf("want 3 attempts, got %d", len(att))
	}

	for i := 1; i < len(att); i++ {
		if att[i].At.Before(att[i-1].At) {
			t.Fatal("recentAttempts must return oldest-first")
		}
	}

	if want := "cmd-5"; att[len(att)-1].Command != want {
		t.Errorf("LastAttempt candidate = %q, want %q", att[len(att)-1].Command, want)
	}
	if att[0].Command != "cmd-3" {
		t.Errorf("window start = %q, want cmd-3 (newest-3 window)", att[0].Command)
	}
}

const spec3780overlap = `{
  "steps": [
    {"id":"all-tests","artifact_glob":"","on_commands":["go test*"]},
    {"id":"build-tests","artifact_glob":"","requires":[],"mode":"warn","on_commands":["go test -run Build*"]}
  ]
}`

func TestIssue3780_OverlappingOnCommandsMultiAttribute(t *testing.T) {
	e := newWFEngineWithSpec(t, spec3780overlap)
	args := json.RawMessage(`{"command":"go test -run Build ./..."}`)
	e.recordAttempt("run_command", args, tool.Result{Content: "ok"})

	for _, id := range []string{"all-tests", "build-tests"} {
		att := e.recentAttempts(id, 5)
		if len(att) != 1 {
			t.Errorf("step %s: want 1 attempt, got %d", id, len(att))
			continue
		}
		if att[0].Command != "go test -run Build ./..." {
			t.Errorf("step %s: command = %q", id, att[0].Command)
		}
	}

	e.recordAttempt("run_command", json.RawMessage(`{"command":"ls"}`), tool.Result{Content: ""})
	if got := len(e.recentAttempts("all-tests", 5)); got != 1 {
		t.Errorf("unrelated command must not add attempts, got %d", got)
	}
}
