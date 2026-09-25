package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
)

// addToolRound appends one assistant tool_use + user tool_result round to the
// manager. filler is the raw tool output body (must be long enough to exceed
// toolResultClearMinLen and free of error markers so hasSemanticImportance
// does not preserve it).
func addToolRound(t *testing.T, cm *ctxpkg.Manager, i int, path string, filler string) {
	t.Helper()
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		provider.ToolUseBlock(fmt.Sprintf("call-%d", i), "read_file", []byte(fmt.Sprintf(`{"path":%q}`, path))),
	}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		provider.ToolResultBlock(fmt.Sprintf("call-%d", i), filler, false),
	}})
}

// countToolResultBlocks returns how many tool_result blocks are present.
func countToolResultBlocks(cm *ctxpkg.Manager) int {
	n := 0
	for _, msg := range cm.Messages() {
		for _, b := range msg.Content {
			if b.Output != "" || b.ToolID != "" {
				if b.Type == "tool_result" {
					n++
				}
			}
		}
	}
	return n
}

// findResultLen returns the Output length of the tool_result with the given id.
func findResultLen(cm *ctxpkg.Manager, id string) (int, bool) {
	for _, msg := range cm.Messages() {
		for _, b := range msg.Content {
			if b.Type == "tool_result" && b.ToolID == id {
				return len(b.Output), true
			}
		}
	}
	return 0, false
}

func TestDeterministicReclaimFreesTokensAndKeepsPairing(t *testing.T) {
	cm := ctxpkg.NewManager(200000)
	a := &Agent{contextManager: cm}

	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		provider.TextBlock("task"),
	}})
	filler := strings.Repeat("x", 2000)
	for i := 0; i < 5; i++ {
		addToolRound(t, cm, i, "x.go", filler)
	}

	before := cm.TokenCount()
	if before <= 0 {
		t.Fatalf("expected nonzero token count, got %d", before)
	}
	msgsBefore := len(cm.Messages())
	blocksBefore := countToolResultBlocks(cm)

	freed := a.deterministicReclaim("test")
	if freed <= 0 {
		t.Fatalf("expected deterministic reclaim to free tokens, got %d", freed)
	}
	if got := cm.TokenCount(); got != before-freed {
		t.Fatalf("token count mismatch: before=%d freed=%d after=%d", before, freed, got)
	}
	// Pairing and message structure must survive: no message dropped, every
	// tool_result block still present (payloads replaced, blocks kept).
	if len(cm.Messages()) != msgsBefore {
		t.Fatalf("message count changed: before=%d after=%d", msgsBefore, len(cm.Messages()))
	}
	if got := countToolResultBlocks(cm); got != blocksBefore {
		t.Fatalf("tool_result block count changed: before=%d after=%d", blocksBefore, got)
	}

	// Older results are compacted to short placeholders; the newest stays
	// verbatim (keepN preservation).
	if n, ok := findResultLen(cm, "call-0"); !ok || n >= 500 {
		t.Fatalf("expected oldest result compacted to placeholder, len=%d ok=%v", n, ok)
	}
	if n, ok := findResultLen(cm, "call-4"); !ok || n != len(filler) {
		t.Fatalf("expected newest result intact (len=%d), got len=%d ok=%v", len(filler), n, ok)
	}

	// Idempotency: a second pass frees nothing further.
	if second := a.deterministicReclaim("test"); second != 0 {
		t.Fatalf("expected second pass to be a no-op, freed %d", second)
	}
}

func TestMaybeReclaimOverThreshold(t *testing.T) {
	cm := ctxpkg.NewManager(6000)
	a := &Agent{contextManager: cm}

	filler := strings.Repeat("y", 6000)
	for i := 0; i < 20; i++ {
		addToolRound(t, cm, i, "x.go", filler)
	}
	if cm.TokenCount() < cm.AutoCompactThreshold() {
		t.Fatalf("precondition failed: tokens=%d not over threshold=%d", cm.TokenCount(), cm.AutoCompactThreshold())
	}

	reclaimed, under := a.maybeReclaimOverThreshold("test")
	if reclaimed <= 0 {
		t.Fatalf("expected reclaim to free tokens, got %d", reclaimed)
	}
	if want := cm.TokenCount() < cm.AutoCompactThreshold(); under != want {
		t.Fatalf("underThreshold=%v inconsistent with tokens=%d threshold=%d", under, cm.TokenCount(), cm.AutoCompactThreshold())
	}
	if !under {
		t.Fatalf("expected context under threshold after reclaim: tokens=%d threshold=%d", cm.TokenCount(), cm.AutoCompactThreshold())
	}

	// No reclaimable content -> reported as not reclaimed.
	cm2 := ctxpkg.NewManager(2000)
	a2 := &Agent{contextManager: cm2}
	cm2.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		provider.TextBlock(strings.Repeat("z", 5000)),
	}})
	if r, u := a2.maybeReclaimOverThreshold("test"); r != 0 || u {
		t.Fatalf("expected no reclaim on non-reclaimable content, got reclaimed=%d under=%v", r, u)
	}
}

