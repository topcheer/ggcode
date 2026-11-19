package restart

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// #5753: "restart arm silently fails to exec — two consecutive armed OK
// with zero exec footprint". The #5367 intent env var proves an exec landed
// but only inside the NEW process; when the exec silently never happens
// (armRestartMsg dropped with no live TUI program, execve error swallowed on
// a path that keeps serving, rollback before the image swap) there is no
// signal anywhere — while the durable resume nudge still fires 10-30s later
// in the OLD process and presents as a successful restart-plus-resume.
//
// This file closes that gap with a durable, PID-scoped arm record stamped at
// ARM time (before anything else can fail) carrying the exec target's
// content identity (vcs revision + sha256), and verified at the two moments
// the outcome is observable:
//
//   - nudge time: the resume nudge fires in whatever process is alive. If
//     that process is the arming pid still running the OLD image, the nudge
//     prompt is prefixed with a loud ARMED-BUT-NOT-EXECUTED alarm instead of
//     silently presenting as success.
//   - cold start: a process that starts WITHOUT the restart intent while a
//     young arm record exists means the armed exec never completed (the
//     arming process died or kept serving); the startup notice says so.
//
// Records are per-pid files so concurrent arms from multiple ggcode
// processes on one host (fleet seats) never mask each other. Verified and
// expired records are cleared; ArmRecordAlarmWindow bounds how long an
// unverified record keeps alarming.
//
// #5809: the original handshake judged "exec landed" by comparing the
// RUNNING revision against the target's DISK revision captured at arm
// time. With an old process and a disk binary synced after the arm (or the
// apply-at-exec update flow, which stamps the record before the new bytes
// land on disk), that comparison reads as a match and FALSE-VERIFIES: the
// record is cleared, the alarm stays silent, and the seat keeps serving
// the old image for days (fleet incident 2026-11-19). Verification now
// requires RUNTIME evidence that THIS pid's image or runtime changed since
// the arm — the #5367 startup intent banner, a runtime MemStats reset
// (NumGC is monotonic in-process and resets on execve), or a running
// revision that differs from RunningRevision (what the arming process was
// EXECUTING at arm time, never read from disk). Records that survive past
// ArmExecDeadline with no runtime evidence alarm regardless of which
// clear-path was expected to consume them.

// ArmRecordAlarmWindow bounds how long an unverified arm record keeps
// raising alarms. The resume nudge fires 10-30s after the arm; a supervisor
// or manual recovery restart lands within minutes. An hour covers the
// coordinator verification-loop cadence without immortal false alarms.
const ArmRecordAlarmWindow = time.Hour

// ArmExecDeadline bounds how long after an arm a still-unresolved record is
// treated as "the exec may yet land" before it MUST alarm (#5809): the arm
// fires at turn end with a 30s fallback, the exec itself is immediate, and
// a successful successor clears its record at startup intent verification.
// Any record still on disk past this bound with no runtime evidence of the
// exec is armed-but-not-executed regardless of which clear-path was
// expected to consume it.
const ArmExecDeadline = 30 * time.Second

// armRecordEnvDir lets tests and operators isolate the record directory.
const armRecordEnvDir = "GGCODE_RESTART_ARM_DIR"

// ArmRecord is the durable on-disk handshake anchor for a restart arm.
type ArmRecord struct {
	TargetPath string    `json:"target_path"`
	Revision   string    `json:"revision,omitempty"` // target binary's embedded vcs.revision (short form, "" when unstamped)
	SHA256     string    `json:"sha256"`             // hex sha256 of the target binary bytes at arm time
	Size       int64     `json:"size"`
	ArmedAt    time.Time `json:"armed_at"`
	PID        int       `json:"pid"` // process that armed (syscall.Exec keeps the pid, so a successful swap is still the same pid)
	Reason     string    `json:"reason,omitempty"`
	// #5809 runtime identity of the ARMING process, captured from
	// runtime/debug and runtime.MemStats — never from the disk binary.
	// RunningRevision is the revision the arming process was EXECUTING at
	// arm time; ArmedNumGC is its GC count (monotonic within a process,
	// reset by execve). Together they let verification decide "did THIS
	// pid's image or runtime change since the arm" without trusting disk
	// state, which an updater may sync at any moment.
	RunningRevision string `json:"running_revision,omitempty"`
	ArmedNumGC      uint32 `json:"armed_num_gc,omitempty"`
	// Exec failure annotation: when an exec attempt was made and failed, the
	// stage and error are recorded so the alarm can name the cause instead
	// of just the symptom.
	ExecStage    string    `json:"exec_stage,omitempty"`
	ExecError    string    `json:"exec_error,omitempty"`
	ExecFailedAt time.Time `json:"exec_failed_at,omitempty"`
}

