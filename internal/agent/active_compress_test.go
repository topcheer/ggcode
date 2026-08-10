package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		s1, s2   string
		expected float64
	}{
		{"identical", "hello world", "hello world", 1.0},
		{"completely different", "hello world", "foo bar baz", 0.0},
		{"partial overlap", "hello world test", "hello universe", 0.2}, // "hello" matches, 1 word shared out of 5 total
		{"empty strings", "", "", 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := similarity(tt.s1, tt.s2)
			// Allow some tolerance for floating point
			if got < tt.expected-0.1 || got > tt.expected+0.1 {
				t.Errorf("similarity() = %v, want ~%v", got, tt.expected)
			}
		})
	}
}

func TestDetectRepetition(t *testing.T) {
	a := &Agent{}

	tests := []struct {
		name     string
		messages []provider.ContentBlock
		wantMax  float64
	}{
		{
			name: "no repetition",
			messages: []provider.ContentBlock{
				{Type: "text", Text: "First message about coding"},
				{Type: "text", Text: "Second message about testing"},
				{Type: "text", Text: "Third message about deployment"},
			},
			wantMax: 0.5,
		},
		{
			name: "high repetition",
			messages: []provider.ContentBlock{
				{Type: "text", Text: "I'm trying to fix the bug in the code"},
				{Type: "text", Text: "I'm trying to fix the bug in the codebase"},
				{Type: "text", Text: "I'm trying to fix the bug in the repository"},
			},
			wantMax: 0.1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := a.detectRepetition(tt.messages)
			if got > tt.wantMax {
				t.Errorf("detectRepetition() = %v, should be <= %v", got, tt.wantMax)
			}
		})
	}
}

func TestCalculateCompressionScore(t *testing.T) {
	a := &Agent{}

	tests := []struct {
		name     string
		messages []provider.ContentBlock
		wantMin  float64
	}{
		{
			name:     "empty messages",
			messages: []provider.ContentBlock{},
			wantMin:  0.0,
		},
		{
			name: "small conversation",
			messages: []provider.ContentBlock{
				{Type: "text", Text: "Hello"},
				{Type: "text", Text: "Hi there"},
			},
			wantMin: 0.0,
		},
		{
			name:     "long conversation",
			messages: make([]provider.ContentBlock, 30),
			wantMin:  0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := a.calculateCompressionScore(tt.messages)
			if got < tt.wantMin {
				t.Errorf("calculateCompressionScore() = %v, want >= %v", got, tt.wantMin)
			}
		})
	}
}

func TestShouldTriggerCompression(t *testing.T) {
	a := &Agent{}
	config := defaultACCConfig()
	state := &accState{
		actionCount:      10,
		lastActionTime:   time.Now().Add(-1 * time.Hour),
		compressionCount: 0,
	}

	tests := []struct {
		name     string
		messages []provider.ContentBlock
		state    *accState
		want     bool
	}{
		{
			name: "trigger on long history",
			messages: func() []provider.ContentBlock {
				msgs := make([]provider.ContentBlock, 50) // More messages for higher score
				for i := range msgs {
					// Generate realistic text content to trigger compression
					msgs[i] = provider.ContentBlock{Type: "text", Text: "This is a message with substantial content that contributes to the context window size and compression score calculation."}
				}
				return msgs
			}(),
			state: state,
			want:  true,
		},
		{
			name: "no trigger on short history",
			messages: []provider.ContentBlock{
				{Type: "text", Text: "Hello"},
				{Type: "text", Text: "Hi"},
			},
			state: state,
			want:  false,
		},
		{
			name:     "no trigger due to max compressions",
			messages: make([]provider.ContentBlock, 30),
			state: &accState{
				actionCount:      10,
				lastActionTime:   time.Now().Add(-1 * time.Hour),
				compressionCount: 10,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := a.shouldTriggerCompression(tt.state, config, tt.messages)
			if got != tt.want {
				t.Errorf("shouldTriggerCompression() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompressHistory(t *testing.T) {
	a := &Agent{}

	messages := []provider.ContentBlock{
		{Type: "text", Text: "Fix the bug"},
		{Type: "tool_use", ToolName: "edit_file"},
		{Type: "text", Text: "Test the fix"},
		{Type: "tool_use", ToolName: "run_command"},
	}

	summary := a.compressHistory(messages)
	if summary == "" {
		t.Error("compressHistory() should not return empty summary for non-empty messages")
	}
	if !strings.Contains(summary, "COMPRESSED") {
		t.Error("compressHistory() should contain COMPRESSED marker")
	}
}

func TestExtractTopics(t *testing.T) {
	a := &Agent{}

	messages := []provider.ContentBlock{
		{Type: "text", Text: "There's a bug in the code"},
		{Type: "text", Text: "I'll fix the bug and write tests"},
		{Type: "text", Text: "Don't forget to test the performance"},
	}

	topics := a.extractTopics(messages)
	if len(topics) == 0 {
		t.Error("extractTopics() should extract some topics")
	}
}

func TestACCStatePersistence(t *testing.T) {
	a := &Agent{
		metadata: make(map[string]string),
	}

	state := &accState{
		compressionCount: 3,
		lastActionTime:   time.Now(),
	}

	a.saveACCState(state)

	// Create new agent and restore state
	a2 := &Agent{
		metadata: a.metadata,
	}
	restored := a2.getACCState()

	if restored.compressionCount != 3 {
		t.Errorf("Expected compressionCount 3, got %d", restored.compressionCount)
	}
}
