package agent

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/session"
)

// Keyed session instructions (#5698): durable, cache-friendly,
// compaction-surviving standing directives keyed per session.
//
// Storage lives in the session JSONL (session.instructions.* records, see
// internal/session/instructions.go). This file owns the agent-side halves:
//
//   - the write gate (security): only user / parent-agent provenance may
//     mutate entries; subagent output, MCP results, tool results and model
//     text are rejected BEFORE any storage mutation, and every attempt —
//     accepted or rejected — leaves an audit line;
//   - replace semantics (extends the in-process name-replace of
//     AddSystemPromptLayer at agent.go): same key overwrites, per-value
//     ValueTooLarge bound, bounded entry count;
//   - rendering: entries render as stable keyed blocks (sorted by key) so
//     only a changed entry's block rewrites — prefix cache stays valid for
//     untouched blocks;
//   - the step-boundary announce: put/remove prepend exactly one delta line
//     inside the changed entry's own block at the next system-prompt
//     assembly (option (a), self-describing). The announce bit is consumed
//     when the carrying prompt is committed for shipment: no new
//     user/assistant message, no extra model turn, no inbox wake.

const (
	// MaxKeyedInstructionKeyRunes bounds a key's length.
	MaxKeyedInstructionKeyRunes = 64
	// MaxKeyedInstructionValueBytes is the per-value ValueTooLarge bound.
	MaxKeyedInstructionValueBytes = 16 * 1024
	// MaxKeyedInstructionEntries bounds the total entry count per session.
	MaxKeyedInstructionEntries = 64
	// keyedInstructionAuditCap bounds the in-memory audit ring.
	keyedInstructionAuditCap = 64
)

// ErrKeyedInstructionValueTooLarge is the ValueTooLarge bound error (#5698).
var ErrKeyedInstructionValueTooLarge = errors.New("ValueTooLarge: keyed instruction value exceeds 16KiB bound")

// ErrKeyedInstructionWriteDenied is the security-gate rejection error.
var ErrKeyedInstructionWriteDenied = errors.New("keyed instruction write denied: only user/parent-agent provenance may write (persistent-injection-channel gate, #5698)")

// ErrKeyedInstructionNoSink is returned when no durable sink is attached.
var ErrKeyedInstructionNoSink = errors.New("keyed instruction write refused: no durable session sink attached")

// KeyedInstructionSink is the narrow persistence seam implemented by the
// session store (*session.JSONLStore via session.InstructionStore).
type KeyedInstructionSink interface {
	AppendInstructionEvent(sessionID string, ev session.InstructionEvent) error
}

// KeyedInstructionAuditLine is one audit trail entry. Every write attempt —
// accepted put, no-op put, remove, unknown-key no-op remove, and rejected
// attempts — leaves exactly one line.
type KeyedInstructionAuditLine struct {
	At     time.Time
	Op     string // put | put-noop | remove | remove-noop | rejected | failed
	Key    string
	Actor  session.InstructionActor
	Detail string
}

type keyedInstructionEntry struct {
	value     string
	updatedAt time.Time
	actor     session.InstructionActor
	// announcePending marks a not-yet-emitted one-line delta; the bit is
	// consumed when the carrying system prompt ships (step boundary).
	announcePending bool
	// announceOp is "put" while the entry is live, or "remove" for a
	// tombstone awaiting its announce emission.
	announceOp string
}

// AttachKeyedInstructions hydrates the durable keyed-instruction state for
// sessionID and wires the persistence sink. A nil sink keeps the layer
// read-only (renders hydrated entries; writes are refused loudly instead of
// silently losing durability). Re-attaching replaces the whole state — used
// on session switch.
func (a *Agent) AttachKeyedInstructions(sessionID string, entries map[string]session.KeyedInstruction, sink KeyedInstructionSink) {
	a.keyedInstrMu.Lock()
	defer a.keyedInstrMu.Unlock()
	a.keyedInstrSessionID = sessionID
	a.keyedInstrSink = sink
	a.keyedInstr = make(map[string]keyedInstructionEntry, len(entries))
	for k, e := range entries {
		a.keyedInstr[k] = keyedInstructionEntry{
			value:      e.Value,
			updatedAt:  e.UpdatedAt,
			actor:      e.Actor,
			announceOp: "put",
		}
	}
	if len(entries) > 0 {
		debug.Log("session-instructions", "attached %d keyed instruction(s) for session %s (durable=%v)", len(entries), sessionID, sink != nil)
	}
}

