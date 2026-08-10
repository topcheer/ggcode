package a2a

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestProposalScore(t *testing.T) {
	weights := DefaultWeights()

	tests := []struct {
		name     string
		proposal Proposal
		wantMin  float64
		wantMax  float64
	}{
		{
			name: "high quality, fast, cheap",
			proposal: Proposal{
				EstimatedTime: 1 * time.Minute,
				Cost:          0.5,
				QualityScore:  0.95,
				Load:          0.1,
				Confidence:    0.9,
			},
			wantMin: 0.7,
			wantMax: 1.0,
		},
		{
			name: "low quality, slow, expensive",
			proposal: Proposal{
				EstimatedTime: 10 * time.Minute,
				Cost:          10.0,
				QualityScore:  0.5,
				Load:          0.9,
				Confidence:    0.5,
			},
			wantMin: 0.0,
			wantMax: 0.3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := tt.proposal.Score(weights)
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("Proposal.Score() = %v, want in [%v, %v]", score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestNegotiatorBasic(t *testing.T) {
	clients := make(map[string]*Client)
	for i := 1; i <= 3; i++ {
		clients[string(rune('A'+i-1))] = &Client{}
	}

	negotiator := NewNegotiator(clients)

	ctx := context.Background()
	result, err := negotiator.Negotiate(ctx, "code-edit", "Fix bug in handler.go")

	if err != nil {
		t.Errorf("Negotiate() error = %v", err)
		return
	}

	if result == nil {
		t.Fatal("Negotiate() returned nil result")
	}

	if result.Selected == nil {
		t.Error("No proposal was selected")
	}

	if result.Duration <= 0 {
		t.Error("Duration should be positive")
	}
}

func TestNegotiatorSelectBest(t *testing.T) {
	weights := DefaultWeights()

	proposals := []Proposal{
		{AgentID: "A", EstimatedTime: 5 * time.Minute, Cost: 2.0, QualityScore: 0.8, Load: 0.5, Confidence: 0.7},
		{AgentID: "B", EstimatedTime: 1 * time.Minute, Cost: 0.5, QualityScore: 0.9, Load: 0.2, Confidence: 0.9},
		{AgentID: "C", EstimatedTime: 10 * time.Minute, Cost: 5.0, QualityScore: 0.6, Load: 0.8, Confidence: 0.5},
	}

	n := &Negotiator{weights: weights}
	best, score := n.selectBest(proposals)

	if best.AgentID != "B" {
		t.Errorf("Selected agent = %v, want B", best.AgentID)
	}

	if score <= 0 {
		t.Errorf("Score = %v, want > 0", score)
	}
}

func TestNegotiatorConcurrentNegotiations(t *testing.T) {
	clients := make(map[string]*Client)
	for i := 0; i < 5; i++ {
		clients[string(rune('A'+i))] = &Client{}
	}

	negotiator := NewNegotiator(clients)
	ctx := context.Background()

	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := negotiator.Negotiate(ctx, "test", "Concurrent test")
			if err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	errorCount := 0
	for err := range errors {
		t.Logf("Concurrent negotiation error: %v", err)
		errorCount++
	}

	if errorCount > 0 {
		t.Errorf("%d concurrent negotiations failed", errorCount)
	}
}

func TestNegotiatorAgentManagement(t *testing.T) {
	negotiator := NewNegotiator(map[string]*Client{})

	if negotiator.AgentCount() != 0 {
		t.Errorf("Initial agent count = %v, want 0", negotiator.AgentCount())
	}

	agentA := &Client{}
	negotiator.AddAgent("A", agentA)

	if negotiator.AgentCount() != 1 {
		t.Errorf("Agent count after add = %v, want 1", negotiator.AgentCount())
	}

	negotiator.RemoveAgent("A")

	if negotiator.AgentCount() != 0 {
		t.Errorf("Agent count after remove = %v, want 0", negotiator.AgentCount())
	}
}

func TestDefaultWeightsSane(t *testing.T) {
	w := DefaultWeights()

	values := []float64{w.Time, w.Cost, w.Quality, w.Load, w.Confidence}
	minVal := 1.0
	maxVal := 0.0

	for _, v := range values {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}

	maxDiff := maxVal - minVal

	if maxDiff > 0.2 {
		t.Errorf("Default weights are unbalanced: max diff = %v", maxDiff)
	}
}
