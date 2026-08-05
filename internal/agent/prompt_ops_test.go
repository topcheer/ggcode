package agent

import (
	"strings"
	"testing"
)

func TestAnalyzePromptRedundancy_NoRedundancy(t *testing.T) {
	prompt := `You are a coding assistant. Read files before editing them.
Always run tests after making changes. Commit only the files you modified.
Use the narrowest search patterns possible to save context tokens.`
	findings := analyzePromptRedundancy(prompt)
	if len(findings) != 0 {
		t.Errorf("expected 0 findings for non-redundant prompt, got %d", len(findings))
	}
}

func TestAnalyzePromptRedundancy_ExactDuplicate(t *testing.T) {
	dup := "Always read the file before editing it to ensure you have the latest content."
	prompt := dup + "\n\n" + dup + "\n\nSome other unique instruction about running tests."
	findings := analyzePromptRedundancy(prompt)
	if len(findings) == 0 {
		t.Fatal("expected at least 1 finding for exact duplicate")
	}
	if findings[0].similarity < 0.9 {
		t.Errorf("expected similarity >= 0.9 for exact duplicate, got %.2f", findings[0].similarity)
	}
}

func TestAnalyzePromptRedundancy_NearDuplicate(t *testing.T) {
	// Two sentences with high word overlap (paraphrases, not exact duplicates).
	prompt := `Always read files before editing them to ensure you have fresh content available.
	Read files before editing them so the content is fresh and up to date.
	This is a unique instruction about running go test with the goolm tag.`
	findings := analyzePromptRedundancy(prompt)
	if len(findings) == 0 {
		t.Fatal("expected at least 1 finding for near-duplicate")
	}
	if findings[0].similarity < promptRedunSimilarityThreshold {
		t.Errorf("similarity %.2f below threshold %.2f", findings[0].similarity, promptRedunSimilarityThreshold)
	}
}

func TestAnalyzePromptRedundancy_ShortPromptSkipped(t *testing.T) {
	prompt := "Short prompt."
	findings := analyzePromptRedundancy(prompt)
	if findings != nil {
		t.Errorf("expected nil for short prompt, got %v", findings)
	}
}

func TestAnalyzePromptRedundancy_MaxWarningsCap(t *testing.T) {
	// Create many duplicate sentences to test the cap.
	var parts []string
	for i := 0; i < 20; i++ {
		parts = append(parts, "Always read files before editing to ensure you have the latest content available.")
	}
	prompt := strings.Join(parts, "\n\n")
	findings := analyzePromptRedundancy(prompt)
	if len(findings) > promptRedunMaxWarnings {
		t.Errorf("expected at most %d findings, got %d", promptRedunMaxWarnings, len(findings))
	}
}

func TestTokenizePromptWords_StopWordsRemoved(t *testing.T) {
	words := tokenizePromptWords("The quick brown fox jumps over the lazy dog")
	if words["the"] {
		t.Error("stop word 'the' should be removed")
	}
	if !words["quick"] {
		t.Error("non-stop word 'quick' should be present")
	}
	if !words["fox"] {
		t.Error("non-stop word 'fox' should be present")
	}
}

func TestTokenizePromptWords_ShortWordsFiltered(t *testing.T) {
	words := tokenizePromptWords("a b c go rs")
	if words["a"] {
		t.Error("single-char word 'a' should be filtered")
	}
	if words["b"] {
		t.Error("single-char word 'b' should be filtered")
	}
	if !words["go"] {
		t.Error("2-char word 'go' should be present")
	}
}

func TestJaccardSimilarity_Identical(t *testing.T) {
	a := tokenizePromptWords("read files before editing")
	b := tokenizePromptWords("read files before editing")
	sim := jaccardSimilarity(a, b)
	if sim != 1.0 {
		t.Errorf("expected 1.0 for identical sets, got %.2f", sim)
	}
}

func TestJaccardSimilarity_Disjoint(t *testing.T) {
	a := tokenizePromptWords("read files editing")
	b := tokenizePromptWords("run tests deploy")
	sim := jaccardSimilarity(a, b)
	if sim != 0.0 {
		t.Errorf("expected 0.0 for disjoint sets, got %.2f", sim)
	}
}

