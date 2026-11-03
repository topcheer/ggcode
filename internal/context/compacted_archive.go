package context

import (
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

const maxCompactedArchiveBytes = 1 << 20

type CompactedEntry struct {
	Role   string
	ToolID string
	Reason string
	Text   string
}

type CompactedHit struct {
	Role    string
	ToolID  string
	Reason  string
	Excerpt string
}

const excerptWindow = 200

func (m *Manager) archiveCompactedMessages(msgs []provider.Message) {
	for _, msg := range msgs {
		if msg.Role == "system" {
			continue
		}
		var b strings.Builder
		for j := range msg.Content {
			blk := &msg.Content[j]
			switch blk.Type {
			case "text":
				b.WriteString(blk.Text)
			case "tool_result":
				if blk.Output != "" {
					if b.Len() > 0 {
						b.WriteByte('\n')
					}
					b.WriteString(blk.Output)
				}
			}
		}
		text := strings.TrimSpace(b.String())
		if text == "" {
			continue
		}
		m.compactedArchive = append(m.compactedArchive, CompactedEntry{
			Role:   msg.Role,
			Reason: "summarized",
			Text:   text,
		})
	}
	m.trimCompactedArchiveLocked()
}

func (m *Manager) archiveSupersededToolOutput(role, toolID, reason, output string) {
	if strings.TrimSpace(output) == "" {
		return
	}
	m.compactedArchive = append(m.compactedArchive, CompactedEntry{
		Role:   role,
		ToolID: toolID,
		Reason: reason,
		Text:   output,
	})
	m.trimCompactedArchiveLocked()
}

func (m *Manager) trimCompactedArchiveLocked() {
	total := 0
	for i := range m.compactedArchive {
		total += len(m.compactedArchive[i].Text)
	}
	if total <= maxCompactedArchiveBytes {
		return
	}
	for total > maxCompactedArchiveBytes && len(m.compactedArchive) > 0 {
		total -= len(m.compactedArchive[0].Text)
		m.compactedArchive = m.compactedArchive[1:]
	}
}

func (m *Manager) SearchCompactedArchive(query string, limit int) []CompactedHit {
	q := strings.TrimSpace(query)
	if q == "" || limit <= 0 {
		return nil
	}
	lower := strings.ToLower(q)
	m.mu.Lock()
	defer m.mu.Unlock()
	var hits []CompactedHit
	for i := len(m.compactedArchive) - 1; i >= 0 && len(hits) < limit; i-- {
		e := &m.compactedArchive[i]
		idx := strings.Index(strings.ToLower(e.Text), lower)
		if idx < 0 {
			continue
		}
		start := idx - excerptWindow
		if start < 0 {
			start = 0
		}
		end := idx + len(q) + excerptWindow
		if end > len(e.Text) {
			end = len(e.Text)
		}
		hits = append(hits, CompactedHit{
			Role:    e.Role,
			ToolID:  e.ToolID,
			Reason:  e.Reason,
			Excerpt: e.Text[start:end],
		})
	}
	return hits
}

func (m *Manager) CompactedArchiveSize() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.compactedArchive)
}
