package restart

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// zz_issue5753_test.go pins the restart-arm outcome handshake: an armed
// receipt that silently fails to exec (while still emitting the resume
// nudge) must become observable. See armrecord.go for the design.

func issue5753IsolatedDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv(armRecordEnvDir, d)
	return d
}

func issue5753TargetFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ggcode-fake")
	if err := os.WriteFile(p, []byte("fake binary bytes for #5753"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func issue5753SHA(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestIssue5753ArmRecordRoundTrip(t *testing.T) {
	issue5753IsolatedDir(t)
	target := issue5753TargetFile(t)

	rec, err := WriteArmRecord(target, "binary updated to fix #5753")
	if err != nil {
		t.Fatalf("WriteArmRecord: %v", err)
	}
	if rec.PID != os.Getpid() {
		t.Errorf("record pid = %d, want own pid %d", rec.PID, os.Getpid())
	}
	if rec.Reason != "binary updated to fix #5753" {
		t.Errorf("record reason = %q", rec.Reason)
	}
	if rec.SHA256 != issue5753SHA(t, target) {
		t.Errorf("record sha256 = %s, want sha of target bytes", rec.SHA256)
	}
	// The staged fake is not a Go binary: revision must degrade to empty,
	// never error the arm.
	if rec.Revision != "" {
		t.Errorf("record revision = %q, want empty for non-Go target", rec.Revision)
	}

	loaded, ok := LoadArmRecord()
	if !ok {
		t.Fatal("LoadArmRecord after write: not found")
	}
	// Compare field-wise: time.Time fields lose monotonic readings across
	// JSON while remaining Equal; normalize every time field on both sides
	// so only wall-clock content is compared (zero ExecFailedAt included).
	loaded.ArmedAt = loaded.ArmedAt.Round(0)
	loaded.ExecFailedAt = loaded.ExecFailedAt.Round(0)
	want := rec
	want.ArmedAt = want.ArmedAt.Round(0)
	want.ExecFailedAt = want.ExecFailedAt.Round(0)
	// Compare explicitly: struct != compares time.Time bitwise, and the
	// internal *Location differs between a fresh time.Now() (Local) and a
	// JSON-unmarshaled instant (UTC) even when the instants are identical.
	// That makes != fail whenever Local is not the UTC location (CI runners
	// with TZ set) while passing on UTC-default hosts. Compare instants via
	// .Equal and primitives via ==.
	if !loaded.ArmedAt.Equal(want.ArmedAt) {
		t.Errorf("ArmedAt: got %v want %v", loaded.ArmedAt, want.ArmedAt)
	}
	if !loaded.ExecFailedAt.Equal(want.ExecFailedAt) {
		t.Errorf("ExecFailedAt: got %v want %v", loaded.ExecFailedAt, want.ExecFailedAt)
	}
	if loaded.TargetPath != want.TargetPath || loaded.Revision != want.Revision ||
		loaded.SHA256 != want.SHA256 || loaded.Size != want.Size ||
		loaded.PID != want.PID || loaded.Reason != want.Reason ||
		loaded.ExecStage != want.ExecStage || loaded.ExecError != want.ExecError {
		t.Errorf("round-trip mismatch: got %+v want %+v", loaded, want)
	}
	// #5809: the arming process's runtime identity must round-trip too.
	if loaded.RunningRevision != want.RunningRevision || loaded.ArmedNumGC != want.ArmedNumGC {
		t.Errorf("running-identity round-trip mismatch: got %+v want %+v", loaded, want)
	}

	ClearArmRecord("test")
	if _, ok := LoadArmRecord(); ok {
		t.Error("record still present after ClearArmRecord")
	}
}

func TestIssue5753NudgeAlarmOnOldImage(t *testing.T) {
	// The incident shape: the process that armed is still alive running the
	// OLD image (running revision != armed target revision). The nudge-time
	// notice must be a loud ARMED-BUT-NOT-EXECUTED alarm naming both
	// revisions, and must include the recorded exec failure when present.
	rec := ArmRecord{
		TargetPath: "/opt/ggcode/bin/ggcode",
		Revision:   "145acc13c7c6",
		SHA256:     "abcdef0123456789",
		ArmedAt:    time.Now().Add(-15 * time.Second),
		PID:        31828,
	}
	notice, verified := armOutcomeFor(rec, runningImage{Revision: "411da6abf9e0"}, time.Now())
	if verified {
		t.Fatal("old-image notice must not be verified")
	}
	for _, want := range []string{"ARMED-BUT-NOT-EXECUTED", "145acc13c7c6", "411da6abf9e0", "31828"} {
		if !strings.Contains(notice, want) {
			t.Errorf("alarm missing %q: %s", want, notice)
		}
	}

	rec.ExecStage = "arm-delivery"
	rec.ExecError = "armRestartMsg undeliverable: no live TUI program"
	notice, _ = armOutcomeFor(rec, runningImage{Revision: "411da6abf9e0"}, time.Now())
	if !strings.Contains(notice, "arm-delivery") || !strings.Contains(notice, "no live TUI program") {
		t.Errorf("alarm must surface the recorded exec failure: %s", notice)
	}
}

func TestIssue5753NudgeVerifiedOnNewImage(t *testing.T) {
	// #5809: a revision match only verifies when the running revision
	// DIFFERS from what the arming process was executing at arm time —
	// that difference is the runtime evidence that the image changed.
	rec := ArmRecord{
		TargetPath:      "/opt/ggcode/bin/ggcode",
		Revision:        "145acc13c7c6",
		RunningRevision: "411da6abf9e0",
		ArmedAt:         time.Now().Add(-12 * time.Second),
		PID:             31828,
	}
	notice, verified := armOutcomeFor(rec, runningImage{Revision: "145acc13c7c6"}, time.Now())
	if !verified {
		t.Fatalf("target revision running and arming revision superseded must verify: %s", notice)
	}
	if !strings.Contains(notice, "restart verified") || !strings.Contains(notice, "145acc13c7c6") {
		t.Errorf("verified notice shape: %s", notice)
	}
}

func TestIssue5753UnstampedBuildIsExplicitlyUnverified(t *testing.T) {
	// No vcs revision on either side: the handshake must SAY it cannot
	// verify (with the sha to check manually) instead of staying silent —
	// silent success is the #5753 defect.
	rec := ArmRecord{
		TargetPath: "/opt/ggcode/bin/ggcode",
		SHA256:     "abcdef0123456789",
		ArmedAt:    time.Now().Add(-20 * time.Second),
		PID:        42,
	}
	notice, verified := armOutcomeFor(rec, runningImage{}, time.Now())
	if verified {
		t.Fatal("unstamped build must not verify by revision")
	}
	// #5809: the soft "could not be verified" note was upgraded to the
	// loud alarm (with the sha and the correct verification signals) —
	// soft notes got lost while seats served the old image for days.
	for _, want := range []string{"ARMED-BUT-NOT-EXECUTED", "abcdef012345", "numGC"} {
		if !strings.Contains(notice, want) {
			t.Errorf("unverified notice missing %q: %s", want, notice)
		}
	}
	// But a startup intent banner that matched is independent evidence.
	if _, verified := armOutcomeFor(rec, runningImage{IntentVerified: true}, time.Now()); !verified {
		t.Error("startup intent match must verify unstamped builds")
	}
}

func TestIssue5753ArmOutcomeNoticeClearsOnVerifyAndExpiry(t *testing.T) {
	issue5753IsolatedDir(t)

	// No record: nothing to say.
	if notice, _ := ArmOutcomeNotice(time.Now()); notice != "" {
		t.Errorf("no record must produce empty notice, got %q", notice)
	}

	// #5809: a record written by THIS still-running process carries no
	// runtime evidence of an exec (same running revision, numGC monotonic
	// since the arm) and must NOT self-verify at nudge time — it alarms and
	// the record stays for the deadline/startup surfaces to keep pressing.
	if _, err := WriteArmRecord(issue5753TargetFile(t), "verify-clear"); err != nil {
		t.Fatal(err)
	}
	notice, verified := ArmOutcomeNotice(time.Now())
	if verified {
		t.Fatalf("same-process record must not self-verify: %q", notice)
	}
	if !strings.Contains(notice, "ARMED-BUT-NOT-EXECUTED") {
		t.Errorf("expected alarm notice, got %q", notice)
	}
	if _, ok := LoadArmRecord(); !ok {
		t.Error("unverified record must be kept")
	}
	ClearArmRecord("test reset")

	// Expired record: cleared silently, no immortal alarms.
	rec2, err := WriteArmRecord(issue5753TargetFile(t), "expired")
	if err != nil {
		t.Fatal(err)
	}
	rec2.ArmedAt = time.Now().Add(-2 * ArmRecordAlarmWindow)
	if err := saveArmRecord(rec2); err != nil {
		t.Fatal(err)
	}
	if notice, _ := ArmOutcomeNotice(time.Now()); notice != "" {
		t.Errorf("expired record must not alarm, got %q", notice)
	}
	if _, ok := LoadArmRecord(); ok {
		t.Error("expired record must be swept")
	}
}

func TestIssue5753RecordExecFailureAnnotates(t *testing.T) {
	issue5753IsolatedDir(t)
	if _, err := WriteArmRecord(issue5753TargetFile(t), "annot"); err != nil {
		t.Fatal(err)
	}
	RecordExecFailure("exec", errors.New("fork/exec /bin/ggcode: no such file or directory"))
	rec, ok := LoadArmRecord()
	if !ok {
		t.Fatal("record disappeared on exec-failure annotation")
	}
	if rec.ExecStage != "exec" || !strings.Contains(rec.ExecError, "no such file or directory") {
		t.Errorf("annotation missing: %+v", rec)
	}
	if rec.ExecFailedAt.IsZero() {
		t.Error("ExecFailedAt must be stamped")
	}

	// #5809 hardening: without an outstanding record the annotation now
	// RECONSTRUCTS one — the old no-op dropped the failure footprint
	// entirely (the 33495 second-order loss: arms whose record never
	// persisted left the alarms nothing to fire on).
	ClearArmRecord("test")
	RecordExecFailure("exec", errors.New("fork/exec /bin/ggcode: permission denied"))
	rec2, ok := LoadArmRecord()
	if !ok {
		t.Fatal("exec-failure annotation must reconstruct a missing record (#5809)")
	}
	if rec2.ExecStage != "exec" || !strings.Contains(rec2.ExecError, "permission denied") {
		t.Errorf("reconstructed annotation missing: %+v", rec2)
	}
	if rec2.PID != os.Getpid() || rec2.TargetPath == "" || rec2.ExecFailedAt.IsZero() {
		t.Errorf("reconstructed record incomplete: %+v", rec2)
	}
	if !strings.Contains(rec2.Reason, "#5809") {
		t.Errorf("reconstructed record must mark its origin: %+v", rec2)
	}
}

func TestIssue5753StaleArmStartupNotice(t *testing.T) {
	issue5753IsolatedDir(t)

	// Dead arming pid with a young record: loud startup alarm, record
	// consumed (removed) so it reports once.
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	rec := ArmRecord{
		TargetPath: "/opt/ggcode/bin/ggcode",
		Revision:   "145acc13c7c6",
		ArmedAt:    time.Now().Add(-2 * time.Minute),
		PID:        dead.Process.Pid,
		Reason:     "binary sync",
		ExecStage:  "exec",
		ExecError:  "fork/exec /bin/ggcode: permission denied",
	}
	if err := saveArmRecord(rec); err != nil {
		t.Fatal(err)
	}
	notice, ok := StaleArmStartupNotice(time.Now())
	if ok || notice == "" {
		t.Fatalf("dead-pid young record must alarm, got ok=%v notice=%q", ok, notice)
	}
	for _, want := range []string{"ARMED-BUT-NOT-EXECUTED", "permission denied", "binary sync"} {
		if !strings.Contains(notice, want) {
			t.Errorf("startup alarm missing %q: %s", want, notice)
		}
	}
	if _, err := os.Stat(ArmRecordPath(rec.PID)); !os.IsNotExist(err) {
		t.Error("reported record must be consumed")
	}

	// Alive foreign pid (another seat mid-handshake, still inside the
	// #5809 exec window): skipped, record kept.
	rec2 := rec
	rec2.PID = os.Getppid()
	rec2.ExecError = ""
	rec2.ArmedAt = time.Now().Add(-5 * time.Second)
	if err := saveArmRecord(rec2); err != nil {
		t.Fatal(err)
	}
	if notice, ok := StaleArmStartupNotice(time.Now()); !ok || notice != "" {
		t.Errorf("alive foreign pid must be skipped, got ok=%v notice=%q", ok, notice)
	}
	if _, err := os.Stat(ArmRecordPath(rec2.PID)); err != nil {
		t.Error("alive foreign record must be kept for its own seat")
	}

	// Expired foreign record: swept silently.
	rec3 := rec
	rec3.PID = dead.Process.Pid + 1000000 // ensure not self, not alive
	rec3.ArmedAt = time.Now().Add(-3 * ArmRecordAlarmWindow)
	if err := saveArmRecord(rec3); err != nil {
		t.Fatal(err)
	}
	if notice, ok := StaleArmStartupNotice(time.Now()); !ok || notice != "" {
		t.Errorf("expired record must not alarm, got ok=%v notice=%q", ok, notice)
	}
	if _, err := os.Stat(ArmRecordPath(rec3.PID)); !os.IsNotExist(err) {
		t.Error("expired record must be swept")
	}
}

func TestIssue5753FileRevisionOnTestBinary(t *testing.T) {
	// The shared FileRevision helper must read the revision out of a real
	// Go binary (the running test binary) — self-consistency with
	// SelfRevision() when the build is vcs-stamped.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got, self := FileRevision(exe), SelfRevision(); got != self {
		t.Errorf("FileRevision(test binary) = %q, SelfRevision() = %q", got, self)
	}
	if FileRevision(issue5753TargetFile(t)) != "" {
		t.Error("non-Go file must yield empty revision")
	}
}

func TestIssue5753RecordJSONShape(t *testing.T) {
	issue5753IsolatedDir(t)
	if _, err := WriteArmRecord(issue5753TargetFile(t), "shape"); err != nil {
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
	for _, key := range []string{"target_path", "sha256", "armed_at", "pid"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("record JSON missing key %q: %s", key, data)
		}
	}
}
