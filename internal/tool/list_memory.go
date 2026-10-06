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
		"Update an entry by re-calling save_memory with the same key; remove it with delete_memory. " +
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
			}
		}
	}`)
}

func (t *ListMemoryTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var params struct {
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if params.Scope == "" {
		params.Scope = "all"
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
