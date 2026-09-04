package provider

import (
	"errors"
	"testing"
)

func TestMaxTokensRejection(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantRejected bool
		wantLimit    int
	}{
		{"nil error", nil, false, 0},
		{"unrelated error", errors.New("connection refused"), false, 0},
		{"context window (not output)", errors.New("context window exceeded"), false, 0},
		{"max_tokens too large", errors.New("max_tokens is too large, must be at most 4096"), true, 4096},
		{"max output tokens", errors.New("max output tokens must be <= 16384"), true, 16384},
		// #577(G): "maximum is" collides with the IsContextOverflowError word
		// list (#303), so this row uses the "at most" phrasing to test genuine
		// output-cap rejection without tripping the overflow exemption.
		{"max_completion_tokens", errors.New("max_completion_tokens too large, at most 2048"), true, 2048},
		{"max_tokens but not too large", errors.New("max_tokens must be > 0"), false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rejected, limit := maxTokensRejection(tt.err)
			if rejected != tt.wantRejected {
				t.Errorf("rejected = %v, want %v", rejected, tt.wantRejected)
			}
			if limit != tt.wantLimit {
				t.Errorf("limit = %d, want %d", limit, tt.wantLimit)
			}
		})
	}
}

func TestParseLimitFromMessage(t *testing.T) {
	tests := []struct {
		msg  string
		want int
	}{
		{"must be at most 4096", 4096},
		{"must be <= 8192 tokens", 8192},
		{"maximum of 16384", 16384},
		{"maximum is 2048", 2048},
		{"no number here", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := parseLimitFromMessage(tt.msg)
		if got != tt.want {
			t.Errorf("parseLimitFromMessage(%q) = %d, want %d", tt.msg, got, tt.want)
		}
	}
}

func TestAdaptiveCapString(t *testing.T) {
	var c *adaptiveCap
	if c.String() != "<nil>" {
		t.Errorf("expected '<nil>' for nil cap, got %q", c.String())
	}
}

func TestAdaptiveCapString_NonNil(t *testing.T) {
	c := &adaptiveCap{key: "test", lo: 100, hi: 4096}
	c.cur.Store(2048)
	s := c.String()
	if s == "" {
		t.Error("expected non-empty string")
	}
}

// TestCloneWithModelCapIsolation pins #1603: a clone on a DIFFERENT model
// must not share the parent's adaptive cap - the registry keys caps on
// (vendor, baseURL, model) precisely so learned values never mix, but the
// clone sites used to copy the pointer wholesale (a smaller-limit clone
// inherited the parent's learned 8000 cap, and its rejections wrote back
// through the shared pointer, dragging the parent down).
func TestCloneWithModelCapIsolation(t *testing.T) {
	parent := AdaptiveCapFor("testvendor", "https://t.example", "model-a", 8000)
	parent.OnRejected(9000) // learned: 9000 is too big for model-a

	swapped := AdaptiveCapForModelSwap(parent, "model-b", 4000)
	if swapped == parent {
		t.Fatal("model swap returned the SAME cap instance")
	}
	if swapped.key == parent.key {
		t.Fatalf("cap key not re-keyed for the new model: %q", swapped.key)
	}
	// A rejection on the clone must not touch the parent's bounds.
	swapped.OnRejected(100)
	parent.mu.Lock()
	hi := parent.hi
	parent.mu.Unlock()
	if hi != 9000 {
		t.Fatalf("parent hi polluted by clone rejection: %d", hi)
	}
}