// ArmRecordDir returns the directory holding per-pid arm records.
func ArmRecordDir() string {
	if d := os.Getenv(armRecordEnvDir); d != "" {
		return d
	}
	base := util.ConfigDir()
	if base == "" {
		return "restart-arms"
	}
	return filepath.Join(base, "restart-arms")
}

// ArmRecordPath returns the record file for pid.
func ArmRecordPath(pid int) string {
	return filepath.Join(ArmRecordDir(), fmt.Sprintf("restart-arm-%d.json", pid))
}

// FileRevision extracts the embedded vcs.revision from a binary file (""
// when absent, e.g. unstamped builds or non-Go files). Shared by the arm
// record and the restart tool's arm-time target report.
func FileRevision(path string) string {
	bi, err := buildinfo.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, kv := range bi.Settings {
		if kv.Key == "vcs.revision" && kv.Value != "" {
			if len(kv.Value) > 12 {
				return kv.Value[:12]
			}
			return kv.Value
		}
	}
	return ""
}

// runtimeNumGC reads the current runtime GC count. NumGC is monotonic
// within one process lifetime and resets to ~0 when execve replaces the
// image with a fresh Go runtime — the machine-checkable form of the
// operator's "fresh memstats line" verification signal (#5809).
func runtimeNumGC() uint32 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.NumGC
}

// runningImage is the runtime evidence about THIS process at verification
// time: the running build's vcs revision, its GC count, and whether the
// #5367 startup intent banner already matched in this process. Everything
// in it comes from the runtime, never from the on-disk binary (#5809).
type runningImage struct {
	Revision       string
	NumGC          uint32
	IntentVerified bool
}

// currentRunningImage captures the running-image evidence for the
// nudge-time handshake.
func currentRunningImage(intentVerified bool) runningImage {
	return runningImage{
		Revision:       SelfRevision(),
		NumGC:          runtimeNumGC(),
		IntentVerified: intentVerified,
	}
}

func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

// WriteArmRecord stamps the durable handshake anchor for THIS process's
// restart arm. Called at arm time — before the nudge is scheduled and before
// the restart requester is invoked — so every later failure mode (dropped
// arm message, swallowed exec error, rollback) happens with the record
// already on disk. Best-effort fields: a missing sha/revision degrades the
// handshake to "unverified" but never blocks the restart itself.
func WriteArmRecord(targetPath, reason string) (ArmRecord, error) {
	rec := ArmRecord{
		TargetPath: targetPath,
		ArmedAt:    time.Now(),
		PID:        os.Getpid(),
		Reason:     reason,
	}
	// #5809: capture what THIS process is actually running at arm time.
	// Verification later compares the running image against
	// RunningRevision/ArmedNumGC — the disk identity below is message
	// context, never exec proof.
	rec.RunningRevision = SelfRevision()
	rec.ArmedNumGC = runtimeNumGC()
	if fi, err := os.Stat(targetPath); err == nil && !fi.IsDir() {
		rec.Size = fi.Size()
		if sum, _, err := fileSHA256(targetPath); err == nil {
			rec.SHA256 = sum
		} else {
			debug.Log("restart", "arm record: target sha unavailable: %v", err)
		}
		rec.Revision = FileRevision(targetPath)
	} else if err != nil {
		debug.Log("restart", "arm record: target stat failed: %v", err)
	}
	if err := saveArmRecord(rec); err != nil {
		return rec, fmt.Errorf("write arm record: %w", err)
	}
	debug.Log("restart", "arm record stamped: pid=%d target=%s revision=%s sha=%s", rec.PID, rec.TargetPath, revOrUnknown(rec.Revision), shortSHA(rec.SHA256))
	return rec, nil
}

func saveArmRecord(rec ArmRecord) error {
	if err := os.MkdirAll(ArmRecordDir(), 0o755); err != nil {
		return fmt.Errorf("create arm record dir: %w", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return util.AtomicWriteFile(ArmRecordPath(rec.PID), data, 0o644)
}

// LoadArmRecord reads this process's own arm record.
func LoadArmRecord() (ArmRecord, bool) {
	return loadArmRecordAt(ArmRecordPath(os.Getpid()))
}

func loadArmRecordAt(path string) (ArmRecord, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ArmRecord{}, false
	}
	var rec ArmRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return ArmRecord{}, false
	}
	return rec, true
}

