package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/sessionidx"
)

// SessionRecall lets the agent recall content from its own and sibling
// sessions (same workspace) via a trigram inverted index, without re-reading
// multi-hundred-megabyte JSONL histories.
type SessionRecall struct {
	Manager  *sessionidx.Manager
	Registry *Registry // fallback: lazy lookup when Manager not set at build time
}

func (t SessionRecall) Name() string { return "session_recall" }

func (t SessionRecall) Description() string {
	return "Search past session transcripts (JSONL histories) of this workspace by keyword. Use when you need to recover details that were compacted out of context: earlier user instructions, decisions, tool results (file contents, command output) from previous sessions. Own-session and sibling-session histories are covered. Terms are ANDed; results are newest-first with role and timestamp."
}

func (t SessionRecall) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Space-separated keywords. All terms must appear in the same message (AND)."
			},
			"session_id": {
				"type": "string",
				"description": "Restrict the search to one session id. Omit to search all sessions in the workspace."
			},
			"role": {
				"type": "string",
				"enum": ["user", "assistant", "tool"],
				"description": "Filter by message role (optional)."
			},
			"limit": {
				"type": "integer",
				"minimum": 1,
				"maximum": 50,
				"description": "Max results (default 10)."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["query", "description"]
	}`)
}

type sessionRecallArgs struct {
	Query     string `json:"query"`
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	Limit     int    `json:"limit"`
}

func roleByteToString(b byte) string {
	switch b {
	case 1:
		return "user"
	case 2:
		return "assistant"
	case 3:
		return "tool"
	default:
		return "other"
	}
}

func (t SessionRecall) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args sessionRecallArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(args.Query) == "" {
		return Result{IsError: true, Content: "query is required"}, nil
	}
	if t.Manager == nil && t.Registry != nil {
		t.Manager = t.Registry.SessionIndex()
	}
	if t.Manager == nil {
		return Result{IsError: true, Content: "session index manager not available in this process"}, nil
	}
	if args.Limit <= 0 {
		args.Limit = 10
	}
	if args.Limit > 50 {
		args.Limit = 50
	}

	// refresh own index opportunistically (cheap tail) so very recent
	// messages are searchable
	if self := t.Manager.Self(); self != nil {
		_ = self.Refresh()
	}

	terms := strings.Fields(strings.ToLower(args.Query))
	results := t.Manager.Query(terms, args.SessionID, args.Limit*2) // headroom for role filter

	var sb strings.Builder
	shown := 0
	for _, r := range results {
		if args.Role != "" && roleByteToString(r.Role) != args.Role {
			continue
		}
		snippet, role, ts := extractRecallView(r.Line)
		if args.Role != "" && role != args.Role {
			continue
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s (session %s): %s\n", role, ts, shortID(r.SessionID), snippet))
		shown++
		if shown >= args.Limit {
			break
		}
	}
	if shown == 0 {
		return Result{Content: fmt.Sprintf("no matches for %q across indexed sessions (own index fresh; sibling indexes refresh every 30m)", args.Query)}, nil
	}
	return Result{Content: fmt.Sprintf("%d match(es) for %q:\n%s", shown, args.Query, sb.String())}, nil
}

func shortID(id string) string {
	if len(id) > 10 {
		return id[:10] + "…"
	}
	return id
}

// extractRecallView pulls a readable snippet (first text block) from a raw
// message JSONL line without full typed decoding.
func extractRecallView(line string) (snippet, role, ts string) {
	var rec struct {
		Timestamp string `json:"timestamp"`
		Message   *struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return truncateRecall(line, 200), "?", ""
	}
	if rec.Message != nil {
		role = rec.Message.Role
		for _, b := range rec.Message.Content {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				snippet = truncateRecall(strings.Join(strings.Fields(b.Text), " "), 300)
				break
			}
		}
	}
	if snippet == "" {
		snippet = truncateRecall(line, 200)
	}
	if rec.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, rec.Timestamp); err == nil {
			ts = t.Format("01-02 15:04")
		} else {
			ts = rec.Timestamp
		}
	}
	return snippet, role, ts
}

func truncateRecall(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
