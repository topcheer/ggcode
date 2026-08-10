package a2a

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// Proposal represents a bid from an agent to handle a task.
type Proposal struct {
	AgentID       string        `json:"agentId"`
	AgentName     string        `json:"agentName"`
	EstimatedTime time.Duration `json:"estimatedTime"`
	Cost          float64       `json:"cost"`
	QualityScore  float64       `json:"qualityScore"` // 0.0-1.0
	Load          float64       `json:"load"`         // 0.0-1.0, current agent load
	Timestamp     time.Time     `json:"timestamp"`
	Confidence    float64       `json:"confidence"` // 0.0-1.0, how confident in estimate
}

// Score calculates a composite score for this proposal based on weighted criteria.
// Higher scores are better.
func (p *Proposal) Score(weights ProposalWeights) float64 {
	// Normalize time: lower is better, max reasonable time 10 minutes
	timeNorm := 1.0 - math.Min(float64(p.EstimatedTime.Minutes())/10.0, 1.0)

	// Normalize cost: lower is better, max reasonable cost $10
	costNorm := 1.0 - math.Min(p.Cost/10.0, 1.0)

	// Quality and confidence: higher is better, already 0-1
	// Load: lower is better
	loadNorm := 1.0 - p.Load

	// Weighted sum
	return weights.Time*timeNorm +
		weights.Cost*costNorm +
		weights.Quality*p.QualityScore +
		weights.Load*loadNorm +
		weights.Confidence*p.Confidence
}

// ProposalWeights defines the importance of each proposal dimension.
type ProposalWeights struct {
	Time       float64
	Cost       float64
	Quality    float64
	Load       float64
	Confidence float64
}

// DefaultWeights returns a balanced weight configuration.
func DefaultWeights() ProposalWeights {
	return ProposalWeights{
		Time:       0.25,
		Cost:       0.25,
		Quality:    0.25,
		Load:       0.15,
		Confidence: 0.10,
	}
}

// CostOptimizedWeights prioritizes cost and load.
func CostOptimizedWeights() ProposalWeights {
	return ProposalWeights{
		Time:       0.15,
		Cost:       0.40,
		Quality:    0.20,
		Load:       0.15,
		Confidence: 0.10,
	}
}

// QualityOptimizedWeights prioritizes quality and confidence.
func QualityOptimizedWeights() ProposalWeights {
	return ProposalWeights{
		Time:       0.15,
		Cost:       0.15,
		Quality:    0.40,
		Load:       0.15,
		Confidence: 0.15,
	}
}

// SpeedOptimizedWeights prioritizes time.
func SpeedOptimizedWeights() ProposalWeights {
	return ProposalWeights{
		Time:       0.50,
		Cost:       0.15,
		Quality:    0.15,
		Load:       0.10,
		Confidence: 0.10,
	}
}

// Negotiator manages the Agent Negotiation Protocol (ANP) flow.
type Negotiator struct {
	clients map[string]*Client
	weights ProposalWeights
	timeout time.Duration
	mu      sync.RWMutex
}

// NewNegotiator creates a new ANP negotiator.
func NewNegotiator(clients map[string]*Client) *Negotiator {
	return &Negotiator{
		clients: clients,
		weights: DefaultWeights(),
		timeout: 30 * time.Second,
	}
}

// SetWeights updates the proposal evaluation weights.
func (n *Negotiator) SetWeights(w ProposalWeights) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.weights = w
}

// SetTimeout updates the proposal collection timeout.
func (n *Negotiator) SetTimeout(d time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.timeout = d
}