// ClearArmRecord removes this process's arm record (handshake complete or
// record expired).
func ClearArmRecord(why string) {
	if err := os.Remove(ArmRecordPath(os.Getpid())); err != nil && !os.IsNotExist(err) {
		debug.Log("restart", "arm record clear failed: %v", err)
		return
	}
	debug.Log("restart", "arm record cleared: %s", why)
}

// RecordExecFailure annotates this process's arm record with the stage and
// error of a failed exec attempt, so the nudge-time/startup alarms can name
// the cause ("fork/exec ...: no such file or directory") instead of only the
// symptom. Records are only annotated when an arm receipt is outstanding;
// exec paths without a prior arm (slash-command restarts) get their record
// written by ExecRestart before the attempt.
func RecordExecFailure(stage string, err error) {
	rec, ok := LoadArmRecord()
	if !ok {
		// #5809 persistence hardening: when the arm-time write failed or
		// never happened, the previous code dropped the exec-failure
		// annotation entirely — the failure left ZERO durable footprint and
		// the alarms had nothing to fire on (the 33495 second-order loss:
		// two arms, no exec, external kill, no record ever persisted).
		// Reconstruct a best-effort record so the cause survives.
		target := "(unresolved)"
		if bin, rerr := ResolveBinary(); rerr == nil && bin != "" {
			target = bin
		}
		rec = ArmRecord{
			TargetPath:      target,
			ArmedAt:         time.Now(),
			PID:             os.Getpid(),
			Reason:          "reconstructed at exec-failure time (#5809: arm record was missing)",
			RunningRevision: SelfRevision(),
			ArmedNumGC:      runtimeNumGC(),
		}
	}
	rec.ExecStage = stage
	if err != nil {
		rec.ExecError = err.Error()
	}
	rec.ExecFailedAt = time.Now()
	if serr := saveArmRecord(rec); serr != nil {
		debug.Log("restart", "failed to persist exec-failure annotation: %v", serr)
	}
	debug.Log("restart", "restart exec failure recorded (stage %s): %v", stage, err)
}

// startupIntentState remembers whether THIS process started as the result of
// a verified restart exec (the #5367 banner matched). It is the fallback
// handshake evidence for unstamped builds where the running image carries no
// vcs revision to compare.
var startupIntentState struct {
	sync.Mutex
	verified bool
}

// NoteStartupIntent records the outcome of the #5367 startup intent banner.
// On a match the armed exec demonstrably landed, so the arm record is
// cleared (handshake complete). On a mismatch the record stays: the rollout
// did not take effect even though an exec happened.
func NoteStartupIntent(matched bool) {
	startupIntentState.Lock()
	startupIntentState.verified = matched
	startupIntentState.Unlock()
	if matched {
		ClearArmRecord("startup intent verified (#5367 banner matched)")
	}
}

func startupIntentVerified() bool {
	startupIntentState.Lock()
	defer startupIntentState.Unlock()
	return startupIntentState.verified
}

// ArmOutcomeNotice is the nudge-time half of the #5753 handshake. It is
// called when the durable resume nudge fires, in whatever process is alive:
//
//   - exec landed — proven by RUNTIME evidence only (#5809): the startup
//     intent banner matched in this process, the runtime counters reset
//     (NumGC below the armed count), or the running revision differs from
//     the revision the arming process was EXECUTING at arm time: quiet
//     confirmation, record cleared;
//   - the armed target's revision is known and NOT running, or no runtime
//     evidence of a swap exists (this process still runs the arming
//     image — including the unstamped case): loud ARMED-BUT-NOT-EXECUTED
//     alarm with the recorded revisions and any recorded exec failure —
//     the nudge must not present as a successful restart it is not;
//   - no record (or an expired one): "" — nudge unchanged.
//
// Content identity of the RUNNING image is taken from runtime/debug and
// runtime.MemStats, never from the on-disk file: a rebuild-in-place or a
// post-arm disk sync leaves the new bytes on disk while the old process
// keeps running, so the disk file cannot prove what image a live process
// is executing — comparing against it was the #5809 false pass.
func ArmOutcomeNotice(now time.Time) (string, bool) {
	rec, ok := LoadArmRecord()
	if !ok {
		return "", true
	}
	if now.Sub(rec.ArmedAt) > ArmRecordAlarmWindow {
		ClearArmRecord("expired (older than alarm window)")
		return "", true
	}
	notice, verified := armOutcomeFor(rec, currentRunningImage(startupIntentVerified()), now)
	if verified {
		ClearArmRecord("running image verified against armed target")
	}
	return notice, verified
}

