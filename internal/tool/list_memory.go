package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/memory"
)

// ListMemoryTool gives the agent visibility over stored auto memories.
// save_memory writes and delete_memory removes, but without an inventory
// view the agent could not curate: outdated entries persisted silently
// because the agent never saw they existed (SelfMem "agent as memory
// curator" gap). Updating is done by re-calling save_memory with the same
// key (supersede semantics).
type ListMemoryTool struct {
	globalMem  *memory.AutoMemory
	projectMem *memory.AutoMemory
}

// NewListMemoryTool creates a list_memory tool with global and project memory.
func NewListMemoryTool(globalMem, projectMem *memory.AutoMemory) *ListMemoryTool {
	return &ListMemoryTool{globalMem: globalMem, projectMem: projectMem}
}

func (t *ListMemoryTool) Name() string { return "list_memory" }
func (t *ListMemoryTool) Description() string {
	return "List stored persistent memories (key + content preview + usage) so you can audit, update, or prune them. " +
		"Update an entry by re-calling save_memory with the same key (supersede semantics; the overwritten version is archived to .history, never destroyed); remove it with delete_memory. " +
		"Pass key to inspect one entry (current content + archived version count); pass key + as_of (RFC3339) to read the exact version that held at that instant (r488 Mem++ read-time temporal selection). " +
		"Scope: 'all' (default), 'project', or 'global'. Does NOT list project memory files (GGCODE.md, AGENTS.md) - only auto-saved memories."
}
func (t *ListMemoryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"scope": {
				"type": "string",
				"description": "Which memory scope to list: 'all' (default), 'project', or 'global'",
				"enum": ["all", "project", "global"]
			},
			"key": {
				"type": "string",
				"description": "Inspect a single memory key: shows current content and archived version count. Combined with as_of, reads the historical version that held at that instant."
			},
			"as_of": {
				"type": "string",
				"description": "RFC3339 instant (e.g. 2026-10-07T09:00:00Z). Requires key. Returns the version of that key that held at this instant, from the non-destructive .history archive."
			}
		}
	}`)
}

func (t *ListMemoryTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var params struct {
		Scope string `json:"scope"`
		Key   string `json:"key"`
		AsOf  string `json:"as_of"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if params.Scope == "" {
		params.Scope = "all"
	}
	if params.AsOf != "" && params.Key == "" {
		return Result{IsError: true, Content: "as_of requires key (which entry's history to read)"}, nil
	}

	type scopeMem struct {
		label string
		mem   *memory.AutoMemory
	}
	var scopes []scopeMem
	switch params.Scope {
	case "global":
		scopes = []scopeMem{{"global", t.globalMem}}
	case "project":
		scopes = []scopeMem{{"project", t.projectMem}}
	case "all":
		scopes = []scopeMem{{"project", t.projectMem}, {"global", t.globalMem}}
	default:
		return Result{IsError: true, Content: fmt.Sprintf("invalid scope %q: must be 'all', 'project', or 'global'", params.Scope)}, nil
	}

	// r488 (Mem++ arXiv:2610.02002) single-key temporal read: which version
	// of this key held at as_of. Without as_of, a plain key view (current
	// content + archived version count).
	if params.Key != "" {
		var asOf time.Time
		if params.AsOf != "" {
			tm, err := time.Parse(time.RFC3339, params.AsOf)
			if err != nil {
				return Result{IsError: true, Content: fmt.Sprintf("invalid as_of %q: %v", params.AsOf, err)}, nil
			}
			asOf = tm
		}
		for _, s := range scopes {
			if s.mem == nil {
				continue
			}
			cur, err := s.mem.LoadKey(params.Key)
			if err != nil {
				return Result{IsError: true, Content: fmt.Sprintf("failed to read %s memory: %v", s.label, err)}, nil
			}
			if cur == "" {
				continue
			}
			if params.AsOf != "" {
				content, held, ok, rerr := s.mem.ReadMemoryAsOf(params.Key, asOf)
				if rerr != nil {
					return Result{IsError: true, Content: fmt.Sprintf("as_of read failed: %v", rerr)}, nil
				}
				if !ok {
					return Result{Content: fmt.Sprintf("[%s] %q did not exist at %s", s.label, params.Key, params.AsOf)}, nil
				}
				return Result{Content: fmt.Sprintf("[%s] %s @ as_of %s (version held since %s):\n%s",
					s.label, params.Key, params.AsOf, held.Format(time.RFC3339), content)}, nil
			}
			vers := s.mem.HistoryVersions(params.Key)
			return Result{Content: fmt.Sprintf("[%s] %s (%d archived version(s)):\n%s", s.label, params.Key, len(vers), cur)}, nil
		}
		return Result{IsError: true, Content: fmt.Sprintf("key %q not found in scope %q", params.Key, params.Scope)}, nil
	}

	var b strings.Builder
	total := 0
	for _, s := range scopes {
		if s.mem == nil {
			continue
		}
		entries, err := s.mem.ListDetailed()
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("failed to list %s memory: %v", s.label, err)}, nil
		}
		if len(entries) == 0 {
			continue
		}
		fmt.Fprintf(&b, "== %s (%d entries) ==\n", s.label, len(entries))
		for _, e := range entries {
			line := fmt.Sprintf("- %s", e.Key)
			if e.Preview != "" {
				line += ": " + e.Preview
			}
			if e.Uses > 0 && !e.LastUsed.IsZero() {
				line += fmt.Sprintf(" [used %d, last %s]", e.Uses, e.LastUsed.Format(time.DateOnly))
			}
			b.WriteString(line + "\n")
			total++
		}
	}
	if total == 0 {
		return Result{Content: "No stored memories found in the requested scope."}, nil
	}
	return Result{Content: strings.TrimRight(b.String(), "\n")}, nil
}