// Negotiate executes the full ANP flow.
func (n *Negotiator) Negotiate(ctx context.Context, skill, description string) (*NegotiationResult, error) {
	start := time.Now()
	result := &NegotiationResult{
		Proposals: make([]Proposal, 0),
		Errors:    make(map[string]error),
	}

	n.mu.RLock()
	agentIDs := make([]string, 0, len(n.clients))
	for id := range n.clients {
		agentIDs = append(agentIDs, id)
	}
	n.mu.RUnlock()

	if len(agentIDs) == 0 {
		return result, fmt.Errorf("no agents registered for negotiation")
	}

	// Collect proposals concurrently
	var wg sync.WaitGroup
	var mu sync.Mutex
	proposalCh := make(chan Proposal, len(agentIDs))

	for _, agentID := range agentIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			proposal, err := n.requestProposal(ctx, id, skill, description)
			if err != nil {
				mu.Lock()
				result.Errors[id] = err
				mu.Unlock()
				return
			}
			proposalCh <- *proposal
		}(agentID)
	}

	// Wait for all proposals
	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	// Collect proposals with timeout
	proposalCtx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()

collectLoop:
	for {
		select {
		case proposal := <-proposalCh:
			mu.Lock()
			result.Proposals = append(result.Proposals, proposal)
			mu.Unlock()
		case <-doneCh:
			break collectLoop
		case <-proposalCtx.Done():
			result.TimedOut = true
			break collectLoop
		}
	}

	// Select best proposal
	if len(result.Proposals) == 0 {
		result.Duration = time.Since(start)
		return result, fmt.Errorf("negotiation failed: no valid proposals received (agents: %d, errors: %d)",
			len(agentIDs), len(result.Errors))
	}

	bestProposal, bestScore := n.selectBest(result.Proposals)
	result.Selected = &bestProposal
	result.Score = bestScore
	result.Duration = time.Since(start)

	return result, nil
}

// requestProposal asks a specific agent for a proposal.
func (n *Negotiator) requestProposal(ctx context.Context, agentID, skill, description string) (*Proposal, error) {
	n.mu.RLock()
	client, ok := n.clients[agentID]
	n.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("agent not registered: %s", agentID)
	}

	// Get agent card for metadata
	card := client.Card()
	agentName := agentID
	if card != nil {
		agentName = card.Name
	}

	// Generate a synthetic proposal based on agent metadata
	// In production, this would be an actual RPC call to the agent
	proposal := &Proposal{
		AgentID:       agentID,
		AgentName:     agentName,
		EstimatedTime: 5 * time.Minute,
		Cost:          1.0,
		QualityScore:  0.8,
		Load:          0.3,
		Timestamp:     time.Now(),
		Confidence:    0.7,
	}

	if card != nil && card.Skills != nil {
		for _, s := range card.Skills {
			if s.ID == skill {
				proposal.QualityScore = 0.85 + (float64(len(s.Description)%10))/100.0
				proposal.Confidence = 0.8
				break
			}
		}
	}

	return proposal, nil
}

// selectBest picks the highest-scoring proposal.
func (n *Negotiator) selectBest(proposals []Proposal) (Proposal, float64) {
	if len(proposals) == 0 {
		return Proposal{}, 0
	}

	best := proposals[0]
	bestScore := best.Score(n.weights)

	for _, p := range proposals[1:] {
		score := p.Score(n.weights)
		if score > bestScore {
			best = p
			bestScore = score
		}
	}

	return best, bestScore
}

// SortProposals returns proposals sorted by score (highest first).
func (n *Negotiator) SortProposals(proposals []Proposal) []Proposal {
	sorted := make([]Proposal, len(proposals))
	copy(sorted, proposals)

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Score(n.weights) > sorted[j].Score(n.weights)
	})

	return sorted
}

// AddAgent dynamically adds an agent.
func (n *Negotiator) AddAgent(agentID string, client *Client) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.clients[agentID] = client
}

// RemoveAgent removes an agent.
func (n *Negotiator) RemoveAgent(agentID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.clients, agentID)
}

// AgentCount returns the number of registered agents.
func (n *Negotiator) AgentCount() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.clients)
}

// ListAgents returns all registered agent IDs.
func (n *Negotiator) ListAgents() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()

	ids := make([]string, 0, len(n.clients))
	for id := range n.clients {
		ids = append(ids, id)
	}
	return ids
}

// NegotiationResult contains the outcome of a negotiation round.
type NegotiationResult struct {
	Proposals []Proposal
	Selected  *Proposal
	Score     float64
	Duration  time.Duration
	Errors    map[string]error
	TimedOut  bool
}
