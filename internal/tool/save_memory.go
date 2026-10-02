package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/memory"
)

const (
	// maxMemoryKeyLen limits key length. Keys become filenames, so excessively
	// long keys cause filesystem issues. 100 chars is generous for a key.
	maxMemoryKeyLen = 100

	// maxMemoryContentBytes limits content size. Memory entries are loaded
	// into the system prompt at startup, so large entries waste context on
	// every session. 10KB is enough for concise rules and patterns.
	maxMemoryContentBytes = 10 * 1024
)

// SaveMemoryTool lets the agent save experiences to persistent memory.
type SaveMemoryTool struct {
	globalMem  *memory.AutoMemory
	projectMem *memory.AutoMemory
	afterSave  func()
}

// NewSaveMemoryTool creates a save_memory tool with global and project memory.
func NewSaveMemoryTool(globalMem, projectMem *memory.AutoMemory) *SaveMemoryTool {
	return &SaveMemoryTool{globalMem: globalMem, projectMem: projectMem}
}

// SetAfterSave configures a callback that runs after memory is persisted.
// Callers can use this to refresh any in-memory prompt state that includes
// auto memory from disk.
func (t *SaveMemoryTool) SetAfterSave(fn func()) {
	t.afterSave = fn
}

func (t *SaveMemoryTool) Name() string { return "save_memory" }
func (t *SaveMemoryTool) Description() string {
	return "Save a pattern or experience to persistent memory for future sessions. Default scope='project' for project-specific knowledge; use scope='global' sparingly — it loads into EVERY project's system prompt."
}

func (t *SaveMemoryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"key": {
					"type": "string",
					"description": "Short identifier for this memory (e.g. 'build-process', 'api-gotcha')"
				},
				"content": {
					"type": "string",
					"description": "The memory content to save"
				},
				"scope": {
					"type": "string",
					"description": "Where to store: 'project' for project-specific knowledge, 'global' for cross-project patterns. Default: 'project'.",
					"enum": ["project", "global"]
				}
			},
			"required": ["key", "content"]
		}`)
}

func (t *SaveMemoryTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var params struct {
		Key     string `json:"key"`
		Content string `json:"content"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	// Default scope is project
	if params.Scope == "" {
		params.Scope = "project"
	}

	var target *memory.AutoMemory
	var scopeLabel string
	switch params.Scope {
	case "global":
		target = t.globalMem
		scopeLabel = "global"
	case "project":
		target = t.projectMem
		scopeLabel = "project"
	default:
		return Result{IsError: true, Content: fmt.Sprintf("invalid scope %q: must be 'global' or 'project'", params.Scope)}, nil
	}

	if target == nil {
		return Result{IsError: true, Content: fmt.Sprintf("%s memory not available", scopeLabel)}, nil
	}

	// Validate key
	// #1700 case 2: an empty key slipped through (only the max length was
	// checked), sanitizeKey turned it into "untitled" and sha256("")'s
	// first bytes mapped EVERY empty key to the same file - silent mutual
	// overwrites reported as "memory saved: ".
	if strings.TrimSpace(params.Key) == "" {
		return Result{IsError: true, Content: "key is required and cannot be blank"}, nil
	}
	if len(params.Key) > maxMemoryKeyLen {
		return Result{IsError: true, Content: fmt.Sprintf("key too long: %d chars (max %d). Use a shorter identifier.", len(params.Key), maxMemoryKeyLen)}, nil
	}

	// Validate content size — memory is loaded into the system prompt at
	// startup, so excessively large entries waste context on every session.
	if len(params.Content) > maxMemoryContentBytes {
		return Result{IsError: true, Content: fmt.Sprintf("content too large: %d bytes (max %d bytes). Summarize or split into smaller entries.", len(params.Content), maxMemoryContentBytes)}, nil
	}

	// Check for duplicate/near-duplicate before saving.
	var dupWarning string
	if dc := target.CheckDuplicate(params.Key, params.Content); dc.IsDuplicate() {
		dupWarning = dc.FormatDuplicateWarning(params.Key)
	}

	// Check for semantic contradictions with existing memories.
	var contraWarning string
	if cc := target.CheckContradiction(params.Key, params.Content); cc.HasConflict() {
		contraWarning = cc.FormatContradictionWarning(params.Key)
	}

	// Memory evolution (supersede.go): when the new content carries explicit
	// replacement semantics AND conflicts on a shared subject, retire the old
	// entries from prompt injection (kept on disk for history) instead of
	// leaving the conflict live forever.
	var supersedeNote string
	if olds := target.DetectSupersession(params.Key, params.Content); len(olds) > 0 {
		if err := target.ApplySupersession(params.Key, olds); err != nil {
			// Non-fatal: the save itself must proceed; the sidecar failure is
			// surfaced in the note instead.
			supersedeNote = fmt.Sprintf("supersede bookkeeping failed: %v", err)
		} else {
			supersedeNote = memory.FormatSupersedeNote(olds)
		}
	}

	// r409 memory-poisoning defense: surface the quarantine decision in the
	// tool result. The AutoMemory layer does the authoritative taint marking
	// (SaveMemoryWithSource); this note tells the model (and the user) why
	// the entry will not appear in future system prompts. Deliberately not
	// an error: a legitimate security writeup must still be persistable.
	var taintNote string
	if pat := memory.DetectInjectionTaint(params.Key, params.Content); pat != "" {
		taintNote = fmt.Sprintf("SECURITY: this memory content matched a prompt-injection pattern (%q). It was saved but marked tainted - it will NOT be auto-inlined into future system prompts (index-only; readable via read_file, which wraps untrusted content). If this is a legitimate security writeup, that is expected; if you did not intend to store injection text, review the source that produced it.", pat)
	}

	if err := target.SaveMemoryWithSource(params.Key, params.Content, "save_memory:"+scopeLabel); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to save %s memory: %v", scopeLabel, err)}, nil
	}
	if t.afterSave != nil {
		t.afterSave()
	}

	msg := fmt.Sprintf("%s memory saved: %s", scopeLabel, params.Key)
	if dupWarning != "" {
		msg += "\n\n" + dupWarning
	}
	if contraWarning != "" {
		msg += "\n\n" + contraWarning
	}
	if supersedeNote != "" {
		msg += "\n\n" + supersedeNote
	}
	if taintNote != "" {
		msg += "\n\n" + taintNote
	}
	return Result{Content: msg}, nil
}
