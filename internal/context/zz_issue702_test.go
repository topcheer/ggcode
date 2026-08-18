package context

// Regression tests for issue #702 (four LOW defects in internal/context):
//   (a) #663 latent regression risk — benign removal paths must keep calling
//       markBenignRemoval so a live-shrunk compaction result rejected after
//       them classifies as BenignTrim (cooldown stays), never UserReset
//       (which would refund the precompact cooldown and reschedule redundant
//       full-context summarizations).
//   (b) CheckAndSummarize reported changed=true when Summarize had NOT
//       applied anything — it inferred "applied" from a version delta, but
//       Add()/benign mutations also bump m.version.
//   (c) AnalyzeBudget used a flat len/4 estimate (while claiming to share
//       EstimateTokens' heuristic), systematically underestimating CJK text
//       ~25%-2x versus the tokenizer's ~1.0 chars/token CJK tier.
//   (d) PinnedContext.Add enforced maxPinnedTotal in BYTES while the
//       per-item cap (#386) counts RUNES — mixed units let multi-byte text
//       blow past the advertised budget.

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// --- (a) benign removal classification -------------------------------------

// TestIssue702a_OrphanCleanupClassifiesAsBenignTrim drives the orphan
// tool-result cleanup (ReconcileToolCalls → removeOrphanToolResults) and
// then applies a compaction result for the pre-cleanup snapshot: the shrink
// must classify as CompactRejectBenignTrim (#663 contract), not UserReset.
func TestIssue702a_OrphanCleanupClassifiesAsBenignTrim(t *testing.T) {
	m := newTestManager(t)

	// A real conversation plus one message holding ONLY an orphan
	// tool_result (its tool_use never appeared) — ReconcileToolCalls
	// removes that whole message.
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "tool_result", ToolID: "orphan-1", Output: strings.Repeat("orphan output ", 30)},
	}})

	snap := m.CompactSnapshot() // opens the #663 attribution window

	m.ReconcileToolCalls() // Phase 0 removes orphans even when no late-result fixes exist
	if len(m.messages) != 2 {
		t.Fatalf("orphan message must be removed, live len=%d", len(m.messages))
	}

	// Live-shrunk result must be rejected AS BENIGN TRIM.
	applied, _ := m.ApplyCompactResult(snap, CompactResult{
		Changed:  true,
		Messages: []provider.Message{{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "summary"}}}},
	})
	if applied {
		t.Fatal("live-shrunk snapshot must be rejected")
	}
	if got := m.LastCompactRejectReason(); got != CompactRejectBenignTrim {
		t.Fatalf("after orphan cleanup, reject must be benign-trim, got %v", got)
	}
}

// TestIssue702a_UserClearClassifiesAsUserReset pins the other side of the
// #663 classifier: a real user-driven reset (Clear) must still classify as
// UserReset so the cooldown refund stays available.
func TestIssue702a_UserClearClassifiesAsUserReset(t *testing.T) {
	m := newTestManager(t)
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}})
	snap := m.CompactSnapshot()

	m.Clear() // user-driven reset

	applied, _ := m.ApplyCompactResult(snap, CompactResult{
		Changed:  true,
		Messages: []provider.Message{{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "summary"}}}},
	})
	if applied {
		t.Fatal("live-shrunk snapshot must be rejected")
	}
	if got := m.LastCompactRejectReason(); got != CompactRejectUserReset {
		t.Fatalf("after user Clear, reject must be user-reset, got %v", got)
	}
}

// --- (b) CheckAndSummarize must not report a false "changed" ----------------

// midChatMutatorProvider's Chat performs a benign non-tail mutation on the
// manager DURING the LLM window — exactly what retry truncation / orphan
// cleanup does concurrently in production. The mutation bumps m.version
// (and nonTailMutSeq), Summarize's TOCTOU guard discards the snapshot, and
// nothing is applied.
type midChatMutatorProvider struct {
	stubProvider
	onChat func()
}

func (p midChatMutatorProvider) Chat(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	if p.onChat != nil {
		p.onChat()
	}
	return p.stubProvider.Chat(ctx, msgs, tools)
}

// TestIssue702b_NoFalseChangedWhenVersionBumpsWithoutApply pins the
// false-positive: a version bump WITHOUT an applied summary (TOCTOU discard
// path) must report changed=false. The old `version != beforeVersion` check
// reported true here, making callers skip fallbacks while the context was
// still over budget.
func TestIssue702b_NoFalseChangedWhenVersionBumpsWithoutApply(t *testing.T) {
	m := newTestManager(t)

	// Real conversation plus an orphan tool_result message whose removal
	// (triggered synchronously inside Chat) is the mid-window mutation.
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "tool_result", ToolID: "orphan-2", Output: strings.Repeat("orphan output ", 30)},
	}})

	prov := midChatMutatorProvider{onChat: func() { m.ReconcileToolCalls() }}

	changed, err := m.CheckAndSummarize(context.Background(), prov)
	if err != nil {
		t.Fatalf("CheckAndSummarize: %v", err)
	}
	if changed {
		t.Fatal("CheckAndSummarize must report changed=false when Summarize discarded its snapshot " +
			"(TOCTOU) and applied nothing — the version bump came from the benign mutation, not an applied summary")
	}
	// And the conversation must NOT contain the stub summary (nothing applied).
	for _, txt := range msgTexts(m) {
		if strings.Contains(txt, "stub summary") {
			t.Fatal("summary must not be applied on the TOCTOU-discard path")
		}
	}
}