func TestCompactLocallyForSendBudgetReclaimsBeforeTruncation(t *testing.T) {
	cm := ctxpkg.NewManager(20000)
	a := &Agent{contextManager: cm}

	filler := strings.Repeat("w", 2000)
	i := 0
	for cm.TokenCount() < cm.PromptBudget() && i < 50 {
		addToolRound(t, cm, i, "x.go", filler)
		i++
	}
	if cm.TokenCount() < cm.PromptBudget() {
		t.Fatalf("precondition failed: could not exceed budget, tokens=%d budget=%d", cm.TokenCount(), cm.PromptBudget())
	}
	msgsBefore := len(cm.Messages())

	if !a.compactLocallyForSendBudget("test") {
		t.Fatal("expected compactLocallyForSendBudget to succeed via reclaim")
	}
	if cm.TokenCount() >= cm.PromptBudget() {
		t.Fatalf("expected tokens under budget after reclaim, tokens=%d budget=%d", cm.TokenCount(), cm.PromptBudget())
	}
	// Reclaim sufficed, so no message groups were destroyed.
	if len(cm.Messages()) != msgsBefore {
		t.Fatalf("expected no truncation (reclaim sufficed), messages %d -> %d", msgsBefore, len(cm.Messages()))
	}

	// Control: with no reclaimable content (tool outputs below the clearing
	// threshold) the destructive truncation path must still fire and drop
	// message groups until the prompt fits.
	cm2 := ctxpkg.NewManager(20000)
	a2 := &Agent{contextManager: cm2}
	shortFiller := strings.Repeat("s", 400) // < toolResultClearMinLen
	for i := 0; i < 300 && cm2.TokenCount() < cm2.PromptBudget(); i++ {
		addToolRound(t, cm2, i, fmt.Sprintf("f%d.go", i), shortFiller)
		if i%5 == 0 {
			// Plain user text starts a new interaction group; without it the
			// whole history is one group and truncation is a no-op.
			cm2.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
				provider.TextBlock("continue"),
			}})
		}
	}
	if cm2.TokenCount() < cm2.PromptBudget() {
		t.Fatalf("precondition failed: tokens=%d budget=%d", cm2.TokenCount(), cm2.PromptBudget())
	}
	before2 := len(cm2.Messages())
	if !a2.compactLocallyForSendBudget("test-truncate") {
		t.Fatal("expected truncation fallback to report success")
	}
	if len(cm2.Messages()) >= before2 {
		t.Fatalf("expected truncation to drop messages, before=%d after=%d", before2, len(cm2.Messages()))
	}
}

func TestDeterministicReclaimSkipsWhilePrecompactRunning(t *testing.T) {
	cm := ctxpkg.NewManager(200000)
	a := &Agent{contextManager: cm}

	filler := strings.Repeat("x", 2000)
	for i := 0; i < 3; i++ {
		addToolRound(t, cm, i, "x.go", filler)
	}
	before := cm.TokenCount()

	a.precompact = &precompactState{}
	if freed := a.deterministicReclaim("test"); freed != 0 {
		t.Fatalf("expected reclaim to be skipped while precompact running, freed %d", freed)
	}
	if cm.TokenCount() != before {
		t.Fatalf("expected no mutation while precompact running, tokens %d -> %d", before, cm.TokenCount())
	}
}

func TestMaybeAutoCompactDefersAfterReclaim(t *testing.T) {
	cm := ctxpkg.NewManager(6000)
	a := &Agent{contextManager: cm}

	filler := strings.Repeat("y", 6000)
	for i := 0; i < 20; i++ {
		addToolRound(t, cm, i, "x.go", filler)
	}
	if cm.TokenCount() < cm.AutoCompactThreshold() {
		t.Fatalf("precondition failed: tokens=%d not over threshold=%d", cm.TokenCount(), cm.AutoCompactThreshold())
	}

	var events []provider.StreamEvent
	onEvent := func(ev provider.StreamEvent) { events = append(events, ev) }
	if err := a.maybeAutoCompact(context.Background(), onEvent, new(bool)); err != nil {
		t.Fatalf("maybeAutoCompact returned error: %v", err)
	}
	// The reclaim must have deferred the LLM precompact entirely: no
	// background precompact scheduled, a single user-visible notice emitted.
	if a.precompact != nil {
		t.Fatal("expected no precompact to be scheduled after successful reclaim")
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly one deferred notice event, got %d", len(events))
	}
}
