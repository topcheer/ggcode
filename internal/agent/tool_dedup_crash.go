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

const (
	crashDedupTTL        = 30 * time.Minute
	crashSidecarMaxBytes = 64 * 1024
)

type crashMutatingCall struct {
	Name string    `json:"n"`
	Args string    `json:"a"`
	At   time.Time `json:"t"`
}

func crashSidecarPath(sessionID string) string {
	return filepath.Join(journalDir(), sessionID+"_mutating.jsonl")
}

func (a *Agent) appendCrashSidecar(name, args string) {
	if a == nil || a.sessionID == "" || a.dedupLedger() == nil {
		return
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
			continue
		}
		kept = append(kept, []byte(l)...)
		kept = append(kept, '\n')
	}
	_ = os.WriteFile(path, kept, 0o644)
}

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

func crashFingerprint(name, args string) string {
	h := sha256.Sum256([]byte(name + "\x00" + args))
	return hex.EncodeToString(h[:])
}

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
		return 0
	}
	defer os.Remove(path)
	now := time.Now()
	seeded := 0
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var c crashMutatingCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			continue
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
