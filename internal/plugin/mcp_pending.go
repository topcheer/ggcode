package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcp"
)

// #5699: MCP deferred-answer surfaces. Esc on an input_required round queues
// a pending answer inside the mcp.Client; these methods aggregate them across
// servers, route answers, and keep the answered-result digests the agent
// injects into the following turn.

const maxAnsweredPendingDigests = 16

type answeredPending struct {
	ID      string
	Server  string
	Tool    string
	Summary string
	At      time.Time
}

// PendingInputAnswers lists this server's live pending answers.
func (m *MCPPlugin) PendingInputAnswers() []mcp.PendingAnswer {
	m.mu.RLock()
	client := m.client
	m.mu.RUnlock()
	if client == nil {
		return nil
	}
	return client.ListPendingAnswers()
}

// AnswerPendingInput answers a pending answer on this server's client.
func (m *MCPPlugin) AnswerPendingInput(ctx context.Context, id string, responses map[string]json.RawMessage) (*mcp.ResumedAnswer, error) {
	m.mu.RLock()
	client := m.client
	m.mu.RUnlock()
	if client == nil {
		return nil, fmt.Errorf("mcp server %q is not connected", m.cfg.Name)
	}
	return client.AnswerPendingInput(ctx, id, responses)
}

// DiscardPendingInput drops a pending answer on this server's client.
func (m *MCPPlugin) DiscardPendingInput(id string) bool {
	m.mu.RLock()
	client := m.client
	m.mu.RUnlock()
	if client == nil {
		return false
	}
	return client.DiscardPendingInput(id)
}

func (m *MCPManager) snapshotPlugins() []*MCPPlugin {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]*MCPPlugin(nil), m.plugins...)
}

// PendingInputAnswers aggregates live pending answers across servers,
// oldest first.
func (m *MCPManager) PendingInputAnswers() []mcp.PendingAnswer {
	var all []mcp.PendingAnswer
	for _, p := range m.snapshotPlugins() {
		all = append(all, p.PendingInputAnswers()...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].CreatedAt != all[j].CreatedAt {
			return all[i].CreatedAt < all[j].CreatedAt
		}
		return all[i].ID < all[j].ID
	})
	return all
}

// PendingMCPAnswerCount reports the total live deferred interactions.
func (m *MCPManager) PendingMCPAnswerCount() int {
	n := 0
	for _, p := range m.snapshotPlugins() {
		for range p.PendingInputAnswers() {
			n++
		}
	}
	return n
}

// PendingMCPAnswerLines renders one line per pending answer for the
// model-facing notice.
func (m *MCPManager) PendingMCPAnswerLines() []string {
	now := time.Now()
	var lines []string
	for _, pa := range m.PendingInputAnswers() {
		tool := pa.ToolName
		if tool == "" {
			tool = pa.Method
		}
		line := fmt.Sprintf("%s on server '%s' (tool %s, queued %s ago, expires in %s)",
			pa.ID, pa.Server, tool, formatPendingAge(pa, now), formatPendingTTL(pa.ExpiresIn(now)))
		if msg := strings.TrimSpace(pa.StatusMessage); msg != "" {
			line += ": " + truncatePendingMessage(msg, 120)
		}
		lines = append(lines, line)
	}
	return lines
}

// AnswerPendingInput routes an answer to the server holding the pending ID
// and records the resumed outcome digest for the next model turn.
func (m *MCPManager) AnswerPendingInput(ctx context.Context, id string, responses map[string]json.RawMessage) (*mcp.ResumedAnswer, error) {
	for _, p := range m.snapshotPlugins() {
		for _, pa := range p.PendingInputAnswers() {
			if pa.ID == id {
				res, err := p.AnswerPendingInput(ctx, id, responses)
				if err != nil {
					m.recordAnsweredPending(pa, "answer failed: "+err.Error())
					return nil, err
				}
				m.recordAnsweredPending(pa, m.summarizeResumed(pa, res))
				return res, nil
			}
		}
	}
	return nil, fmt.Errorf("no pending MCP answer %q across connected servers; it may have expired or been discarded", id)
}

// DiscardPendingInput drops a pending answer on whichever server holds it.
func (m *MCPManager) DiscardPendingInput(id string) bool {
	for _, p := range m.snapshotPlugins() {
		for _, pa := range p.PendingInputAnswers() {
			if pa.ID == id {
				return p.DiscardPendingInput(id)
			}
		}
	}
	return false
}

func (m *MCPManager) summarizeResumed(pa mcp.PendingAnswer, res *mcp.ResumedAnswer) string {
	tool := pa.ToolName
	if tool == "" {
		tool = pa.Method
	}
	if res == nil {
		return "answered"
	}
	if res.Requeued != nil {
		return fmt.Sprintf("answered; server asked a follow-up question - new pending answer %s queued", res.Requeued.ID)
	}
	if res.Result != nil {
		summary := mcp.SummarizeCallToolResult(res.Result, 600)
		if summary == "" {
			summary = "(empty tool result)"
		}
		return "resumed result: " + summary
	}
	if len(res.Raw) > 0 {
		raw := string(res.Raw)
		if len(raw) > 600 {
			raw = raw[:600] + "…"
		}
		return "resumed result: " + raw
	}
	return "answered"
}

func (m *MCPManager) recordAnsweredPending(pa mcp.PendingAnswer, summary string) {
	tool := pa.ToolName
	if tool == "" {
		tool = pa.Method
	}
	m.answeredMu.Lock()
	m.answered = append(m.answered, answeredPending{
		ID:      pa.ID,
		Server:  pa.Server,
		Tool:    tool,
		Summary: summary,
		At:      time.Now(),
	})
	if len(m.answered) > maxAnsweredPendingDigests {
		m.answered = m.answered[len(m.answered)-maxAnsweredPendingDigests:]
	}
	m.answeredMu.Unlock()
	debug.Log("mcp-pending", "server=%s pending=%s answered (%s)", pa.Server, pa.ID, tool)
}

// TakeAnsweredMCPAnswerLines drains the one-shot digests of answered
// interactions; the agent injects them into exactly one following turn.
func (m *MCPManager) TakeAnsweredMCPAnswerLines() []string {
	m.answeredMu.Lock()
	pending := m.answered
	m.answered = nil
	m.answeredMu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	lines := make([]string, 0, len(pending))
	for _, ap := range pending {
		lines = append(lines, fmt.Sprintf(
			"[mcp] The user answered deferred MCP interaction %s (server '%s', tool %s): %s",
			ap.ID, ap.Server, ap.Tool, ap.Summary))
	}
	return lines
}

func formatPendingAge(pa mcp.PendingAnswer, now time.Time) string {
	created, err := time.Parse(time.RFC3339, pa.CreatedAt)
	if err != nil {
		return "unknown"
	}
	d := now.Sub(created)
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func formatPendingTTL(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func truncatePendingMessage(msg string, limit int) string {
	if len(msg) <= limit {
		return msg
	}
	return msg[:limit] + "…"
}
