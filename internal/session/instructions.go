package session

import (
	"fmt"
	"time"
)

// Keyed session instructions (#5698): durable, keyed, per-session standing
// directives stored under the session.instructions.* namespace.
//
// Persistence model: every put/remove appends one immutable "instruction"
// record to the session JSONL. Loading folds the records last-wins per key,
// independent of message checkpoints/compaction, so entries survive revert,
// compaction and process restarts. The append-only log is also the durable
// audit trail: every write leaves a record carrying actor provenance.
//
// Forward compatibility: older binaries that do not know the "instruction"
// record type skip those lines when loading (unknown fields are ignored by
// encoding/json) and never rewrite existing lines, so their appends leave
// instruction records intact — rewrite-safe on unknown fields by design.
//
// Namespace isolation (ADR-0001): this store owns session.instructions.*
// only. The #5260 prospective intention store (internal/intentions) owns
// memory.intentions.*; neither layer reads or writes the other's keys.

// InstructionActorKind is the provenance class of a keyed-instruction
// writer. Only user and parent-agent provenance may mutate keyed
// instructions; every other kind is rejected before any storage mutation
// (persistent-injection-channel gate, #5698 security gate).
type InstructionActorKind string

const (
	// InstructionActorUser is the human user surface (e.g. the TUI
	// /instructions command).
	InstructionActorUser InstructionActorKind = "user"
	// InstructionActorParentAgent is the parent agent configuring a child
	// session it owns.
	InstructionActorParentAgent InstructionActorKind = "parent-agent"

	// Rejected provenance kinds. These exist so callers can label attempted
	// writes precisely; AppendInstructionEvent callers gate on
	// AllowedToWriteInstructions before persisting.

	// InstructionActorSubagentOutput is output produced by a subagent.
	InstructionActorSubagentOutput InstructionActorKind = "subagent-output"
	// InstructionActorMCPResult is text returned by an MCP server.
	InstructionActorMCPResult InstructionActorKind = "mcp-result"
	// InstructionActorToolResult is a generic tool result payload.
	InstructionActorToolResult InstructionActorKind = "tool-result"
	// InstructionActorModel is the model's own generated text.
	InstructionActorModel InstructionActorKind = "model"
)

// AllowedToWriteInstructions reports whether the provenance kind is on the
// keyed-instruction write allowlist (#5698 security gate).
func (k InstructionActorKind) AllowedToWriteInstructions() bool {
	return k == InstructionActorUser || k == InstructionActorParentAgent
}

// InstructionActor carries caller-path provenance for one write attempt.
type InstructionActor struct {
	Kind InstructionActorKind `json:"kind"`
	// Source names the concrete surface (e.g. "tui:/instructions"), so the
	// audit trail is testable end to end.
	Source string `json:"source,omitempty"`
}

// InstructionEvent is one append-only keyed-instruction mutation record.
type InstructionEvent struct {
	Key     string           `json:"key"`
	Value   string           `json:"value,omitempty"`
	Removed bool             `json:"removed,omitempty"`
	Actor   InstructionActor `json:"actor"`
	At      time.Time        `json:"at"`
}

// KeyedInstruction is the folded runtime view of one live entry.
type KeyedInstruction struct {
	Key       string           `json:"key"`
	Value     string           `json:"value"`
	UpdatedAt time.Time        `json:"updated_at"`
	Actor     InstructionActor `json:"actor"`
}

// InstructionStore is the narrow persistence seam for keyed instructions.
// *JSONLStore implements it; the agent layer depends only on this interface.
type InstructionStore interface {
	AppendInstructionEvent(sessionID string, ev InstructionEvent) error
}

// AppendInstructionEvent appends one keyed-instruction mutation record to the
// session JSONL. The event timestamp is normalized to UTC when unset.
func (s *JSONLStore) AppendInstructionEvent(sessionID string, ev InstructionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validSessionID(sessionID) {
		return fmt.Errorf("session: invalid session id for instruction event")
	}
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	rec := jsonlRecord{
		Type:        "instruction",
		SessionID:   sessionID,
		Instruction: &ev,
	}
	return appendRecordLine(s.sessionPath(sessionID), rec)
}

// foldInstructionRecords applies instruction records to the runtime session
// state, last record per key wins (#5698 compaction/revert survival: the
// fold is independent of the message/checkpoint replay paths).
func foldInstructionRecords(ses *Session, recs []jsonlRecord) {
	for _, rec := range recs {
		if rec.Instruction == nil {
			continue
		}
		ev := rec.Instruction
		if ev.Removed {
			delete(ses.KeyedInstructions, ev.Key)
			continue
		}
		if ses.KeyedInstructions == nil {
			ses.KeyedInstructions = make(map[string]KeyedInstruction)
		}
		ses.KeyedInstructions[ev.Key] = KeyedInstruction{
			Key:       ev.Key,
			Value:     ev.Value,
			UpdatedAt: ev.At,
			Actor:     ev.Actor,
		}
	}
}
