package context

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestPinnedSurvivesDirectSummarize verifies the pinned contract on the
// direct Summarize path (PTL recovery, /compact) - not just
// ApplyCompactResult (#382).
func TestPinnedSurvivesDirectSummarize(t *testing.T) {
	cm := NewManager(10000)
	ctx := context.Background()
	prov := &mockProvider{}

	if _, err := cm.Pinned().Add("CRITICAL: build with go build -tags goolm ./..."); err != nil {
		t.Fatalf("pin add failed: %v", err)
	}

	sys := provider.Message{Role: "system"}
	sys.Content = []provider.ContentBlock{{Type: "text", Text: "System prompt."}}
	firstU := provider.Message{Role: "user"}
	firstU.Content = []provider.ContentBlock{{Type: "text", Text: "First message."}}
	firstA := provider.Message{Role: "assistant"}
	firstA.Content = []provider.ContentBlock{{Type: "text", Text: "First response."}}
	secondU := provider.Message{Role: "user"}
	secondU.Content = []provider.ContentBlock{{Type: "text", Text: "Second message."}}
	secondA := provider.Message{Role: "assistant"}
	secondA.Content = []provider.ContentBlock{{Type: "text", Text: "Second response."}}
	cm.Add(sys)
	cm.Add(firstU)
	cm.Add(firstA)
	cm.Add(secondU)
	cm.Add(secondA)

	if err := cm.Summarize(ctx, prov); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	msgs := cm.Messages()
	if !messageContainsTextInList(msgs, "[Previous conversation summary]") {
		t.Fatal("expected summary after direct Summarize")
	}
	if !messageContainsTextInList(msgs, "[Pinned Context") {
		t.Fatal("expected pinned context to survive direct Summarize (#382)")
	}
	if !messageContainsTextInList(msgs, "go build -tags goolm") {
		t.Fatal("expected pinned content present after direct Summarize")
	}

	summaryIdx, pinnedIdx := -1, -1
	for i, msg := range msgs {
		if msg.Role == "system" && len(msg.Content) > 0 {
			text := msg.Content[0].Text
			if strings.Contains(text, "[Previous conversation summary]") {
				summaryIdx = i
			}
			if strings.Contains(text, "[Pinned Context") {
				pinnedIdx = i
			}
		}
	}
	if summaryIdx < 0 || pinnedIdx != summaryIdx+1 {
		t.Fatalf("expected pinned right after summary, got summary=%d pinned=%d", summaryIdx, pinnedIdx)
	}
}

// TestCalibrationSampleIncludesToolOverhead verifies the calibration
// sample's estimated side includes toolDefinitionOverhead so both sides
// share the same composition (#383).
func TestCalibrationSampleIncludesToolOverhead(t *testing.T) {
	cm := NewManager(100000)
	cm.SetToolDefinitionOverhead(5000)

	body := provider.Message{Role: "user"}
	body.Content = []provider.ContentBlock{{Type: "text", Text: strings.Repeat("a", 2000)}}
	cm.Add(body)

	usage := provider.TokenUsage{InputTokens: 7000, OutputTokens: 10}
	cm.RecordUsage(usage)

	want := cm.tokens + 5000
	if got := cm.calibrator.LastEstimated(); got != want {
		t.Fatalf("calibration estimate should include tool overhead: want %d, got %d", want, got)
	}
}
