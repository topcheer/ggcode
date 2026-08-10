package agent

import (
	"testing"
)

func TestNewToolAffinityLearner(t *testing.T) {
	learner := NewToolAffinityLearner()
	if learner == nil {
		t.Fatal("NewToolAffinityLearner returned nil")
	}
	if learner.affinities == nil {
		t.Error("affinities map not initialized")
	}
	if learner.minSamples != 3 {
		t.Errorf("expected minSamples=3, got %d", learner.minSamples)
	}
	if learner.maxEntries != 1000 {
		t.Errorf("expected maxEntries=1000, got %d", learner.maxEntries)
	}
}

func TestRecordOutcome(t *testing.T) {
	learner := NewToolAffinityLearner()

	// Record a success
	learner.RecordOutcome("read_file", "read config", true)

	// Verify entry was created
	if len(learner.affinities) != 1 {
		t.Errorf("expected 1 entry, got %d", len(learner.affinities))
	}

	// Record a failure for same context
	learner.RecordOutcome("read_file", "read config", false)

	// Verify entry was updated, not duplicated
	if len(learner.affinities) != 1 {
		t.Errorf("expected 1 entry, got %d", len(learner.affinities))
	}

	// Verify attempts and successes were recorded
	var entry *toolAffinityEntry
	for _, e := range learner.affinities {
		entry = e
		break
	}
	if entry == nil {
		t.Fatal("no entry found")
	}
	if entry.attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", entry.attempts)
	}
	if entry.successes != 1 {
		t.Errorf("expected 1 success, got %d", entry.successes)
	}
}

func TestRecordOutcome_EmptyContext(t *testing.T) {
	learner := NewToolAffinityLearner()

	// Should not create entry for empty context
	learner.RecordOutcome("read_file", "", true)
	if len(learner.affinities) != 0 {
		t.Errorf("expected 0 entries for empty context, got %d", len(learner.affinities))
	}
}

func TestGetRecommendations_InsufficientData(t *testing.T) {
	learner := NewToolAffinityLearner()

	// Record only 2 outcomes (less than minSamples=3)
	learner.RecordOutcome("read_file", "read config", true)
	learner.RecordOutcome("read_file", "read config", true)

	recs := learner.GetRecommendations("read config", 3)
	if recs != nil {
		t.Errorf("expected nil recommendations with insufficient data, got %v", recs)
	}
}

func TestGetRecommendations_SufficientData(t *testing.T) {
	learner := NewToolAffinityLearner()

	// Record 3+ successes
	learner.RecordOutcome("read_file", "read config", true)
	learner.RecordOutcome("read_file", "read config", true)
	learner.RecordOutcome("read_file", "read config", true)

	recs := learner.GetRecommendations("read config", 3)
	if recs == nil {
		t.Fatal("expected recommendations with sufficient data")
	}
	if len(recs) == 0 {
		t.Fatal("expected at least 1 recommendation")
	}
	if recs[0].ToolName != "read_file" {
		t.Errorf("expected tool 'read_file', got '%s'", recs[0].ToolName)
	}
	if recs[0].Score <= 0 {
		t.Errorf("expected positive score, got %f", recs[0].Score)
	}
}

func TestGetRecommendations_EmptyContext(t *testing.T) {
	learner := NewToolAffinityLearner()

	recs := learner.GetRecommendations("", 3)
	if recs != nil {
		t.Errorf("expected nil for empty context, got %v", recs)
	}
}

func TestScoreDecay(t *testing.T) {
	learner := NewToolAffinityLearner()

	// Record successful outcomes
	for i := 0; i < 5; i++ {
		learner.RecordOutcome("read_file", "read config", true)
	}

	// Get initial score
	learner.mu.RLock()
	var initialScore float64
	for _, e := range learner.affinities {
		initialScore = e.score
		break
	}
	learner.mu.RUnlock()

	// Record more outcomes (triggers decay)
	for i := 0; i < 10; i++ {
		learner.RecordOutcome("grep", "search code", true)
	}

	// Check that score decayed
	learner.mu.RLock()
	var decayedScore float64
	for _, e := range learner.affinities {
		if e.toolName == "read_file" {
			decayedScore = e.score
			break
		}
	}
	learner.mu.RUnlock()

	if decayedScore >= initialScore {
		t.Errorf("expected score to decay, but went from %f to %f", initialScore, decayedScore)
	}
}

func TestMaxEntriesEviction(t *testing.T) {
	learner := NewToolAffinityLearner()
	learner.maxEntries = 5 // Small limit for testing

	// Record more entries than max
	for i := 0; i < 10; i++ {
		context := "context_" + string(rune('a'+i))
		learner.RecordOutcome("tool", context, true)
	}

	// Verify entries are capped
	if len(learner.affinities) > learner.maxEntries {
		t.Errorf("expected at most %d entries, got %d", learner.maxEntries, len(learner.affinities))
	}
}

func TestHashToolContext(t *testing.T) {
	h1 := hashToolContext("read_file", "read config")
	h2 := hashToolContext("read_file", "read config")
	h3 := hashToolContext("grep", "read config")
	h4 := hashToolContext("read_file", "write config")

	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
	if h1 == h3 {
		t.Error("different tool should produce different hash")
	}
	if h1 == h4 {
		t.Error("different context should produce different hash")
	}
}

func TestRecommendationConfidence(t *testing.T) {
	learner := NewToolAffinityLearner()

	// Record minimum samples for recommendations
	learner.RecordOutcome("read_file", "read config", true)
	learner.RecordOutcome("read_file", "read config", true)
	learner.RecordOutcome("read_file", "read config", true)

	recs := learner.GetRecommendations("read config", 1)
	if recs == nil || len(recs) == 0 {
		t.Fatal("expected recommendation")
	}

	// Confidence should be ~1.0 at exactly minSamples (allowing for precision)
	if recs[0].Confidence < 0.99 || recs[0].Confidence > 1.01 {
		t.Errorf("expected confidence≈1.0 at minSamples, got %f", recs[0].Confidence)
	}

	// Add more samples
	learner.RecordOutcome("read_file", "read config", true)
	learner.RecordOutcome("read_file", "read config", true)

	recs = learner.GetRecommendations("read config", 1)
	if recs == nil || len(recs) == 0 {
		t.Fatal("expected recommendation")
	}

	// Confidence should increase with more samples
	if recs[0].Confidence <= 1.0 {
		t.Errorf("expected confidence > 1.0 with more samples, got %f", recs[0].Confidence)
	}
}
