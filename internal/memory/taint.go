package memory

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// InjectionPatterns are case-insensitive patterns that strongly indicate an
// attempted prompt injection. Each is a substring match (lowercased).
// The list is deliberately high-precision: a single match taints the whole
// entry (r409) or wraps a whole tool result (agent guard), so every entry
// must be rare in legitimate code and documentation.
//
// r409: moved from internal/agent/prompt_injection_guard.go so that BOTH the
// agent-side result-wrapping guard and the memory-layer poisoning defense
// (write-time taint marking) share one source of truth. MINJA
// (arXiv 2601.05504) showed query-only memory poisoning reaches 95%+
// injection success; sleeper poisoning (arXiv 2605.15338) stays dormant
// across sessions. A poisoned memory entry that is inlined raw into the
// system prompt at every startup is a persistent, unreviewed injection
// channel - this file closes the persistence leg of that attack.
var InjectionPatterns = []string{
	"ignore all previous instructions",
	"ignore previous instructions",
	"ignore your instructions",
	"ignore the above instructions",
	"disregard all previous",
	"disregard your instructions",
	// #937: anchored to imperative-verb phrases only. The bare "you are now
	// in" flagged any narrative text as an injection attack.
	"you are now in a",
	"you are now in maint",
	"you are now in developer",
	"you are now in chat",
	"new instructions:",
	"updated instructions:",
	"override your system prompt",
	"override system prompt",
	"override your instructions",
	"forget all previous",
	"forget your instructions",
	"do not follow your instructions",
	"stop following your rules",
	"[system]",
	"[system prompt]",
	"system: you are now",
	"system: ignore",
	// #937: newline-anchored - bare "### system" matched every '### System
	// Requirements' heading in technical docs.
	"### system\n",
	"## system\n",
	"important: ignore",
	"override your previous",
	"act as if you have no",
	"pretend you have no instructions",
	// Chat-template role markers: a near-zero-false-positive class.
	"<|system|>",
	"<|im_start|>system",
	"<|im_start|>user",
	"<|im_start|>assistant",
	// Exfiltration directives: imperative verb + specific object.
	"send the contents to",
	"upload the file to",
	"post this data to",
	"transfer the contents",
}

// DetectInjectionTaint returns the first pattern matched (lowercased
// substring) by key or content, or "" when the pair looks clean.
func DetectInjectionTaint(key, content string) string {
	lowered := strings.ToLower(key + "\n" + content)
	for _, pattern := range InjectionPatterns {
		if strings.Contains(lowered, pattern) {
			return pattern
		}
	}
	return ""
}

// taintSidecarSuffix names the sidecar marking a memory file as tainted.
// Kept separate from .usage.json so a corrupt quarantine marker can never
// take the usage/provenance telemetry down with it.
const taintSidecarSuffix = ".taint"

// writeMuTaint serializes taint-sidecar writes per path (same pattern as the
// memory .md writeMu in auto.go).
var writeMuTaint sync.Map // path -> *sync.Mutex

// MarkTainted records the quarantine sidecar for a memory key (safeKey is the
// sanitized filename, as produced by disambiguateKey(sanitizeKey(k))).
// The .md itself is written verbatim - tainting never blocks a save (a
// legitimate security-writeup memory must still be persistable), it only
// stops the entry from being auto-inlined into future system prompts.
func (am *AutoMemory) MarkTainted(safeKey, pattern string) error {
	path := filepath.Join(am.dir, safeKey+taintSidecarSuffix)
	if unlock, err := util.FileLock(am.dir + ".lock"); err == nil {
		defer unlock()
	} else {
		debug.Log("memory", "taint sidecar filelock failed, degraded: %v", err)
	}
	payload := time.Now().UTC().Format(time.RFC3339) + "\n" + pattern + "\n"
	tmp, err := os.CreateTemp(am.dir, safeKey+".taint.tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(payload); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	muAny, _ := writeMuTaint.LoadOrStore(path, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	debug.Log("memory", "entry %q tainted (pattern %q) - demoted to index-only", safeKey, pattern)
	return nil
}

// ClearTaint removes the sidecar; used when a key is re-saved with clean
// content so a past flag does not shadow a fixed entry forever.
func (am *AutoMemory) ClearTaint(safeKey string) {
	path := filepath.Join(am.dir, safeKey+taintSidecarSuffix)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		debug.Log("memory", "clear taint sidecar %s: %v", path, err)
	}
}

// TaintOf reports whether the entry is tainted and which pattern flagged it.
func (am *AutoMemory) TaintOf(safeKey string) (pattern string, tainted bool) {
	data, err := os.ReadFile(filepath.Join(am.dir, safeKey+taintSidecarSuffix))
	if err != nil {
		return "", false
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) >= 2 {
		return lines[1], true
	}
	// Malformed sidecar (single line / empty): still treat as tainted - the
	// quarantine decision must fail closed, only the pattern label is lost.
	return "(unlabeled)", true
}
