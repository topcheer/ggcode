package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	maxMemoryKeyLen = 100

	maxMemoryContentBytes = 10 * 1024
)

type SaveMemoryTool struct {
	globalMem  *memory.AutoMemory
	projectMem *memory.AutoMemory
	afterSave  func()
}

func NewSaveMemoryTool(globalMem, projectMem *memory.AutoMemory) *SaveMemoryTool {
	return &SaveMemoryTool{globalMem: globalMem, projectMem: projectMem}
}

func (t *SaveMemoryTool) SetAfterSave(fn func()) {
	t.afterSave = fn
}

func (t *SaveMemoryTool) Name() string { return "save_memory" }
func (t *SaveMemoryTool) Description() string {
	return "Save a pattern or experience to persistent memory for future sessions. Default scope='project' for project-specific knowledge; use scope='global' sparingly — it loads into EVERY project's system prompt. " +
		"Re-saving an existing key archives the previous version to .history instead of destroying it; read a past version with list_memory key=<key> as_of=<RFC3339>."
}

func (t *SaveMemoryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"key": {
					"type": "string",
					"description": "Short identifier for this memory (e.g. 'build-process', 'api-gotcha'). For procedural knowledge (workflows, conventions, how-to steps), use an '-impl' suffix or 'build-'/'release-' prefix in the key - such keys are classified persistent and inlined into future sessions' prompts; other keys may be index-only."
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

	if strings.TrimSpace(params.Key) == "" {
		return Result{IsError: true, Content: "key is required and cannot be blank"}, nil
	}
	if len(params.Key) > maxMemoryKeyLen {
		return Result{IsError: true, Content: fmt.Sprintf("key too long: %d chars (max %d). Use a shorter identifier.", len(params.Key), maxMemoryKeyLen)}, nil
	}

	if len(params.Content) > maxMemoryContentBytes {
		return Result{IsError: true, Content: fmt.Sprintf("content too large: %d bytes (max %d bytes). Summarize or split into smaller entries.", len(params.Content), maxMemoryContentBytes)}, nil
	}

	var dupWarning string
	if dc := target.CheckDuplicate(params.Key, params.Content); dc.IsDuplicate() {
		dupWarning = dc.FormatDuplicateWarning(params.Key)
	}

	var contraWarning string
	if cc := target.CheckContradiction(params.Key, params.Content); cc.HasConflict() {
		contraWarning = cc.FormatContradictionWarning(params.Key)
	}

	var supersedeNote string
	if olds := target.DetectSupersession(params.Key, params.Content); len(olds) > 0 {
		if err := target.ApplySupersession(params.Key, olds); err != nil {

			supersedeNote = fmt.Sprintf("supersede bookkeeping failed: %v", err)
		} else {
			supersedeNote = memory.FormatSupersedeNote(olds)
		}
	}

	var taintNote string
	if pat := memory.DetectInjectionTaint(params.Key, params.Content); pat != "" {
		taintNote = fmt.Sprintf("SECURITY: this memory content matched a prompt-injection pattern (%q). It was saved but marked tainted - it will NOT be auto-inlined into future system prompts (index-only; readable via read_file, which wraps untrusted content). If this is a legitimate security writeup, that is expected; if you did not intend to store injection text, review the source that produced it.", pat)
	}

	actor := util.ActorFromContext(ctx)
	if err := target.SaveMemoryWithSourceActor(params.Key, params.Content, "save_memory:"+scopeLabel, actor); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to save %s memory: %v", scopeLabel, err)}, nil
	}
	if t.afterSave != nil {
		t.afterSave()
	}

	msg := fmt.Sprintf("%s memory saved: %s", scopeLabel, params.Key)
	if actor != "" {

		msg += fmt.Sprintf("\n(provenance: recorded as %s)", actor)
	}
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
