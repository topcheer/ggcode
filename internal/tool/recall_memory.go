package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/session"
)

// maxRecallResults caps how many hits the tool returns to the model. Each
// hit is a ~2-line snippet; 50 is the ceiling to keep the tool_result within
// a sane context budget.
const maxRecallResults = 50

// RecallMemoryTool gives the agent first-class access to its own episodic
// memory: the transcripts of past sessions. Frontier memory research
// (AgeMem, arXiv:2601.01885; Mem0 State of AI Agent Memory 2026) argues
// memory operations — retrieval included — should be tool-based agent
// actions, not just UI features: the model should decide WHEN to recall.
// ggcode previously exposed cross-session search only through the TUI
// inspector panel; save_memory/knowledge_graph covered semantic and
// structural memory, but nothing let the agent look up what actually
// happened in earlier conversations. recall_memory closes that gap with
// multi-signal ranking (coverage × term frequency + phrase + title +
// recency + user-role boost) instead of raw substring grep.
type RecallMemoryTool struct {
	// Dir optionally overrides the session storage directory (tests).
	// Empty means session.DefaultDir().
	Dir string
	// Now optionally overrides the wall clock for recency scoring (tests).
	Now time.Time
}

func (t *RecallMemoryTool) Name() string { return "recall_memory" }

func (t *RecallMemoryTool) Description() string {
	return `Search the transcripts of past ggcode sessions (episodic memory) and return the most relevant message snippets, ranked by a multi-signal relevance score (keyword coverage, term frequency, exact phrase, session title match, recency, user-role boost). Use this BEFORE re-deriving context the user says was discussed before, e.g. "上次我们怎么解决的", "what did we decide about X last week". Supports filtering by role, time window, and workspace scope. This searches history, not the current conversation.`
}

func (t *RecallMemoryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Keywords or phrase to search for. Multi-keyword queries are ranked by coverage + phrase match; use the distinctive words, not generic ones."
    },
    "max_results": {
      "type": "integer",
      "description": "Maximum number of hits to return (default 15, max 50)."
    },
    "role": {
      "type": "string",
      "description": "Optionally restrict hits to one speaker.",
      "enum": ["user", "assistant"]
    },
    "since_days": {
      "type": "integer",
      "description": "Only consider messages from the last N days (e.g. 7, 30). 0 = all history."
    },
    "all_workspaces": {
      "type": "boolean",
      "description": "Search ALL projects' sessions instead of only the current workspace. Default false."
    }
  },
  "required": ["query"]
}`)
}

func (t *RecallMemoryTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var params struct {
		Query         string `json:"query"`
		MaxResults    int    `json:"max_results"`
		Role          string `json:"role"`
		SinceDays     int    `json:"since_days"`
		AllWorkspaces bool   `json:"all_workspaces"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(params.Query) == "" {
		return Result{IsError: true, Content: "query is required and cannot be blank"}, nil
	}
	switch params.Role {
	case "", "user", "assistant":
	default:
		return Result{IsError: true, Content: fmt.Sprintf("invalid role %q: must be 'user' or 'assistant'", params.Role)}, nil
	}
	if params.MaxResults <= 0 {
		params.MaxResults = 15
	}
	if params.MaxResults > maxRecallResults {
		params.MaxResults = maxRecallResults
	}
	if params.SinceDays < 0 {
		params.SinceDays = 0
	}

	dir := t.Dir
	if dir == "" {
		var err error
		dir, err = session.DefaultDir()
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("resolving session directory: %v", err)}, nil
		}
	}
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("opening session store: %v", err)}, nil
	}

	opts := session.RankedOptions{
		Role:      params.Role,
		SinceDays: params.SinceDays,
		Now:       t.Now,
	}
	if !params.AllWorkspaces {
		opts.Workspace = session.CurrentWorkspacePath()
	}

	results, err := store.SearchSessionsRanked(params.Query, params.MaxResults, opts)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("searching sessions: %v", err)}, nil
	}
	if len(results) == 0 {
		return Result{Content: fmt.Sprintf(
			"No past-session matches for %q. Try fewer, more distinctive keywords, a longer time window, or all_workspaces=true.",
			params.Query)}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Recalled %d past-session match(es) for %q, ranked by relevance:\n\n", len(results), params.Query)
	for i, r := range results {
		when := ""
		if !r.Timestamp.IsZero() {
			when = r.Timestamp.Format("2006-01-02")
		}
		fmt.Fprintf(&b, "%d. [score %.2f] %s — %s (%s)\n   %q\n   session: %s\n",
			i+1, r.Score, r.Title, when, r.Role, r.Snippet, r.SessionID)
	}
	b.WriteString("\nTip: resume a hit with `ggcode --resume <session_id>` for full context.")
	return Result{Content: b.String()}, nil
}