func TestJaccardSimilarity_PartialOverlap(t *testing.T) {
	a := tokenizePromptWords("read files before editing them carefully")
	b := tokenizePromptWords("read files before running tests")
	// After stop word removal: a={read, files, editing, carefully}, b={read, files, running, tests}
	// intersection: read, files = 2
	// union: read, files, editing, carefully, running, tests = 6
	sim := jaccardSimilarity(a, b)
	expected := 2.0 / 6.0
	if sim < expected-0.01 || sim > expected+0.01 {
		t.Errorf("expected ~%.2f, got %.2f", expected, sim)
	}
}

func TestJaccardSimilarity_EmptySets(t *testing.T) {
	sim := jaccardSimilarity(map[string]bool{}, map[string]bool{})
	if sim != 0 {
		t.Errorf("expected 0 for empty sets, got %.2f", sim)
	}
}

func TestSplitPromptSentences(t *testing.T) {
	prompt := "First sentence here. Second one follows! Is this a question?\n\nNew paragraph."
	sentences := splitPromptSentences(prompt)
	if len(sentences) < 3 {
		t.Errorf("expected at least 3 sentences, got %d", len(sentences))
	}
}

func TestSplitPromptSentences_DoubleNewline(t *testing.T) {
	prompt := "First paragraph without period\n\nSecond paragraph also without period"
	sentences := splitPromptSentences(prompt)
	if len(sentences) < 2 {
		t.Errorf("expected at least 2 sentences from double-newline split, got %d", len(sentences))
	}
}

func TestTruncateForDisplay_ShortString(t *testing.T) {
	s := "short string"
	result := truncateForDisplay(s, 80)
	if result != s {
		t.Errorf("expected unchanged string, got %s", result)
	}
}

func TestTruncateForDisplay_LongString(t *testing.T) {
	s := strings.Repeat("abcdefghij", 20) // 200 chars
	result := truncateForDisplay(s, 50)
	if len(result) > 50 {
		t.Errorf("expected result <= 50 chars, got %d", len(result))
	}
	if !strings.HasSuffix(result, "...") {
		t.Error("expected '...' suffix for truncated string")
	}
}

func TestAnalyzePromptBudget_SingleDominantLayer(t *testing.T) {
	layers := []promptLayerInfo{
		{name: "base", tokens: 1000},
		{name: "ratchet", tokens: 5000},
		{name: "playbook", tokens: 500},
	}
	report := analyzePromptBudget(layers)
	if report.totalTokens != 6500 {
		t.Errorf("expected total 6500, got %d", report.totalTokens)
	}
	if report.dominantLayer != "ratchet" {
		t.Errorf("expected dominant layer 'ratchet', got %s", report.dominantLayer)
	}
	if report.dominantTokens != 5000 {
		t.Errorf("expected dominant tokens 5000, got %d", report.dominantTokens)
	}
	if report.dominantRatio < 0.76 || report.dominantRatio > 0.77 {
		t.Errorf("expected dominant ratio ~0.77, got %.2f", report.dominantRatio)
	}
}

func TestAnalyzePromptBudget_EmptyLayers(t *testing.T) {
	report := analyzePromptBudget(nil)
	if report.totalTokens != 0 {
		t.Errorf("expected 0 tokens, got %d", report.totalTokens)
	}
}

func TestPromptOpsState_Reset(t *testing.T) {
	s := newPromptOpsState()
	s.fired = true
	s.lastPrompt = "test"
	s.lastTokenEst = 5000
	s.reset()
	if s.fired {
		t.Error("fired should be false after reset")
	}
	if s.lastPrompt != "" {
		t.Error("lastPrompt should be empty after reset")
	}
	if s.lastTokenEst != 0 {
		t.Error("lastTokenEst should be 0 after reset")
	}
}

func TestAnalyzePromptRedundancy_RealWorldPrompt(t *testing.T) {
	// Simulate a realistic system prompt with exact duplicate sentences
	// (common when the same rule appears in ratchet rules and playbook).
	prompt := `You are a helpful coding assistant working in a Go codebase.

## Ratchet Rules
1. Always use the goolm build tag when running go build or go test.
2. Never commit files you did not explicitly modify in this session.
3. Always read a file with read_file before editing it so content is fresh.

## Playbook
- Always use the goolm build tag when running go build or go test.
- Never commit files you did not explicitly modify in this session.
- Always read a file with read_file before editing it so content is fresh.

## Extra
The working directory is /Volumes/new/ggai/ggcode.`
	findings := analyzePromptRedundancy(prompt)
	if len(findings) == 0 {
		t.Fatal("expected redundancy findings in realistic prompt with exact duplicate rules")
	}
}
