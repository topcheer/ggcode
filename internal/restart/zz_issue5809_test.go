package restart

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// zz_issue5809_test.go pins the armed-but-not-executed false-pass fix:
// exec verification must use RUNTIME evidence (the #5367 startup intent
// banner, a memstats numGC reset, or a running revision that differs from
// the revision the arming process was EXECUTING at arm time) and never the
// arm-time DISK revision; an armed record surviving past the exec deadline
// must alarm regardless of which clear-path was expected to consume it.
//
// The battery deliberately injects records as raw JSON: keys unknown to the
// pre-#5809 ArmRecord unmarshal are silently ignored there, so this file
// compiles and runs at the pre-fix ref, where every behavioral pin below
// turns red.

// issue5809WriteRecord injects an arm record for pid as raw JSON.
func issue5809WriteRecord(t *testing.T, pid int, fields map[string]any) {
	t.Helper()
	fields["pid"] = pid
	if _, ok := fields["armed_at"]; !ok {
		fields["armed_at"] = time.Now().Add(-15 * time.Second).Format(time.RFC3339Nano)
	}
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ArmRecordDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ArmRecordPath(pid), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIssue5809NudgeDiskRevisionMatchMustNotVerify(t *testing.T) {
	issue5753IsolatedDir(t)
	// The incident shape: the arming process still runs revision R, and the
	// arm-time DISK snapshot also said R (apply-at-exec update flow stamps
	// the record before the new bytes land; or the disk was synced after
	// the arm). The pre-#5809 check compared running R == disk-at-arm R,
	// called it verified, and CLEARED the record — silencing the alarm
	// while the old image kept serving.
	//
	// The revisions are SYNTHETIC on purpose: the verdict must not depend
	// on the verifying process being vcs-stamped (go test binaries never
	// are — zero vcs.* build settings — so a SelfRevision-guarded variant
	// would skip on every runner, including CI). With no runtime evidence
	// of a swap (same image since the arm, numGC monotonic), the only
	// honest outcome is the loud alarm.
	issue5809WriteRecord(t, os.Getpid(), map[string]any{
		"target_path":      "/opt/ggcode/bin/ggcode",
		"revision":         "411da6abf9e0",
		"running_revision": "411da6abf9e0",
		"armed_num_gc":     0,
		"sha256":           "abcdef0123456789abcdef",
	})
	notice, verified := ArmOutcomeNotice(time.Now())
	if verified {
		t.Fatalf("disk-revision match must NOT verify the exec (the running image is unchanged since the arm): %q", notice)
	}
	for _, want := range []string{"ARMED-BUT-NOT-EXECUTED", "numGC", "memstats"} {
		if !strings.Contains(notice, want) {
			t.Errorf("alarm missing %q: %s", want, notice)
		}
	}
	if _, ok := LoadArmRecord(); !ok {
		t.Error("unverified record must NOT be cleared at nudge time")
	}
}

func TestIssue5809NudgeNumGCResetVerifies(t *testing.T) {
	issue5753IsolatedDir(t)
	// A runtime reset is exec proof even with zero revision evidence
	// (unstamped builds, same-image restarts): NumGC is monotonic within
	// a process and resets on execve, so a live count below the armed
	// count means this pid's runtime restarted since the arm.
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	issue5809WriteRecord(t, os.Getpid(), map[string]any{
		"target_path":  "/opt/ggcode/bin/ggcode",
		"revision":     "",
		"armed_num_gc": ms.NumGC + 500,
		"sha256":       "abcdef0123456789abcdef",
	})
	notice, verified := ArmOutcomeNotice(time.Now())
	if !verified {
		t.Fatalf("numGC reset (armed above the live count) must verify the exec: %q", notice)
	}
	if !strings.Contains(notice, "restart verified") {
		t.Errorf("verified notice shape: %s", notice)
	}
	if _, ok := LoadArmRecord(); ok {
		t.Error("verified record must be cleared")
	}
}

func TestIssue5809StartupAlarmOnLivePidPastExecDeadline(t *testing.T) {
	issue5753IsolatedDir(t)
	alive := os.Getppid()
	issue5809WriteRecord(t, alive, map[string]any{
		"target_path": "/opt/ggcode/bin/ggcode",
		"revision":    "145acc13c7c6",
		"armed_at":    time.Now().Add(-5 * time.Minute).Format(time.RFC3339Nano),
	})
	notice, ok := StaleArmStartupNotice(time.Now())
	if ok || !strings.Contains(notice, "ARMED-BUT-NOT-EXECUTED") {
		t.Fatalf("live arming pid past the exec deadline must alarm, got ok=%v notice=%q", ok, notice)
	}
	if !strings.Contains(notice, "STILL RUNNING") {
		t.Errorf("alarm must say the arming process is still running: %s", notice)
	}
	if _, err := os.Stat(ArmRecordPath(alive)); err != nil {
		t.Error("live-pid record must be kept for its own seat's handshake")
	}
}

func TestIssue5809StartupSkipsYoungLivePidRecord(t *testing.T) {
	issue5753IsolatedDir(t)
	issue5809WriteRecord(t, os.Getppid(), map[string]any{
		"target_path": "/opt/ggcode/bin/ggcode",
		"revision":    "145acc13c7c6",
		"armed_at":    time.Now().Add(-5 * time.Second).Format(time.RFC3339Nano),
	})
	if notice, ok := StaleArmStartupNotice(time.Now()); !ok || notice != "" {
		t.Errorf("young live-pid record inside the exec window must be skipped, got ok=%v notice=%q", ok, notice)
	}
}

func TestIssue5809StartupDeadPidSameRevisionIsNotRecovery(t *testing.T) {
	rev := SelfRevision()
	if rev == "" {
		t.Skip("test binary is not vcs-stamped; recovery semantics need revision evidence")
	}
	issue5753IsolatedDir(t)
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	// The arming process died still running R and the arm-time disk target
	// was also R (apply-at-exec). A successor running R has NOT landed any
	// rollout — recovery requires evidence the image actually changed.
	issue5809WriteRecord(t, dead.Process.Pid, map[string]any{
		"target_path":      "/opt/ggcode/bin/ggcode",
		"revision":         rev,
		"running_revision": rev,
		"armed_at":         time.Now().Add(-2 * time.Minute).Format(time.RFC3339Nano),
	})
	notice, ok := StaleArmStartupNotice(time.Now())
	if ok || !strings.Contains(notice, "ARMED-BUT-NOT-EXECUTED") {
		t.Fatalf("same-revision successor is not recovery, got ok=%v notice=%q", ok, notice)
	}
	if strings.Contains(notice, "restart-recovered") {
		t.Errorf("must not present as recovered: %s", notice)
	}
}

func TestIssue5809StartupDeadPidImageChangeStillRecovers(t *testing.T) {
	rev := SelfRevision()
	if rev == "" {
		t.Skip("test binary is not vcs-stamped; recovery semantics need revision evidence")
	}
	issue5753IsolatedDir(t)
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	issue5809WriteRecord(t, dead.Process.Pid, map[string]any{
		"target_path":      "/opt/ggcode/bin/ggcode",
		"revision":         rev,
		"running_revision": "411da6abf9e0",
		"armed_at":         time.Now().Add(-2 * time.Minute).Format(time.RFC3339Nano),
	})
	notice, _ := StaleArmStartupNotice(time.Now())
	if !strings.Contains(notice, "restart-recovered") {
		t.Fatalf("a successor running the target revision the arming process did not run IS recovery, got %q", notice)
	}
}

func TestIssue5809RecordExecFailureLeavesFootprintWithoutRecord(t *testing.T) {
	issue5753IsolatedDir(t)
	ClearArmRecord("clean slate")
	RecordExecFailure("exec", errors.New("fork/exec /bin/ggcode: no such file or directory"))
	rec, ok := LoadArmRecord()
	if !ok {
		t.Fatal("exec-failure annotation must persist a footprint even when the arm record is missing (#5809 second-order loss)")
	}
	if rec.ExecStage != "exec" || !strings.Contains(rec.ExecError, "no such file or directory") {
		t.Errorf("annotation missing: %+v", rec)
	}
	if rec.ExecFailedAt.IsZero() {
		t.Error("ExecFailedAt must be stamped")
	}
}

func TestIssue5809WriteArmRecordStampsRuntimeIdentity(t *testing.T) {
	issue5753IsolatedDir(t)
	// Force GCs so ArmedNumGC is non-zero and survives the omitempty.
	runtime.GC()
	runtime.GC()
	runtime.GC()
	if _, err := WriteArmRecord(issue5753TargetFile(t), "runtime identity"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ArmRecordPath(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("record is not valid JSON: %v", err)
	}
	if rev := SelfRevision(); rev != "" {
		got, _ := raw["running_revision"].(string)
		if got != rev {
			t.Errorf("running_revision = %q, want the arming process's own running revision %q: %s", got, rev, data)
		}
	}
	gc, ok := raw["armed_num_gc"].(float64)
	if !ok || gc <= 0 {
		t.Errorf("armed_num_gc = %v, want a positive GC count captured at arm time: %s", raw["armed_num_gc"], data)
	}
}
