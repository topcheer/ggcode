package context

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// pccTestProvider is a configurable mock for PCC probes: it answers each
// Chat call with a marker derived from the request text (so block order
// can be verified), counts concurrency, and can inject failures.
type pccTestProvider struct {
	mu          sync.Mutex
	calls       int
	maxInFlight int32
	inFlight    int32
	failOn      func(requestText string) bool
	emptyOn     func(requestText string) bool
}

func (p *pccTestProvider) Name() string { return "pcc-mock" }

func (p *pccTestProvider) requestMarker(msgs []provider.Message) string {
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, "MARK-") {
				return b.Text
			}
		}
	}
	return ""
}

func (p *pccTestProvider) Chat(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	cur := atomic.AddInt32(&p.inFlight, 1)
	for {
		old := atomic.LoadInt32(&p.maxInFlight)
		if cur <= old || atomic.CompareAndSwapInt32(&p.maxInFlight, old, cur) {
			break
		}
	}
	defer atomic.AddInt32(&p.inFlight, -1)
	reqText := ""
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == "text" {
				reqText += b.Text
			}
		}
	}
	if p.failOn != nil && p.failOn(reqText) {
		return nil, errors.New("injected block failure")
	}
	// Hold each call open briefly so fan-out overlap is observable even
	// without -race scheduler interleaving: all three goroutines are
	// spawned before the first 10ms window can elapse.
	time.Sleep(10 * time.Millisecond)
	text := "block-summary-ok"
	if p.emptyOn != nil && p.emptyOn(reqText) {
		text = ""
	}
	return &provider.ChatResponse{
		Message: provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{{Type: "text", Text: text}},
		},
		Usage: provider.TokenUsage{InputTokens: 10, OutputTokens: 5},
	}, nil
}

func (p *pccTestProvider) ChatStream(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}

func (p *pccTestProvider) CountTokens(ctx context.Context, msgs []provider.Message) (int, error) {
	return 100, nil
}

// bigPayload builds a payload well above pccMinPayloadTokens with N
// message units, each tagged MARK-i so block membership is verifiable.
func bigPayload(units int, fillerLines int) string {
	var sb strings.Builder
	line := strings.Repeat("x", 60) // ~15 tokens per line
	for i := 0; i < units; i++ {
		sb.WriteString(fmt.Sprintf("[user]\nMARK-%d start\n", i))
		for j := 0; j < fillerLines; j++ {
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func TestSplitPayloadBlocksSmallReturnsNil(t *testing.T) {
	small := "[user]\nhello\n[assistant]\nhi there\n"
	if got := splitPayloadBlocks(small, pccMinPayloadTokens); got != nil {
		t.Fatalf("small payload should not split, got %d blocks", len(got))
	}
}

func TestSplitPayloadBlocksSingleUnitReturnsNil(t *testing.T) {
	one := "[user]\n" + strings.Repeat("y", 100) + "\n"
	if got := splitPayloadBlocks(one, 10); got != nil {
		t.Fatalf("single message unit should not split, got %d blocks", len(got))
	}
}

func TestSplitPayloadBlocksCapsAtMaxAndPreservesOrder(t *testing.T) {
	payload := bigPayload(20, 60) // ~20 units × ~900 tokens > pccMinPayloadTokens
	blocks := splitPayloadBlocks(payload, pccMinPayloadTokens)
	if len(blocks) < 2 {
		t.Fatalf("expected multi-block split, got %d", len(blocks))
	}
	if len(blocks) > pccMaxBlocks {
		t.Fatalf("blocks %d exceed cap %d", len(blocks), pccMaxBlocks)
	}
	// MARK tags must appear in strictly increasing order across blocks.
	seen := 0
	for _, b := range blocks {
		for i := seen; i < 20; i++ {
			if strings.Contains(b, fmt.Sprintf("MARK-%d ", i)) {
				seen = i + 1
			}
		}
	}
	if seen != 20 {
		t.Fatalf("order broken: last sequential MARK seen = %d, want 20", seen-1)
	}
}

func TestSummarizeParallelConcurrentAndOrdered(t *testing.T) {
	blocks := []string{"[user]\nMARK-A", "[user]\nMARK-B", "[user]\nMARK-C"}
	p := &pccTestProvider{}
	var usageCalls int32
	summary, ok := summarizeParallel(context.Background(), p, blocks, 600, func(provider.TokenUsage) {
		atomic.AddInt32(&usageCalls, 1)
	})
	if !ok {
		t.Fatal("expected parallel summarize to succeed")
	}
	if p.calls != 3 {
		t.Fatalf("expected 3 fan-out calls, got %d", p.calls)
	}
	if got := atomic.LoadInt32(&p.maxInFlight); got < 2 {
		t.Fatalf("expected concurrent in-flight calls (>=2), got %d", got)
	}
	if !strings.Contains(summary, "block-summary-ok") {
		t.Fatalf("summary missing block text: %q", summary)
	}
	if parts := strings.Split(summary, "\n\n---\n\n"); len(parts) != 3 {
		t.Fatalf("expected 3 joined parts, got %d", len(parts))
	}
	if atomic.LoadInt32(&usageCalls) != 3 {
		t.Fatalf("expected 3 serial onUsage calls, got %d", usageCalls)
	}
}

func TestSummarizeParallelBlockFailureFallsBack(t *testing.T) {
	blocks := []string{"[user]\nMARK-A", "[user]\nMARK-B"}
	p := &pccTestProvider{failOn: func(req string) bool { return strings.Contains(req, "MARK-B") }}
	_, ok := summarizeParallel(context.Background(), p, blocks, 400, nil)
	if ok {
		t.Fatal("block failure must signal fallback (ok=false)")
	}
}

func TestSummarizeParallelEmptyBlockFallsBack(t *testing.T) {
	blocks := []string{"[user]\nMARK-A", "[user]\nMARK-B"}
	p := &pccTestProvider{emptyOn: func(req string) bool { return strings.Contains(req, "MARK-A") }}
	_, ok := summarizeParallel(context.Background(), p, blocks, 400, nil)
	if ok {
		t.Fatal("empty block summary must signal fallback (ok=false)")
	}
}

func TestSummarizeMessagesSmallPayloadSingleRequest(t *testing.T) {
	// Zero-regression guard: a payload below the PCC floor must make
	// exactly one prov.Chat call through the sequential path.
	p := &pccTestProvider{}
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "short conversation"}}},
	}
	summary, err := summarizeMessages(context.Background(), p, msgs, nil, 2000, "")
	if err != nil {
		t.Fatalf("sequential path failed: %v", err)
	}
	if summary == "" {
		t.Fatal("empty summary")
	}
	if p.calls != 1 {
		t.Fatalf("small payload must stay single-request, got %d calls", p.calls)
	}
}
