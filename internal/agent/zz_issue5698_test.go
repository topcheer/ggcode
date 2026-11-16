package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
)

// #5698: durable keyed session instructions — agent-layer pins (gate,
// bounds, replace, announce idempotency, render stability, survival).

type fakeKeyedSink5698 struct {
	events []session.InstructionEvent
	fail   error
}

func (f *fakeKeyedSink5698) AppendInstructionEvent(sessionID string, ev session.InstructionEvent) error {
	if f.fail != nil {
		return f.fail
	}
	f.events = append(f.events, ev)
	return nil
}

func newAgentWithSink5698(t *testing.T) (*Agent, *fakeKeyedSink5698) {
	t.Helper()
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "ok"}}},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "base prompt", 5)
	sink := &fakeKeyedSink5698{}
	a.AttachKeyedInstructions("sess-a", nil, sink)
	return a, sink
}

func userActor5698() session.InstructionActor {
	return session.InstructionActor{Kind: session.InstructionActorUser, Source: "test"}
}

func countAnnounce5698(s string) int {
	return strings.Count(s, "[session-instruction changed:")
}

func TestIssue5698PutRenderStableSortedBlocks(t *testing.T) {
	a, sink := newAgentWithSink5698(t)
	if err := a.PutKeyedInstruction("zeta", "value-z", userActor5698()); err != nil {
		t.Fatalf("put zeta: %v", err)
	}
	if err := a.PutKeyedInstruction("alpha", "value-a", userActor5698()); err != nil {
		t.Fatalf("put alpha: %v", err)
	}
	if len(sink.events) != 2 {
		t.Fatalf("sink must record 2 events, got %d", len(sink.events))
	}

	block1, announced := a.renderKeyedInstructionBlocks()
	if !strings.Contains(block1, "<session-instructions>") || !strings.Contains(block1, "</session-instructions>") {
		t.Fatalf("stable wrapper framing missing: %q", block1)
	}
	if strings.Index(block1, "alpha") > strings.Index(block1, "zeta") {
		t.Errorf("blocks must render sorted by key for byte-stability")
	}
	if !strings.Contains(block1, "<instruction key=\"alpha\">") || !strings.Contains(block1, "<instruction key=\"zeta\">") {
		t.Errorf("keyed block framing missing: %q", block1)
	}
	if len(announced) != 2 {
		t.Errorf("both changed keys must carry announce bits, got %v", announced)
	}

	a.consumeKeyedInstructionAnnounce(announced)
	block2, announced2 := a.renderKeyedInstructionBlocks()
	if countAnnounce5698(block2) != 0 || len(announced2) != 0 {
		t.Errorf("announce must be consumed on emission: %q announced=%v", block2, announced2)
	}
	block3, _ := a.renderKeyedInstructionBlocks()
	if block2 != block3 {
		t.Errorf("post-consumption render must be byte-stable (cache-friendly)")
	}
}

func TestIssue5698AnnounceAtStepBoundaryExactlyOnce(t *testing.T) {
	a, _ := newAgentWithSink5698(t)
	if err := a.PutKeyedInstruction("style", "always cite issue numbers", userActor5698()); err != nil {
		t.Fatalf("put: %v", err)
	}

	cm, ok := a.contextManager.(*ctxpkg.Manager)
	if !ok {
		t.Fatalf("expected *context.Manager")
	}

	// Step boundary #1: system prompt assembly ships with exactly one
	// announce line inside the changed entry's block.
	a.maybeInjectDynamicSystemPrompt("first turn")
	sysText := systemText5698(cm.Messages())
	if countAnnounce5698(sysText) != 1 {
		t.Fatalf("exactly one announce line per change expected, got %d in %q", countAnnounce5698(sysText), sysText)
	}
	if !strings.Contains(sysText, "[session-instruction changed: key \"style\" set by user]") {
		t.Errorf("announce line must be self-describing (key + actor), got %q", sysText)
	}
	if !strings.Contains(sysText, "always cite issue numbers") {
		t.Errorf("value block must ride the same assembly, got %q", sysText)
	}
	// No new user/assistant message was minted by the announce path.
	for _, m := range cm.Messages() {
		if m.Role == "user" || m.Role == "assistant" {
			t.Fatalf("announce must not add user/assistant messages; found %q", m.Role)
		}
	}

	// Step boundary #2: announce consumed; block remains, no announce line.
	a.maybeInjectDynamicSystemPrompt("second turn")
	sysText2 := systemText5698(cm.Messages())
	if countAnnounce5698(sysText2) != 0 {
		t.Fatalf("announce must fire exactly once (idempotent), found again: %q", sysText2)
	}
	if !strings.Contains(sysText2, "always cite issue numbers") {
		t.Errorf("durable block must persist after announce consumption: %q", sysText2)
	}

	// Step boundary #3: unchanged state renders the identical prompt (early
	// return on lastInjectedSystemPrompt — cache-friendly no-op).
	a.maybeInjectDynamicSystemPrompt("third turn")
	if got := a.lastInjectedSystemPrompt; got == "" || countAnnounce5698(got) != 0 {
		t.Errorf("stable prompt must remain announce-free: %q", got)
	}
}