// armOutcomeFor is the pure decision core of ArmOutcomeNotice. It decides
// from RUNTIME evidence only (#5809): the arm-time disk revision is message
// context, never proof of execution.
func armOutcomeFor(rec ArmRecord, run runningImage, now time.Time) (string, bool) {
	age := now.Sub(rec.ArmedAt).Round(time.Second)
	execNote := ""
	if rec.ExecError != "" {
		execNote = fmt.Sprintf(" A failed exec attempt was recorded (stage %s): %s.", rec.ExecStage, rec.ExecError)
	}
	// 1. The #5367 startup banner matched in THIS process: the armed exec
	//    demonstrably landed here — the strongest same-pid evidence, and the
	//    only revision-independent one for unstamped builds before #5809.
	if run.IntentVerified {
		return "[restart verified: startup intent banner matched the armed exec target.]", true
	}
	// 2. The armed target's revision is known and this process runs a
	//    different one: the target never took effect — loud alarm.
	if run.Revision != "" && rec.Revision != "" && run.Revision != rec.Revision {
		return fmt.Sprintf(
			"restart-alarm: ARMED-BUT-NOT-EXECUTED — a restart armed %s ago (target %s, revision %s) never took effect: this process (pid %d) is still running revision %s. The earlier 'OK: restart armed' receipt certified only the arming, not the execution; treat it as void.%s Re-run the restart tool (or restart manually) and verify via the arm report's current revision plus a fresh memstats line (numGC resets on exec) — process start time is NOT a signal (unchanged across execve).",
			age, rec.TargetPath, rec.Revision, rec.PID, run.Revision, execNote), false
	}
	// 3. Runtime reset: NumGC is monotonic within a process and resets on
	//    execve. A live count BELOW the armed count proves this pid's
	//    runtime restarted since the arm — the exec landed. Works for
	//    unstamped and same-image restarts alike.
	if rec.ArmedNumGC > 0 && run.NumGC < rec.ArmedNumGC {
		return fmt.Sprintf("[restart verified: runtime counters reset since the arm (numGC %d -> %d in pid %d) — the restart exec landed.]",
			rec.ArmedNumGC, run.NumGC, rec.PID), true
	}
	// 4. Image change: this process runs the armed target's revision AND
	//    that differs from what the arming process was RUNNING at arm time.
	//    A revision match against the arm-time disk state ALONE is not
	//    accepted: with the old process still serving and the disk synced
	//    after the arm, it matches trivially — the #5809 false pass.
	if run.Revision != "" && rec.Revision != "" && run.Revision == rec.Revision &&
		rec.RunningRevision != "" && run.Revision != rec.RunningRevision {
		return fmt.Sprintf("[restart verified: running image revision %s matches the armed target and differs from the arming process's running revision %s (armed %s ago) — the restart exec landed.]",
			run.Revision, rec.RunningRevision, age), true
	}
	// 5. No runtime evidence of an exec: same revision as the arming process
	//    was running (or no revision evidence at all). The exec did not
	//    provably land in THIS process — loud alarm (#5809 ask 2: an armed
	//    record surviving to nudge time must alarm regardless of which
	//    clear-path was expected to consume it; the nudge fires after the
	//    arm's turn-end/30s fire window, so silence here is exactly the
	//    fleet false-pass shape).
	return fmt.Sprintf(
		"restart-alarm: ARMED-BUT-NOT-EXECUTED — a restart armed %s ago (target %s, revision %s, sha256 %s) never provably landed: this process (pid %d) still runs revision %s — the same image the arming process was running at arm time. A match against the arm-time DISK revision proves nothing (an old process with a since-synced disk binary reads as a match; pre-#5809 that false-verified and cleared this record). The earlier 'OK: restart armed' receipt certified only the arming, not the execution; treat it as void.%s Re-run the restart tool (or restart manually) and verify via the arm report's current revision plus a FRESH memstats line (numGC resets on exec) — process start time is NOT a signal (unchanged across execve).",
		age, rec.TargetPath, revOrUnknown(rec.Revision), shortSHA(rec.SHA256), rec.PID, revOrUnknown(run.Revision), execNote), false
}

