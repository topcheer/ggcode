package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Crash-restore idempotency window for the duplicate-suppression ledger
// (tool_dedup.go).
//
// The in-memory ledger dies with the process. After a crash, session resume
// injects a [Crash Recovery] notice that typically prompts the model to
// re-issue the mutating calls it remembers making - a duplicate `git push`,
// duplicate IM send, duplicate a2a task dispatch. Neither the fresh
// (empty) ledger nor the 60s TTL defends against this: the replay happens
// minutes later in a new process. This is the "revived authority" window of
// semantic-rollback attacks (ACRFence, arXiv:2603.20625) and the stale-intent
// replay of checkpoint-resume execution ("Safe to Resume?", arXiv:2608.29381).
//
// Mechanism: every successful mutating call appends one line to a per-session
// sidecar file next to the run journal (append-only, so a crash mid-write
// costs at most one torn final line - the reader skips unparseable lines).
// MarkRunning/MarkCompleted delete the sidecar, so its mere existence after
// resume means the previous run crashed mid-flight. SeedCrashDedup feeds the
// surviving records (within the crash window) into the new ledger as a
// separate epoch-free table; a hit suppresses re-execution with an advisory
// (the pre-crash result body was not persisted, so nothing is replayed -
// the model is told to verify the side effect's real state instead).
//
// Kill switch: GGCODE_TOOL_DEDUP=0 (shared with the base ledger).

const (
	// crashDedupTTL bounds how long after the crash a replayed mutating call
	// is still suppressed. Long enough to cover "restart + resume + notice
	// the model re-issuing its plan", short enough that a legitimate retry
	// the next morning is not blocked.
	crashDedupTTL = 30 * time.Minute
	// crashSidecarMaxBytes caps the sidecar; when exceeded it is rewritten
	// keeping the newest half. Bounded disk, bounded seed time.
	crashSidecarMaxBytes = 64 * 1024
)

// crashMutatingCall is one sidecar record. Arguments are stored verbatim
// (they are hashed on seed); result bodies are deliberately NOT stored -
// the advisory must not pretend to know the pre-crash output.
type crashMutatingCall struct {
	Name string    `json:"n"`
	Args string    `json:"a"`
	At   time.Time `json:"t"`
}

func crashSidecarPath(sessionID string) string {
	return filepath.Join(journalDir(), sessionID+"_mutating.jsonl")
}

// appendCrashSidecar records a successful mutating call for the current
// session. Fire-and-forget: any error only logs (the sidecar is a best-effort
// crash-recovery aid, never a correctness gate).
func (a *Agent) appendCrashSidecar(name, args string) {
	if a == nil || a.sessionID == "" || a.dedupLedger() == nil {
		return // kill switch off, or not yet bound to a session
	}
	if !isMutatingTool(name) {
		return
	}
	path := crashSidecarPath(a.sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	line, err := json.Marshal(crashMutatingCall{Name: name, Args: args, At: time.Now()})
	if err != nil {
		return
	}
	if info, err := os.Stat(path); err == nil && info.Size() > crashSidecarMaxBytes {
		trimCrashSidecar(path)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		debug.Log("tool-dedup", "crash sidecar append failed: %v", err)
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// trimCrashSidecar rewrites an oversized sidecar keeping the newest half of
// its records (bounded growth without losing the recent window).
func trimCrashSidecar(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	half := lines[len(lines)/2:]
	kept := make([]byte, 0, len(data)/2)
	for _, l := range half {
		if l == "" {
			continue
		}
		var probe crashMutatingCall
		if json.Unmarshal([]byte(l), &probe) != nil {
			continue // torn tail line from a mid-write crash
		}
		kept = append(kept, []byte(l)...)
		kept = append(kept, '\n')
	}
	_ = os.WriteFile(path, kept, 0o644)
}

// seedCrashWindow loads surviving sidecar records into the ledger's crash
// table. Fingerprints here deliberately omit the workspace epoch: the new
// process starts at epoch 0 and must still recognize pre-crash calls.
func (l *toolDedupLedger) seedCrashWindow(calls []crashMutatingCall, now time.Time) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range calls {
		if now.Sub(c.At) > crashDedupTTL || c.At.IsZero() {
			continue
		}
		l.crashTable[crashFingerprint(c.Name, c.Args)] = c.At
	}
}

// crashFingerprint hashes name+args WITHOUT the workspace epoch so that a
// fresh process (epoch 0) still matches a pre-crash record.
func crashFingerprint(name, args string) string {
	h := sha256.Sum256([]byte(name + "\x00" + args))
	return hex.EncodeToString(h[:])
}

// SeedCrashDedup ingests the session's crash sidecar (if any) into the
// agent's ledger and removes the file. Returns the number of seeded calls.
// Callers: session-resume path after CheckCrashedRun detected a crash. Safe
// to call unconditionally - a missing sidecar (clean previous run) is a no-op.
func SeedCrashDedup(a *Agent, sessionID string) int {
	if a == nil || sessionID == "" {
		return 0
	}
	ledger := a.dedupLedger()
	if ledger == nil {
		return 0
	}
	path := crashSidecarPath(sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0 // no sidecar: previous run completed cleanly
	}
	defer os.Remove(path) // one-shot: the crash window opens exactly once
	now := time.Now()
	seeded := 0
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var c crashMutatingCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			continue // torn tail line - skip, keep the rest
		}
		if now.Sub(c.At) <= crashDedupTTL {
			seeded++
		}
	}
	before := len(ledger.crashTable)
	ledger.seedCrashWindow(parseCrashSidecar(data), now)
	debug.Log("tool-dedup", "crash-restore window seeded: %d calls (table %d->%d)", seeded, before, len(ledger.crashTable))
	return seeded
}

// parseCrashSidecar decodes all intact records, skipping torn lines.
func parseCrashSidecar(data []byte) []crashMutatingCall {
	var calls []crashMutatingCall
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var c crashMutatingCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			continue
		}
		calls = append(calls, c)
	}
	return calls
}