func systemText5698(msgs []provider.Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		if m.Role == "system" {
			for _, b := range m.Content {
				sb.WriteString(b.Text)
				sb.WriteString("\n---BLOCK---\n")
			}
		}
	}
	return sb.String()
}

func TestIssue5698ReplaceSemanticsAndIdenticalNoop(t *testing.T) {
	a, sink := newAgentWithSink5698(t)
	if err := a.PutKeyedInstruction("style", "v1", userActor5698()); err != nil {
		t.Fatalf("put v1: %v", err)
	}
	if err := a.PutKeyedInstruction("style", "v2", userActor5698()); err != nil {
		t.Fatalf("put v2 (replace): %v", err)
	}
	entries := a.KeyedInstructions()
	if len(entries) != 1 || entries[0].Value != "v2" {
		t.Fatalf("replace semantics: want single entry v2, got %+v", entries)
	}
	block, announced := a.renderKeyedInstructionBlocks()
	if strings.Count(block, "<instruction key=") != 1 {
		t.Errorf("replaced key must render exactly one block: %q", block)
	}
	if len(announced) != 1 {
		t.Errorf("one announce bit for the changed entry, got %v", announced)
	}
	a.consumeKeyedInstructionAnnounce(announced)

	// Identical-value put: provenance refresh, no announce, audited.
	if err := a.PutKeyedInstruction("style", "v2", userActor5698()); err != nil {
		t.Fatalf("identical put: %v", err)
	}
	if _, announced := a.renderKeyedInstructionBlocks(); len(announced) != 0 {
		t.Errorf("identical-value put must not announce, got %v", announced)
	}
	ops := auditOps5698(a)
	if !contains5698(ops, "put-noop") {
		t.Errorf("identical-value put must leave a put-noop audit line, got %v", ops)
	}
	if len(sink.events) != 3 {
		t.Errorf("all three puts persist events (audit trail), got %d", len(sink.events))
	}
}

func TestIssue5698ValueTooLargeBound(t *testing.T) {
	a, sink := newAgentWithSink5698(t)
	big := strings.Repeat("x", MaxKeyedInstructionValueBytes+1)
	err := a.PutKeyedInstruction("big", big, userActor5698())
	if !errors.Is(err, ErrKeyedInstructionValueTooLarge) {
		t.Fatalf("want ErrKeyedInstructionValueTooLarge, got %v", err)
	}
	if len(a.KeyedInstructions()) != 0 || len(sink.events) != 0 {
		t.Fatalf("rejected write must mutate nothing: entries=%d events=%d", len(a.KeyedInstructions()), len(sink.events))
	}
	if !contains5698(auditOps5698(a), "rejected") {
		t.Errorf("rejected write must leave an audit line")
	}
	// At-bound value is accepted.
	if err := a.PutKeyedInstruction("big", strings.Repeat("x", MaxKeyedInstructionValueBytes), userActor5698()); err != nil {
		t.Fatalf("at-bound value must pass: %v", err)
	}
}

