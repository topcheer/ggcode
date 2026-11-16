package session

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// #5698: durable keyed session instructions — storage-layer pins.

func newTestStore5698(t *testing.T) *JSONLStore {
	t.Helper()
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	return store
}

func textMessage5698(role, text string) provider.Message {
	return provider.Message{Role: role, Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

func TestIssue5698InstructionRecordRoundTrip(t *testing.T) {
	store := newTestStore5698(t)
	user := InstructionActor{Kind: InstructionActorUser, Source: "test"}
	base := time.Now().UTC().Add(-time.Minute)

	events := []InstructionEvent{
		{Key: "style", Value: "run tests first", Actor: user, At: base},
		{Key: "style", Value: "run tests and lint first", Actor: user, At: base.Add(time.Second)}, // replace
		{Key: "docs", Value: "update CHANGELOG", Actor: user, At: base.Add(2 * time.Second)},
		{Key: "docs", Removed: true, Actor: user, At: base.Add(3 * time.Second)}, // removal
	}
	for _, ev := range events {
		if err := store.AppendInstructionEvent("sess-5698", ev); err != nil {
			t.Fatalf("AppendInstructionEvent: %v", err)
		}
	}

	ses, err := store.Load("sess-5698")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ses.KeyedInstructions) != 1 {
		t.Fatalf("want 1 live entry (replace + removal folded), got %d: %v", len(ses.KeyedInstructions), ses.KeyedInstructions)
	}
	got := ses.KeyedInstructions["style"]
	if got.Value != "run tests and lint first" {
		t.Errorf("replace semantics: last record must win, got %q", got.Value)
	}
	if got.Actor.Kind != InstructionActorUser || got.Actor.Source != "test" {
		t.Errorf("actor provenance not folded: %+v", got.Actor)
	}
	if got.UpdatedAt.Before(base) {
		t.Errorf("updated_at not folded: %v", got.UpdatedAt)
	}
	if _, still := ses.KeyedInstructions["docs"]; still {
		t.Errorf("removed key must not survive the fold")
	}
}

func TestIssue5698SurvivesRevertAndCheckpointCompaction(t *testing.T) {
	store := newTestStore5698(t)
	user := InstructionActor{Kind: InstructionActorUser, Source: "test"}
	ses := &Session{ID: "sess-5698c"}

	if err := store.AppendMessage(ses, textMessage5698("user", "hello")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := store.AppendInstructionEvent("sess-5698c", InstructionEvent{Key: "style", Value: "always cite issue numbers", Actor: user, At: time.Now().UTC()}); err != nil {
		t.Fatalf("AppendInstructionEvent: %v", err)
	}
	if err := store.AppendMessage(ses, textMessage5698("user", "second turn")); err != nil {
		t.Fatalf("AppendMessage 2: %v", err)
	}
	// Revert: drop the last user message (message_remove record path).
	if err := store.AppendUserMessageRemovalToDisk(ses); err != nil {
		t.Fatalf("AppendUserMessageRemovalToDisk: %v", err)
	}
	// Compaction: append a checkpoint record the way compaction does — the
	// instruction fold must be unaffected by the checkpoint replay path.
	if err := appendRecordLine(store.sessionPath("sess-5698c"), jsonlRecord{
		Type:                   "checkpoint",
		SessionID:              "sess-5698c",
		CheckpointSummaryMsgID: "sum-1",
		CheckpointLastMsgID:    "msg-1",
		CheckpointTokens:       10,
	}); err != nil {
		t.Fatalf("append checkpoint record: %v", err)
	}

	loaded, err := store.Load("sess-5698c")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry, ok := loaded.KeyedInstructions["style"]
	if !ok || entry.Value != "always cite issue numbers" {
		t.Fatalf("keyed instruction must survive revert + checkpoint compaction, got %+v (ok=%v)", entry, ok)
	}
}

func TestIssue5698ForwardCompatOldBinaryRewriteSafe(t *testing.T) {
	store := newTestStore5698(t)

	// Newer binary writes an instruction record that also carries unknown
	// extra fields from a future schema revision.
	path := store.sessionPath("sess-5698f")
	raw := "{\"type\":\"instruction\",\"session_id\":\"sess-5698f\",\"instruction\":{\"key\":\"style\",\"value\":\"v1\",\"actor\":{\"kind\":\"user\"},\"at\":\"2026-11-16T00:00:00Z\",\"future_field\":{\"nested\":[1,2,3]}},\"another_unknown\":true}"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString(raw + "\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	// Old-binary behavior: the line decodes cleanly (unknown fields ignored).
	var rec jsonlRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		t.Fatalf("old binary must tolerate unknown fields, got: %v", err)
	}
	if rec.Instruction == nil || rec.Instruction.Key != "style" {
		t.Fatalf("instruction payload must decode for current binaries: %+v", rec.Instruction)
	}

	// Old binary keeps appending its own records; nothing rewrites lines.
	ses := &Session{ID: "sess-5698f"}
	if err := store.AppendMessage(ses, textMessage5698("user", "old binary turn")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "future_field") {
		t.Errorf("append-only store must preserve unknown fields on old-binary rewrites; line lost")
	}

	loaded, err := store.Load("sess-5698f")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if entry, ok := loaded.KeyedInstructions["style"]; !ok || entry.Value != "v1" {
		t.Fatalf("instruction must survive old-binary traffic alongside it, got %+v (ok=%v)", entry, ok)
	}
}

func TestIssue5698ActorKindGatePins(t *testing.T) {
	allowed := map[InstructionActorKind]bool{
		InstructionActorUser:        true,
		InstructionActorParentAgent: true,
	}
	denied := []InstructionActorKind{
		InstructionActorSubagentOutput,
		InstructionActorMCPResult,
		InstructionActorToolResult,
		InstructionActorModel,
	}
	for k, want := range allowed {
		if got := k.AllowedToWriteInstructions(); got != want {
			t.Errorf("%s: allowed=%v, want %v", k, got, want)
		}
	}
	for _, k := range denied {
		if k.AllowedToWriteInstructions() {
			t.Errorf("%s must be denied by the write gate (persistent-injection-channel risk)", k)
		}
	}
}

func TestIssue5698InvalidSessionIDRejected(t *testing.T) {
	store := newTestStore5698(t)
	err := store.AppendInstructionEvent("../escape", InstructionEvent{Key: "k", Value: "v"})
	if err == nil {
		t.Fatalf("invalid session id must be rejected")
	}
}
