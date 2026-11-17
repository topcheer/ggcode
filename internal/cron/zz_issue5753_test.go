package cron

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/restart"
)

// zz_issue5753_test.go pins the nudge-time half of the restart handshake:
// when the durable resume nudge fires, its prompt is prefixed with the arm
// outcome notice — a loud ARMED-BUT-NOT-EXECUTED alarm when the firing
// process still runs the old image, a verification line when the swap
// landed, an explicit unverified note for unstamped builds. Never again a
// bare success-shaped nudge after a silently failed exec.

func TestIssue5753NudgeCarriesArmOutcome(t *testing.T) {
	t.Setenv("GGCODE_RESTART_ARM_DIR", t.TempDir())
	store := filepath.Join(t.TempDir(), "cron-jobs.json")

	var mu sync.Mutex
	var prompts []string
	s := NewScheduler(func(prompt string, queueIfBusy bool) {
		mu.Lock()
		prompts = append(prompts, prompt)
		mu.Unlock()
	}, store)

	// Arm record targeting a revision this process is NOT running (or, on
	// unstamped builds, no revision to compare): the notice must be
	// non-empty either way — alarm or explicit-unverified, never silence.
	rec, err := restart.WriteArmRecord(filepath.Join(t.TempDir(), "ggcode"), "nudge handshake test")
	if err != nil {
		t.Fatal(err)
	}
	rec.Revision = "deadbeefcafe" // != any real running revision
	// Rewrite the record with the mismatching revision in place.
	recBytes, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restart.ArmRecordPath(rec.PID), recBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreateOneShotWithID(RestartResumeNudgeID, time.Now().Add(5*time.Millisecond), "continue the previous task", true); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(prompts)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	if len(prompts) == 0 {
		mu.Unlock()
		t.Fatal("resume nudge never fired")
	}
	got = prompts[0]
	mu.Unlock()

	if got == "continue the previous task" {
		t.Fatal("nudge fired WITHOUT the outcome prefix — the #5753 silent-success shape")
	}
	if !strings.Contains(got, "continue the previous task") {
		t.Errorf("nudge must keep the original prompt as its body: %q", got)
	}
	if !strings.HasPrefix(got, "restart-") {
		t.Errorf("nudge must be prefixed by the arm outcome notice: %q", got)
	}
	if !strings.Contains(got, "deadbeefcafe") {
		t.Errorf("alarm/unverified notice must name the armed target revision: %q", got)
	}
}
