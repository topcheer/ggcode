package context

// recall_compacted: post-compaction re-retrieval archive.
//
// Research basis: Context Engineering (Anthropic, 2025) frames compaction
// as one half of a pair - the other half is deciding what to RE-RETRIEVE.
// "Compaction is necessary but not sufficient": when a summary replaces
// earlier turns, details the model later needs verbatim (a command's exact
// output, an error string, a file path from a tool result) are gone from
// the live window. Without a retrieval path the model can only guess or
// redo work. 2026 agent-engineering surveys call this information loss
// "the silent killer of AI agents".
//
// Design:
//   - Both content-drop points archive the original text BEFORE it leaves
//     the live window: (1) ApplyCompactResult archives the messages the
//     summary replaces; (2) compactSupersededToolResults archives each
//     tool_result output it rewrites to a "[superseded: ...]" stub.
//   - The archive is process-memory only, capped at ~1MB of text; when the
//     cap is exceeded the OLDEST entries are dropped first. System prompts
//     are never archived (huge, no retrieval value).
//   - SearchCompactedArchive is defined on *Manager (NOT added to the
//     ContextManager interface) so existing mock implementers are
//     unaffected; the agent binds it via a local interface assertion, the
//     same pattern as CompactSupersededReads.

import (
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

// maxCompactedArchiveBytes caps the total text held in the archive (~1MB).
// Beyond the cap the oldest entries are evicted.
const maxCompactedArchiveBytes = 1 << 20

// CompactedEntry is one archived content unit rescued from compaction.
type CompactedEntry struct {
	Role   string // "user" | "assistant" (system is never archived)
	ToolID string // non-empty when archived from a mechanical supersession
	Reason string // why it left the live window: "summarized" | supersession reason
	Text   string // original verbatim text
}

// CompactedHit is a search hit over the archive.
type CompactedHit struct {
	Role    string
	ToolID  string
	Reason  string
	Excerpt string // first match position +/- excerptWindow runes
}

const excerptWindow = 200

// archiveCompactedMessages appends the readable text of msgs to the archive,
// skipping system messages. Caller must hold m.mu.
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

// archiveSupersededToolOutput archives a single tool_result output just
// before it is rewritten to a stub. Caller must hold m.mu.
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

// trimCompactedArchiveLocked evicts oldest entries until the total text is
// within the cap. Caller must hold m.mu.
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

// SearchCompactedArchive returns up to limit hits whose text contains the
// case-insensitive substring query, newest first. Each hit carries an
// excerpt around the first match. Defined on *Manager only; the agent binds
// it through a local interface assertion so the ContextManager interface
// and its mock implementers stay untouched.
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

// CompactedArchiveSize returns the number of archived entries (testing /
// observability).
func (m *Manager) CompactedArchiveSize() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.compactedArchive)
}