// StaleArmStartupNotice is the cold-start half of the #5753 handshake. It is
// called on startup when GGCODE_RESTART_INTENT is ABSENT (this process was
// not started by the armed exec). A young arm record then means the previous
// armed restart never completed its swap:
//
//   - arming pid still alive and the record still inside the exec window:
//     skipped here to avoid cross-seat noise — the arming seat's own
//     nudge-time handshake owns it;
//   - arming pid still alive PAST ArmExecDeadline with the record
//     unresolved: loud startup alarm too (#5809 ask 2) — the nudge may
//     have been lost, muted, or false-verified by the pre-#5809
//     disk-revision check; the record is kept (the arming pid's handshake
//     still owns it) and expiry bounds the noise;
//   - arming pid dead (the process died before/without the exec): loud
//     startup alarm naming the armed target, unless this process PROVABLY
//     runs a newer image than the arming process did (running revision ==
//     target revision AND != the arming process's recorded running
//     revision — a supervisor/manual restart landed the rollout anyway:
//     quiet recovery note instead). Running the same revision the arming
//     process was already running is NOT recovery: it proves nothing about
//     the swap (#5809);
//   - expired records are swept silently.
func StaleArmStartupNotice(now time.Time) (string, bool) {
	if os.Getenv(IntentEnvKey) != "" {
		return "", true // intent present: handled by StartupVerifyNotice
	}
	matches, err := filepath.Glob(filepath.Join(ArmRecordDir(), "restart-arm-*.json"))
	if err != nil || len(matches) == 0 {
		return "", true
	}
	self := os.Getpid()
	notice := ""
	for _, path := range matches {
		rec, ok := loadArmRecordAt(path)
		if !ok || rec.PID == 0 || rec.PID == self {
			continue
		}
		age := now.Sub(rec.ArmedAt)
		if age > ArmRecordAlarmWindow {
			_ = os.Remove(path)
			debug.Log("restart", "swept expired arm record %s", path)
			continue
		}
		execNote := ""
		if rec.ExecError != "" {
			execNote = fmt.Sprintf(" The exec attempt failed (stage %s): %s.", rec.ExecStage, rec.ExecError)
		}
		if processAlive(rec.PID) {
			// #5809 ask 2: an armed record surviving past the exec deadline
			// on a LIVE arming process is armed-but-not-executed no matter
			// which clear-path was expected to consume it. The record is
			// kept: the arming pid's own handshake still owns it, and the
			// alarm window bounds repetition.
			if age > ArmExecDeadline {
				notice = fmt.Sprintf(
					"restart-alarm: ARMED-BUT-NOT-EXECUTED — process %d armed a restart %s ago (target %s, revision %s, reason %q) and is STILL RUNNING past the exec deadline (%s) with the arm record unresolved: the armed exec never provably landed, and its arm receipt certified only the arming.%s Restart that process from the intended binary and verify via the arm report's current revision plus a fresh memstats line (numGC resets on exec).",
					rec.PID, age.Round(time.Second), rec.TargetPath, revOrUnknown(rec.Revision), rec.Reason, ArmExecDeadline, execNote)
			}
			continue
		}
		_ = os.Remove(path)
		ageStr := age.Round(time.Second)
		// #5809: recovery requires evidence the image actually changed.
		// rev == target alone used to read as "rollout is live", but when
		// the armed target equaled the arming process's running image
		// (same-image restart, or the apply-at-exec flow stamping the record
		// before the new bytes landed) the new process running that same
		// revision proves nothing. Legacy records without RunningRevision
		// keep the tolerant behavior (unknowable; they age out within the
		// alarm window).
		if rev := SelfRevision(); rev != "" && rec.Revision != "" && rev == rec.Revision &&
			(rec.RunningRevision == "" || rev != rec.RunningRevision) {
			notice = fmt.Sprintf(
				"restart-recovered: process %d armed a restart %s ago (target %s, revision %s) and died before the swap, but this process already runs the target revision %s (the arming process ran %s) — the rollout is live.",
				rec.PID, ageStr, rec.TargetPath, rec.Revision, rev, revOrUnknown(rec.RunningRevision))
			continue
		}
		notice = fmt.Sprintf(
			"restart-alarm: ARMED-BUT-NOT-EXECUTED — process %d armed a restart %s ago (target %s, revision %s, reason %q) and exited without completing the swap; this process started without a restart intent (running revision %s, arming process ran %s). The arm receipt said OK but the exec never landed.%s Restart from the intended binary and verify via the arm report's current revision plus a fresh memstats line (numGC resets on exec).",
			rec.PID, ageStr, rec.TargetPath, revOrUnknown(rec.Revision), rec.Reason, revOrUnknown(SelfRevision()), revOrUnknown(rec.RunningRevision), execNote)
	}
	if notice == "" {
		return "", true
	}
	return notice, false
}

func revOrUnknown(rev string) string {
	if rev == "" {
		return "unknown"
	}
	return rev
}

func shortSHA(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	if sum == "" {
		return "unavailable"
	}
	return sum
}
