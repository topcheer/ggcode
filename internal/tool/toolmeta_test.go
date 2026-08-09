package tool

import (
	"testing"
)

// TestToolMeta verifies that tools implementing MetaProvider return valid metadata.
func TestToolMeta(t *testing.T) {
	r := registerTestTools(t)

	tests := []struct {
		name           string
		expectedCost   float64
		expectedRPS    float64
		expectedPolicy string
	}{
		{"web_search", 0.0001, 2.0, "exponential"},
		{"web_fetch", 0.0001, 5.0, "exponential"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, ok := r.GetMeta(tt.name)
			if !ok {
				t.Fatalf("tool %q should provide metadata", tt.name)
			}
			if meta.CostEstimate != tt.expectedCost {
				t.Errorf("CostEstimate = %v, want %v", meta.CostEstimate, tt.expectedCost)
			}
			if meta.RateLimitRPS != tt.expectedRPS {
				t.Errorf("RateLimitRPS = %v, want %v", meta.RateLimitRPS, tt.expectedRPS)
			}
			if meta.RetryPolicy != tt.expectedPolicy {
				t.Errorf("RetryPolicy = %q, want %q", meta.RetryPolicy, tt.expectedPolicy)
			}
		})
	}
}

// TestToolMeta_NotProvided verifies that tools without MetaProvider return false.
func TestToolMeta_NotProvided(t *testing.T) {
	r := registerTestTools(t)
	// read_file is a local tool that shouldn't provide metadata
	meta, ok := r.GetMeta("read_file")
	if ok {
		t.Errorf("read_file should not provide metadata, got %+v", meta)
	}
	if meta != (ToolMeta{}) {
		t.Errorf("expected zero-value ToolMeta, got %+v", meta)
	}
}