// TestIssue702b_TrueChangeWhenApplied pins the positive side: when Summarize
// actually replaces the conversation, changed must be true.
func TestIssue702b_TrueChangeWhenApplied(t *testing.T) {
	m := NewManager(2000) // tiny window forces compaction
	for i := 0; i < 8; i++ {
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
			{Type: "text", Text: strings.Repeat("question ", 80)},
		}})
		m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "text", Text: strings.Repeat("answer ", 80)},
		}})
	}
	changed, err := m.CheckAndSummarize(context.Background(), stubProvider{})
	if err != nil {
		t.Fatalf("CheckAndSummarize: %v", err)
	}
	if !changed {
		t.Fatal("CheckAndSummarize must report changed=true when Summarize applied a summary")
	}
	if !strings.Contains(strings.Join(msgTexts(m), "\n"), "stub summary") {
		t.Fatal("summary message must be present after applied summarization")
	}
}

func msgTexts(m *Manager) []string {
	var out []string
	for _, msg := range m.Messages() {
		for _, b := range msg.Content {
			if b.Text != "" {
				out = append(out, b.Text)
			}
		}
	}
	return out
}

// --- (c) AnalyzeBudget must be script-aware ---------------------------------

func TestIssue702c_AnalyzeBudgetCJKNotUnderestimated(t *testing.T) {
	// Same RUNE count, ASCII vs pure-CJK: the CJK text costs ~3.5x more
	// tokens under the script-aware estimator. The old len/4 fork priced
	// them nearly identically (CJK is 3 bytes/rune → only ~2.6x bytes).
	const runes = 200
	asciiMsg := provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "text", Text: strings.Repeat("a", runes)},
	}}
	cjkMsg := provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "text", Text: strings.Repeat("汉", runes)},
	}}

	asciiTokens := AnalyzeBudget([]provider.Message{asciiMsg}).TotalTokens
	cjkTokens := AnalyzeBudget([]provider.Message{cjkMsg}).TotalTokens

	// Consistency with EstimateTokens (the contract AnalyzeBudget now claims).
	if want := EstimateTokens(strings.Repeat("汉", runes)); cjkTokens != want {
		t.Fatalf("AnalyzeBudget CJK total = %d, want EstimateTokens = %d", cjkTokens, want)
	}
	if want := EstimateTokens(strings.Repeat("a", runes)); asciiTokens != want {
		t.Fatalf("AnalyzeBudget ASCII total = %d, want EstimateTokens = %d", asciiTokens, want)
	}
	// The script-aware gap: CJK must be substantially more expensive per rune.
	if cjkTokens < asciiTokens*2 {
		t.Fatalf("CJK (%d tokens) must be ≥2x ASCII (%d tokens) for equal rune counts", cjkTokens, asciiTokens)
	}
}

// --- (d) pinned budget must count runes uniformly ---------------------------

func TestIssue702d_PinnedBudgetCountsRunes(t *testing.T) {
	p := newPinnedContext()

	// Fill with CJK items priced in RUNES: 3 items x 2000 runes = 6000 runes
	// (18000 BYTES). A byte-counting implementation of the total cap rejects
	// the SECOND item (12000 bytes > 8000); the rune contract accepts it.
	cjk := strings.Repeat("汉", maxPinnedChars) // exactly the per-item rune cap
	for i := 0; i < 3; i++ {
		if _, err := p.Add(cjk); err != nil {
			t.Fatalf("item %d: rune-priced CJK item at the rune cap must be accepted: %v", i+1, err)
		}
	}

	// An over-cap item is TRUNCATED to the cap (per #386 rune-safe semantics),
	// not rejected — and the truncated item fills the budget to exactly 8000.
	id, err := p.Add(strings.Repeat("字", maxPinnedChars+50))
	if err != nil {
		t.Fatalf("over-cap item must be truncated-and-accepted: %v", err)
	}
	for _, item := range p.List() {
		if item.ID == id && len([]rune(item.Text)) != maxPinnedChars {
			t.Fatalf("over-cap item must be truncated to %d runes, got %d", maxPinnedChars, len([]rune(item.Text)))
		}
	}

	// One more rune anywhere must now overflow the total rune budget.
	if _, err := p.Add("x"); err == nil {
		t.Fatal("total rune budget (8000) exceeded — Add must be rejected")
	}

	// Final invariant: total pinned runes within the advertised budget.
	total := 0
	for _, item := range p.List() {
		total += len([]rune(item.Text))
	}
	if total != maxPinnedTotal {
		t.Fatalf("total pinned runes = %d, want exactly %d", total, maxPinnedTotal)
	}
}