func TestIssue5698SecurityGateRejectsUntrustedProvenance(t *testing.T) {
	a, sink := newAgentWithSink5698(t)
	if err := a.PutKeyedInstruction("seed", "seed-value", userActor5698()); err != nil {
		t.Fatalf("seed put: %v", err)
	}
	baseline := len(sink.events)

	denied := []session.InstructionActor{
		{Kind: session.InstructionActorSubagentOutput, Source: "subagent-42"},
		{Kind: session.InstructionActorMCPResult, Source: "mcp:evil-server"},
		{Kind: session.InstructionActorToolResult, Source: "tool:run_command"},
		{Kind: session.InstructionActorModel, Source: "assistant-text"},
	}
	for i, actor := range denied {
		if err := a.PutKeyedInstruction("injected", "persistent payload", actor); !errors.Is(err, ErrKeyedInstructionWriteDenied) {
			t.Fatalf("denied[%d] put: want ErrKeyedInstructionWriteDenied, got %v", i, err)
		}
		if err := a.RemoveKeyedInstruction("seed", actor); !errors.Is(err, ErrKeyedInstructionWriteDenied) {
			t.Fatalf("denied[%d] remove: want ErrKeyedInstructionWriteDenied, got %v", i, err)
		}
	}
	if len(sink.events) != baseline {
		t.Fatalf("security gate must reject BEFORE any storage mutation: events went %d -> %d", baseline, len(sink.events))
	}
	if entries := a.KeyedInstructions(); len(entries) != 1 || entries[0].Key != "seed" {
		t.Fatalf("in-memory state must be untouched by denied writes: %+v", entries)
	}
	audits := a.KeyedInstructionAuditLines()
	rejected := 0
	for _, l := range audits {
		if l.Op == "rejected" {
			rejected++
			if l.Actor.Kind == "" {
				t.Errorf("audit line must carry provenance: %+v", l)
			}
		}
	}
	if rejected != 2*len(denied) {
		t.Errorf("every denied attempt must leave an audit line, want %d got %d", 2*len(denied), rejected)
	}
	// Parent-agent provenance is on the allowlist.
	if err := a.PutKeyedInstruction("child-config", "be terse", session.InstructionActor{Kind: session.InstructionActorParentAgent, Source: "spawn"}); err != nil {
		t.Fatalf("parent-agent put must be allowed: %v", err)
	}
}

func TestIssue5698UnknownKeyRemoveIsAuditedNoop(t *testing.T) {
	a, sink := newAgentWithSink5698(t)
	if err := a.RemoveKeyedInstruction("ghost", userActor5698()); err != nil {
		t.Fatalf("unknown-key remove must be a no-op, not an error: %v", err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("unknown-key remove must not persist an event, got %d", len(sink.events))
	}
	if !contains5698(auditOps5698(a), "remove-noop") {
		t.Errorf("unknown-key remove must leave a remove-noop audit line, got %v", auditOps5698(a))
	}
}

func TestIssue5698RemovalTombstoneAnnouncesOnce(t *testing.T) {
	a, _ := newAgentWithSink5698(t)
	if err := a.PutKeyedInstruction("old", "v", userActor5698()); err != nil {
		t.Fatalf("put: %v", err)
	}
	block, announced := a.renderKeyedInstructionBlocks()
	a.consumeKeyedInstructionAnnounce(announced)
	_ = block

	if err := a.RemoveKeyedInstruction("old", userActor5698()); err != nil {
		t.Fatalf("remove: %v", err)
	}
	block2, announced2 := a.renderKeyedInstructionBlocks()
	if !strings.Contains(block2, "[session-instruction removed: key \"old\" by user]") {
		t.Errorf("removal must announce a tombstone line, got %q", block2)
	}
	if strings.Contains(block2, "<instruction key=\"old\">") {
		t.Errorf("removed entry must not render a value block: %q", block2)
	}
	if len(announced2) != 1 {
		t.Fatalf("tombstone announce bit expected, got %v", announced2)
	}
	a.consumeKeyedInstructionAnnounce(announced2)
	block3, announced3 := a.renderKeyedInstructionBlocks()
	if block3 != "" || len(announced3) != 0 {
		t.Errorf("consumed tombstone must vanish entirely, got %q announced=%v", block3, announced3)
	}
	if entries := a.KeyedInstructions(); len(entries) != 0 {
		t.Errorf("no live entries expected after removal, got %+v", entries)
	}
}

func TestIssue5698CompactionSurvivalViaRealStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "ok"}}},
		},
	}
	a1 := NewAgent(mp, tool.NewRegistry(), "base prompt", 5)
	a1.AttachKeyedInstructions("sess-rt", nil, store)
	if err := a1.PutKeyedInstruction("style", "always cite issue numbers", userActor5698()); err != nil {
		t.Fatalf("put via real store: %v", err)
	}

	// "Restart": fresh agent hydrated from the folded session state —
	// compaction/revert cannot touch the instruction records.
	loaded, err := store.Load("sess-rt")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a2 := NewAgent(mp, tool.NewRegistry(), "base prompt", 5)
	a2.AttachKeyedInstructions("sess-rt", loaded.KeyedInstructions, store)
	block, announced := a2.renderKeyedInstructionBlocks()
	if !strings.Contains(block, "always cite issue numbers") {
		t.Fatalf("entry must survive restart via session storage, got %q", block)
	}
	// Hydrated entries never re-announce (announce bits are in-memory).
	if len(announced) != 0 || countAnnounce5698(block) != 0 {
		t.Errorf("restart must not re-announce, got %q announced=%v", block, announced)
	}
	if got := strings.Count(block, "<instruction key="); got != 1 {
		t.Errorf("exactly one keyed block expected, got %d", got)
	}
}

