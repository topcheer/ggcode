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

// ArmRecordAlarmWindow bounds how long an unverified arm record keeps
// raising alarms. The resume nudge fires 10-30s after the arm; a supervisor
// or manual recovery restart lands within minutes. An hour covers the
// coordinator verification-loop cadence without immortal false alarms.
const ArmRecordAlarmWindow = time.Hour

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
		return
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
//   - exec landed (running revision == armed target revision, or the startup
//     intent banner already matched): quiet confirmation, record cleared;
//   - still the OLD image in the arming process: loud ARMED-BUT-NOT-EXECUTED
//     alarm with both revisions and any recorded exec failure — the nudge
//     must not present as a successful restart it is not;
//   - unstamped build with no revision evidence: an explicit UNVERIFIED
//     notice telling the reader how to verify, instead of silent success;
//   - no record (or a foreign/expired one): "" — nudge unchanged.
//
// Content identity of the RUNNING image is taken from runtime/debug (the
// compiled-in revision), never from the on-disk file: a rebuild-in-place
// leaves the new bytes on disk while the old process keeps running, so the
// disk file cannot prove what image a live process is executing.
func ArmOutcomeNotice(now time.Time) (string, bool) {
	rec, ok := LoadArmRecord()
	if !ok {
		return "", true
	}
	if now.Sub(rec.ArmedAt) > ArmRecordAlarmWindow {
		ClearArmRecord("expired (older than alarm window)")
		return "", true
	}
	notice, verified := armOutcomeFor(rec, SelfRevision(), startupIntentVerified(), now)
	if verified {
		ClearArmRecord("running image verified against armed target")
	}
	return notice, verified
}

// armOutcomeFor is the pure decision core of ArmOutcomeNotice.
func armOutcomeFor(rec ArmRecord, runningRevision string, intentVerified bool, now time.Time) (string, bool) {
	age := now.Sub(rec.ArmedAt).Round(time.Second)
	execNote := ""
	if rec.ExecError != "" {
		execNote = fmt.Sprintf(" A failed exec attempt was recorded (stage %s): %s.", rec.ExecStage, rec.ExecError)
	}
	if runningRevision != "" && rec.Revision != "" {
		if runningRevision == rec.Revision {
			return fmt.Sprintf("[restart verified: running image revision %s matches the armed target (armed %s ago) — the restart exec landed.]",
				runningRevision, age), true
		}
		return fmt.Sprintf(
			"restart-alarm: ARMED-BUT-NOT-EXECUTED — a restart armed %s ago (target %s, revision %s) never took effect: this process (pid %d) is still running revision %s. The earlier 'OK: restart armed' receipt certified only the arming, not the execution; treat it as void.%s Re-run the restart tool (or restart manually) and verify the running revision afterwards.",
			age, rec.TargetPath, rec.Revision, rec.PID, runningRevision, execNote), false
	}
	if intentVerified {
		return "[restart verified: startup intent banner matched the armed exec target.]", true
	}
	return fmt.Sprintf(
		"restart-note: a restart armed %s ago (target %s, revision %s, sha256 %s) could not be verified in-process: the running image carries no vcs revision (unstamped build). Do NOT treat the restart as done — verify via the startup banner or the process start time before relying on the new image.%s",
		age, rec.TargetPath, revOrUnknown(rec.Revision), shortSHA(rec.SHA256), execNote), false
}

// StaleArmStartupNotice is the cold-start half of the #5753 handshake. It is
// called on startup when GGCODE_RESTART_INTENT is ABSENT (this process was
// not started by the armed exec). A young arm record then means the previous
// armed restart never completed its swap:
//
//   - arming pid still alive: it kept serving the old image — its own
//     nudge-time alarm covers it; skipped here to avoid cross-seat noise;
//   - arming pid dead (the process died before/without the exec): loud
//     startup alarm naming the armed target, unless this process already
//     runs the target revision (a supervisor/manual restart landed the
//     rollout anyway — quiet recovery note instead);
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
		if processAlive(rec.PID) {
			continue
		}
		_ = os.Remove(path)
		ageStr := age.Round(time.Second)
		execNote := ""
		if rec.ExecError != "" {
			execNote = fmt.Sprintf(" The exec attempt failed (stage %s): %s.", rec.ExecStage, rec.ExecError)
		}
		if rev := SelfRevision(); rev != "" && rec.Revision != "" && rev == rec.Revision {
			notice = fmt.Sprintf(
				"restart-recovered: process %d armed a restart %s ago (target %s, revision %s) and died before the swap, but this process already runs the target revision %s — the rollout is live.",
				rec.PID, ageStr, rec.TargetPath, rec.Revision, rev)
			continue
		}
		notice = fmt.Sprintf(
			"restart-alarm: ARMED-BUT-NOT-EXECUTED — process %d armed a restart %s ago (target %s, revision %s, reason %q) and exited without completing the swap; this process started without a restart intent (running revision %s). The arm receipt said OK but the exec never landed.%s Restart from the intended binary and verify the startup banner.",
			rec.PID, ageStr, rec.TargetPath, revOrUnknown(rec.Revision), rec.Reason, revOrUnknown(SelfRevision()), execNote)
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