func normalizeKeyedInstructionKey(key string) (string, error) {
	k := strings.TrimSpace(key)
	if k == "" {
		return "", errors.New("keyed instruction key must not be empty")
	}
	if r := []rune(k); len(r) > MaxKeyedInstructionKeyRunes {
		return "", fmt.Errorf("keyed instruction key exceeds %d runes", MaxKeyedInstructionKeyRunes)
	}
	for _, c := range k {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '/'
		if !ok {
			return "", fmt.Errorf("keyed instruction key %q contains disallowed character %q (allowed: letters, digits, - _ . /)", k, string(c))
		}
	}
	return k, nil
}

func (a *Agent) recordKeyedInstructionAuditLocked(op, key string, actor session.InstructionActor, detail string) {
	line := KeyedInstructionAuditLine{
		At:     time.Now().UTC(),
		Op:     op,
		Key:    key,
		Actor:  actor,
		Detail: detail,
	}
	a.keyedInstrAudit = append(a.keyedInstrAudit, line)
	if len(a.keyedInstrAudit) > keyedInstructionAuditCap {
		a.keyedInstrAudit = append([]KeyedInstructionAuditLine(nil), a.keyedInstrAudit[len(a.keyedInstrAudit)-keyedInstructionAuditCap:]...)
	}
	debug.Log("session-instructions", "audit op=%s key=%q actor=%s/%s %s", op, key, actor.Kind, actor.Source, detail)
}

// PutKeyedInstruction writes (or replaces) one keyed instruction. Replace
// semantics extend the in-process name-replace of AddSystemPromptLayer: the
// same key overwrites in place. An identical-value put refreshes provenance
// but does not announce (nothing changed for the model to see).
func (a *Agent) PutKeyedInstruction(key, value string, actor session.InstructionActor) error {
	a.keyedInstrMu.Lock()
	defer a.keyedInstrMu.Unlock()

	if !actor.Kind.AllowedToWriteInstructions() {
		a.recordKeyedInstructionAuditLocked("rejected", key, actor, "put denied: provenance not on user/parent-agent allowlist")
		debug.Log("session-instructions", "SECURITY: rejected keyed-instruction put key=%q actor=%s — persistent-injection-channel gate", key, actor.Kind)
		return ErrKeyedInstructionWriteDenied
	}
	k, err := normalizeKeyedInstructionKey(key)
	if err != nil {
		a.recordKeyedInstructionAuditLocked("rejected", key, actor, err.Error())
		return err
	}
	if len(value) > MaxKeyedInstructionValueBytes {
		a.recordKeyedInstructionAuditLocked("rejected", k, actor, fmt.Sprintf("ValueTooLarge: %d bytes > %d bound", len(value), MaxKeyedInstructionValueBytes))
		return ErrKeyedInstructionValueTooLarge
	}
	if a.keyedInstrSink == nil {
		a.recordKeyedInstructionAuditLocked("failed", k, actor, "no durable sink attached")
		return ErrKeyedInstructionNoSink
	}
	if _, exists := a.keyedInstr[k]; !exists && len(a.keyedInstr) >= MaxKeyedInstructionEntries {
		a.recordKeyedInstructionAuditLocked("rejected", k, actor, fmt.Sprintf("entry count bound %d reached", MaxKeyedInstructionEntries))
		return fmt.Errorf("keyed instruction entry bound reached (%d)", MaxKeyedInstructionEntries)
	}

	prev, existed := a.keyedInstr[k]
	ev := session.InstructionEvent{Key: k, Value: value, Actor: actor, At: time.Now().UTC()}
	if err := a.keyedInstrSink.AppendInstructionEvent(a.keyedInstrSessionID, ev); err != nil {
		a.recordKeyedInstructionAuditLocked("failed", k, actor, "sink append: "+err.Error())
		return fmt.Errorf("persisting keyed instruction: %w", err)
	}

	changed := !existed || prev.value != value
	entry := keyedInstructionEntry{value: value, updatedAt: ev.At, actor: actor, announceOp: "put"}
	if changed {
		entry.announcePending = true
		a.recordKeyedInstructionAuditLocked("put", k, actor, fmt.Sprintf("value set (%d bytes, replaced=%v)", len(value), existed))
	} else {
		entry.announcePending = prev.announcePending
		entry.announceOp = prev.announceOp
		a.recordKeyedInstructionAuditLocked("put-noop", k, actor, "value unchanged; provenance refreshed, no announce")
	}
	a.keyedInstr[k] = entry
	return nil
}