func TestIssue5698NoSinkRefusesWrite(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "ok"}}},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "base prompt", 5)
	a.AttachKeyedInstructions("sess-nosink", nil, nil)
	if err := a.PutKeyedInstruction("k", "v", userActor5698()); err == nil {
		t.Fatalf("write without a durable sink must be refused, not silently dropped")
	}
}

func TestIssue5698KeyValidation(t *testing.T) {
	a, _ := newAgentWithSink5698(t)
	bad := []string{"", "  ", "has space", strings.Repeat("k", MaxKeyedInstructionKeyRunes+1), "quote\""}
	for i, k := range bad {
		if err := a.PutKeyedInstruction(k, "v", userActor5698()); err == nil {
			t.Errorf("bad key[%d] %q must be rejected", i, k)
		}
	}
	if err := a.PutKeyedInstruction(" ns-trimmed ", "v", userActor5698()); err != nil {
		t.Fatalf("trimmed key must be accepted: %v", err)
	}
	if entries := a.KeyedInstructions(); len(entries) != 1 || entries[0].Key != "ns-trimmed" {
		t.Fatalf("key must be trimmed+normalized, got %+v", entries)
	}
}

// TestIssue5698WiringAnchors pins the integration points (repo
// regex-on-source convention): render + consume in the prompt assembly,
// hydration in the TUI session switch, and the /instructions route.
func TestIssue5698WiringAnchors(t *testing.T) {
	read := func(path string) string {
		data, err := os.ReadFile(filepath.Join(".", path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(data)
	}
	inject := read("agent_prompt_inject.go")
	if !strings.Contains(inject, "renderKeyedInstructionBlocks()") {
		t.Errorf("prompt assembly must render keyed instruction blocks")
	}
	if !strings.Contains(inject, "consumeKeyedInstructionAnnounce(keyedAnnounced)") {
		t.Errorf("prompt assembly must consume announce bits at the ship decision")
	}
	tuiModel := read("../tui/model.go")
	if !strings.Contains(tuiModel, "AttachKeyedInstructions(ses.ID, ses.KeyedInstructions, instrSink)") {
		t.Errorf("TUI SetSession must hydrate keyed instructions + sink")
	}
	tuiCmds := read("../tui/commands.go")
	if !strings.Contains(tuiCmds, "\"/instructions\"") {
		t.Errorf("/instructions command route missing")
	}
}

func auditOps5698(a *Agent) []string {
	var ops []string
	for _, l := range a.KeyedInstructionAuditLines() {
		ops = append(ops, l.Op)
	}
	return ops
}

func contains5698(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
