package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestSpiralHallucination_Reset(t *testing.T) {
	s := newSpiralHallucinationState()
	s.turn = 5
	s.warnings = 1
	s.topics = append(s.topics, spiralTrackedTopic{word: "postgres", sourceTurn: 1})
	s.committedCounts["postgres"] = 3
	s.verified = true

	s.reset()

	if s.turn != 0 || s.warnings != 0 || len(s.topics) != 0 {
		t.Fatalf("reset did not clear state: %+v", s)
	}
	if len(s.committedCounts) != 0 {
		t.Fatalf("reset did not clear committedCounts: %+v", s.committedCounts)
	}
	if s.verified {
		t.Fatal("reset did not clear verified")
	}
}

func TestExtractSpiralTopics(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		minCount int
		mustHave []string
	}{
		{
			name:     "assumption about database",
			text:     "I assume the database is PostgreSQL with connection pooling enabled",
			minCount: 1,
			mustHave: []string{"postgresql", "connection"},
		},
		{
			name:     "hedging about config",
			text:     "I think the default port is probably 3000 for this framework",
			minCount: 1,
			mustHave: []string{"default", "port"},
		},
		{
			name:     "no uncertainty markers",
			text:     "The database is PostgreSQL. The port is 3000.",
			minCount: 0,
		},
		{
			name:     "empty text",
			text:     "",
			minCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topics := extractSpiralTopics(tt.text)
			if len(topics) < tt.minCount {
				t.Fatalf("expected at least %d topics, got %d: %v", tt.minCount, len(topics), topics)
			}
			lowerTopics := make(map[string]bool)
			for _, tp := range topics {
				lowerTopics[tp] = true
			}
			for _, must := range tt.mustHave {
				if !lowerTopics[must] {
					t.Fatalf("expected topic %q in results: %v", must, topics)
				}
			}
		})
	}
}

func TestExtractSpiralTopics_DedupAndLimit(t *testing.T) {
	text := "I assume postgresql postgresql postgresql config config docker redis graphql webpack vite"
	topics := extractSpiralTopics(text)

	seen := make(map[string]bool)
	for _, tp := range topics {
		if seen[tp] {
			t.Fatalf("duplicate topic found: %s", tp)
		}
		seen[tp] = true
	}

	if len(topics) > spiralMaxTopics {
		t.Fatalf("too many topics: %d > %d", len(topics), spiralMaxTopics)
	}
}

func TestDetectCommittedTopics(t *testing.T) {
	tracked := []spiralTrackedTopic{
		{word: "postgresql", sourceTurn: 1},
		{word: "redis", sourceTurn: 1},
	}

	tests := []struct {
		name        string
		text        string
		tracked     []spiralTrackedTopic
		mustHave    []string
		mustNotHave []string
	}{
		{
			name:     "committed about prior topic",
			text:     "Since we're using PostgreSQL, the connection string needs updating",
			tracked:  tracked,
			mustHave: []string{"postgresql"},
		},
		{
			name:     "committed about multiple prior topics",
			text:     "Given that PostgreSQL is our database and Redis handles caching, we need to configure both",
			tracked:  tracked,
			mustHave: []string{"postgresql", "redis"},
		},
		{
			name:        "no committed language",
			text:        "The PostgreSQL database is large",
			tracked:     tracked,
			mustNotHave: []string{"postgresql"},
		},
		{
			name:        "still hedging about topic",
			text:        "I think the PostgreSQL config might need updating, probably",
			tracked:     tracked,
			mustNotHave: []string{"postgresql"},
		},
		{
			name:        "no tracked topics",
			text:        "Since we're using PostgreSQL",
			tracked:     nil,
			mustNotHave: []string{"postgresql"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched := detectCommittedTopics(tt.text, tt.tracked)
			matchedMap := make(map[string]bool)
			for _, m := range matched {
				matchedMap[m] = true
			}
			for _, must := range tt.mustHave {
				if !matchedMap[must] {
					t.Fatalf("expected %q in matched topics: %v", must, matched)
				}
			}
			for _, mustNot := range tt.mustNotHave {
				if matchedMap[mustNot] {
					t.Fatalf("did not expect %q in matched topics: %v", mustNot, matched)
				}
			}
		})
	}
}

func TestRecordSpiralTurn_UncertaintyRecording(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}

	// Turn 1: express uncertainty
	a.recordSpiralTurn("I assume the database is PostgreSQL with connection pooling")
	if len(a.spiralState.topics) == 0 {
		t.Fatal("expected topics to be recorded after uncertainty turn")
	}
	if a.spiralState.verified {
		t.Fatal("should not be verified after uncertainty turn")
	}
}

func TestRecordSpiralTurn_VerificationBreaksSpiral(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}

	// Turn 1: express uncertainty
	a.recordSpiralTurn("I assume the database is PostgreSQL")
	if a.spiralState.verified {
		t.Fatal("should not be verified yet")
	}

	// Turn 2: verification occurs (test passed)
	a.recordSpiralTurn("I ran the test and it passed. The build succeeded.")
	if !a.spiralState.verified {
		t.Fatal("should be verified after test/build turn")
	}
}