// RemoveKeyedInstruction removes one keyed instruction. Removing an unknown
// key is an audited no-op, not an error (multi-writer race safety).
func (a *Agent) RemoveKeyedInstruction(key string, actor session.InstructionActor) error {
	a.keyedInstrMu.Lock()
	defer a.keyedInstrMu.Unlock()

	if !actor.Kind.AllowedToWriteInstructions() {
		a.recordKeyedInstructionAuditLocked("rejected", key, actor, "remove denied: provenance not on user/parent-agent allowlist")
		debug.Log("session-instructions", "SECURITY: rejected keyed-instruction remove key=%q actor=%s — persistent-injection-channel gate", key, actor.Kind)
		return ErrKeyedInstructionWriteDenied
	}
	k, err := normalizeKeyedInstructionKey(key)
	if err != nil {
		a.recordKeyedInstructionAuditLocked("rejected", key, actor, err.Error())
		return err
	}
	if _, exists := a.keyedInstr[k]; !exists {
		a.recordKeyedInstructionAuditLocked("remove-noop", k, actor, "unknown key; nothing removed")
		return nil
	}
	if a.keyedInstrSink == nil {
		a.recordKeyedInstructionAuditLocked("failed", k, actor, "no durable sink attached")
		return ErrKeyedInstructionNoSink
	}
	ev := session.InstructionEvent{Key: k, Removed: true, Actor: actor, At: time.Now().UTC()}
	if err := a.keyedInstrSink.AppendInstructionEvent(a.keyedInstrSessionID, ev); err != nil {
		a.recordKeyedInstructionAuditLocked("failed", k, actor, "sink append: "+err.Error())
		return fmt.Errorf("persisting keyed instruction removal: %w", err)
	}
	// Tombstone: the entry keeps only its announce so the removal delta line
	// renders once at the next step boundary, then disappears.
	a.keyedInstr[k] = keyedInstructionEntry{updatedAt: ev.At, actor: actor, announcePending: true, announceOp: "remove"}
	a.recordKeyedInstructionAuditLocked("remove", k, actor, "value removed")
	return nil
}

// KeyedInstructions returns a sorted-by-key snapshot of the live entries.
func (a *Agent) KeyedInstructions() []session.KeyedInstruction {
	a.keyedInstrMu.Lock()
	defer a.keyedInstrMu.Unlock()
	out := make([]session.KeyedInstruction, 0, len(a.keyedInstr))
	for k, e := range a.keyedInstr {
		if e.announceOp == "remove" {
			continue
		}
		out = append(out, session.KeyedInstruction{Key: k, Value: e.value, UpdatedAt: e.updatedAt, Actor: e.actor})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// KeyedInstructionAuditLines returns the in-memory audit ring (bounded,
// newest last). The durable audit trail is the session JSONL record log.
func (a *Agent) KeyedInstructionAuditLines() []KeyedInstructionAuditLine {
	a.keyedInstrMu.Lock()
	defer a.keyedInstrMu.Unlock()
	return append([]KeyedInstructionAuditLine(nil), a.keyedInstrAudit...)
}

// renderKeyedInstructionBlocks renders the stable keyed blocks section and
// reports which keys carried a not-yet-consumed announce line. Blocks are
// emitted sorted by key inside one wrapper so the section is byte-stable
// across renders unless an entry actually changed.
func (a *Agent) renderKeyedInstructionBlocks() (string, []string) {
	a.keyedInstrMu.Lock()
	defer a.keyedInstrMu.Unlock()
	if len(a.keyedInstr) == 0 {
		return "", nil
	}
	keys := make([]string, 0, len(a.keyedInstr))
	for k := range a.keyedInstr {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var announced []string
	var b strings.Builder
	b.WriteString("<session-instructions>\n")
	for _, k := range keys {
		e := a.keyedInstr[k]
		if e.announceOp == "remove" {
			if e.announcePending {
				fmt.Fprintf(&b, "[session-instruction removed: key %q by %s]\n", k, e.actor.Kind)
				announced = append(announced, k)
			}
			continue
		}
		fmt.Fprintf(&b, "<instruction key=%q>\n", k)
		if e.announcePending {
			fmt.Fprintf(&b, "[session-instruction changed: key %q set by %s]\n", k, e.actor.Kind)
			announced = append(announced, k)
		}
		if v := strings.TrimRight(e.value, "\n"); v != "" {
			b.WriteString(v)
			b.WriteString("\n")
		}
		b.WriteString("</instruction>\n")
	}
	b.WriteString("</session-instructions>")
	return b.String(), announced
}

// consumeKeyedInstructionAnnounce clears announce bits after the carrying
// system prompt has been committed for shipment (step boundary emission —
// exactly one announce line per change).
func (a *Agent) consumeKeyedInstructionAnnounce(keys []string) {
	if len(keys) == 0 {
		return
	}
	a.keyedInstrMu.Lock()
	defer a.keyedInstrMu.Unlock()
	for _, k := range keys {
		e, ok := a.keyedInstr[k]
		if !ok || !e.announcePending {
			continue
		}
		e.announcePending = false
		if e.announceOp == "remove" {
			delete(a.keyedInstr, k)
			continue
		}
		e.announceOp = "put"
		a.keyedInstr[k] = e
	}
}