func TestMaybeWarnSpiralHallucination_SpiralPattern(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}

	// Turn 1: express uncertainty about PostgreSQL
	a.recordSpiralTurn("I assume the database is PostgreSQL with some specific config")

	// Turn 2: start committing (first committed statement)
	a.recordSpiralTurn("Since we're using PostgreSQL, the schema needs migration")

	// Should not fire yet (only 1 committed statement)
	hint := a.maybeWarnSpiralHallucination()
	if hint != "" {
		t.Fatal("should not warn after only 1 committed statement")
	}

	// Turn 3: more commitment (second committed statement)
	a.recordSpiralTurn("Given that PostgreSQL is our database, the connection pool needs tuning")

	// Now should fire (2 committed statements)
	hint = a.maybeWarnSpiralHallucination()
	if hint == "" {
		t.Fatal("expected spiral warning after 2 committed statements on uncertain topic")
	}

	if !strings.Contains(hint, "spiral-of-hallucination") {
		t.Fatalf("hint should contain detector tag: %s", hint)
	}
	if !strings.Contains(hint, "postgresql") {
		t.Fatalf("hint should mention spiraled topic: %s", hint)
	}

	// Should not fire again (max 1 warning)
	hint2 := a.maybeWarnSpiralHallucination()
	if hint2 != "" {
		t.Fatal("should not warn again after max warnings reached")
	}
}

func TestMaybeWarnSpiralHallucination_VerificationPrevents(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}

	// Turn 1: express uncertainty
	a.recordSpiralTurn("I assume the framework is Django")

	// Turn 2: verify (breaks spiral)
	a.recordSpiralTurn("I ran the test suite and confirmed the framework. Build passed.")

	// Turn 3: committed assertion (but verified)
	a.recordSpiralTurn("Since we're using Django, the models need updating")

	// Turn 4: another committed assertion
	a.recordSpiralTurn("Given that Django is our framework, the ORM needs configuration")

	// Should not fire because verification broke the spiral chain
	// (unless 3+ topics spiraled, which is not the case here)
	hint := a.maybeWarnSpiralHallucination()
	if hint != "" {
		t.Fatal("should not warn when verification broke the spiral for a single topic")
	}
}

func TestMaybeWarnSpiralHallucination_NoTopicsTracked(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}

	// No uncertainty expressed, just committed language
	a.recordSpiralTurn("Since we're using PostgreSQL, the config needs updating")
	a.recordSpiralTurn("Given that PostgreSQL is our database, we need migrations")

	hint := a.maybeWarnSpiralHallucination()
	if hint != "" {
		t.Fatal("should not warn when no prior uncertainty was tracked")
	}
}

func TestMaybeWarnSpiralHallucination_NilState(t *testing.T) {
	a := &Agent{spiralState: nil}
	hint := a.maybeWarnSpiralHallucination()
	if hint != "" {
		t.Fatal("should return empty when state is nil")
	}
}

func TestRecordSpiralTurn_NilState(t *testing.T) {
	a := &Agent{spiralState: nil}
	// Should not panic
	a.recordSpiralTurn("some text")
}

func TestSpiralVerificationRegex(t *testing.T) {
	// #161: generic narrative mentions of test/build/error/result must NOT
	// disable the detector — only explicit first-person verification
	// assertions count (real tool calls are the primary signal, recorded
	// via recordSpiralVerification from the tool execution loop).
	tests := []struct {
		text    string
		matches bool
	}{
		{"I ran the test and it passed", true},
		{"I have verified the fix works", true},
		{"I ran the build to check", true},
		{"The build failed with an error", false},
		{"Let me verify this assumption", false},
		{"The diagnostic output shows the issue", false},
		{"I compiled the code successfully", false},
		{"The result confirms our hypothesis", false},
		{"Tests passed for the new module", false},
		{"Let's proceed with the implementation", false},
		{"Since we're using PostgreSQL", false},
	}

	for _, tt := range tests {
		got := spiralVerificationRe.MatchString(tt.text)
		if got != tt.matches {
			t.Fatalf("verification regex for %q: expected %v, got %v", tt.text, tt.matches, got)
		}
	}
}

func TestSpiralMaxTopicsEnforced(t *testing.T) {
	// Generate text with many distinct topics after an uncertainty marker
	words := []string{}
	for i := 0; i < 50; i++ {
		words = append(words, fmt.Sprintf("topic%d", i))
	}
	text := "I assume " + strings.Join(words, " ")
	topics := extractSpiralTopics(text)
	if len(topics) > spiralMaxTopics {
		t.Fatalf("expected at most %d topics, got %d", spiralMaxTopics, len(topics))
	}
}

// TestSpiralVerification_ReadOnlyToolsNotCounted pins fix #167: only
// execution-type tools count as verification; read-only tools must not
// re-silence the detector.
func TestSpiralVerification_ReadOnlyToolsNotCounted(t *testing.T) {
	for _, ro := range []string{"read_file", "grep", "glob", "list_directory"} {
		if spiralExecutionTools[ro] {
			t.Fatalf("read-only tool %q must not be in spiralExecutionTools", ro)
		}
	}
	for _, ex := range []string{"run_command", "start_command", "browser"} {
		if !spiralExecutionTools[ex] {
			t.Fatalf("execution tool %q must be in spiralExecutionTools", ex)
		}
	}
}
