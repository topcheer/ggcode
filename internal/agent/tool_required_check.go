package agent

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

// Schema-driven pre-flight required-parameter validation.
//
// Root cause (verified): runtime enforcement lives in per-tool hand-written
// checks (13 CheckRequired call sites, none checking `description`), and the
// error messages are bare ("missing required parameter: file_path") with no
// schema description. The provider does not validate tool-call arguments
// (ToDefinitions sends raw schemas), so a missing functional parameter is
// only reported AFTER dispatch, and the LLM has to guess what the parameter
// means from the name alone — costing extra retry rounds.
//
// This validator reads the tool's OWN schema (the single source of truth the
// LLM was shown), checks top-level required fields before Execute runs, and
// embeds each missing parameter's schema description in the error so the
// model can fix the call in ONE retry.
//
// Boundaries (intentional):
//   - Top-level required only. Nested schemas (multi_edit_file's
//     edits[].old_text/new_text) stay with the tool's own checks.
//   - Malformed schema JSON skips validation (never blocks execution).
//   - Presence check only (null/non-empty); type coercion stays downstream.

type requiredSchemaCache struct {
	mu     sync.RWMutex
	byTool map[string]*requiredSchemaInfo
}

type requiredSchemaInfo struct {
	required []requiredField
}

type requiredField struct {
	Name string
	Desc string
}

var globalRequiredSchemaCache = &requiredSchemaCache{byTool: map[string]*requiredSchemaInfo{}}

// parseRequiredSchema extracts top-level required fields and their
// descriptions from a JSON Schema. Returns nil when the schema is absent,
// unparseable, or declares no required fields.
func parseRequiredSchema(params json.RawMessage) *requiredSchemaInfo {
	if len(params) == 0 {
		return nil
	}
	var sch struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(params, &sch); err != nil {
		return nil
	}
	if len(sch.Required) == 0 {
		return nil
	}
	info := &requiredSchemaInfo{}
	for _, name := range sch.Required {
		f := requiredField{Name: name}
		if p, ok := sch.Properties[name]; ok {
			f.Desc = p.Description
		}
		info.required = append(info.required, f)
	}
	return info
}

func (c *requiredSchemaCache) get(t tool.Tool) *requiredSchemaInfo {
	name := t.Name()
	c.mu.RLock()
	info, ok := c.byTool[name]
	c.mu.RUnlock()
	if ok {
		return info
	}
	info = parseRequiredSchema(t.Parameters())
	c.mu.Lock()
	c.byTool[name] = info
	c.mu.Unlock()
	return info
}

// missingRequiredFields returns the schema-required top-level parameters
// that are absent or empty in the call arguments.
func missingRequiredFields(info *requiredSchemaInfo, args json.RawMessage) []requiredField {
	if info == nil || len(info.required) == 0 {
		return nil
	}
	var call map[string]json.RawMessage
	if len(args) > 0 {
		if err := json.Unmarshal(args, &call); err != nil {
			// Arguments not an object (or invalid JSON): let the tool's own
			// parse produce its usual error instead of a confusing list.
			return nil
		}
	}
	var missing []requiredField
	for _, f := range info.required {
		raw, present := call[f.Name]
		if present && !isEmptyJSONValue(raw) {
			continue
		}
		missing = append(missing, f)
	}
	return missing
}

// isEmptyJSONValue reports whether a raw JSON value counts as "not provided":
// null, empty string, empty array, or empty object.
func isEmptyJSONValue(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case trimmed == "" || trimmed == "null":
		return true
	case trimmed == `""`:
		return true
	case trimmed == "[]" || trimmed == "{}":
		return true
	default:
		return false
	}
}

// formatMissingRequired renders the structured pre-flight error, embedding
// each missing parameter's schema description so the LLM can self-correct
// in one retry.
func formatMissingRequired(toolName string, missing []requiredField) string {
	var sb strings.Builder
	if len(missing) == 1 {
		sb.WriteString("missing required parameter: ")
	} else {
		sb.WriteString("missing required parameters: ")
	}
	for i, f := range missing {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(f.Name)
	}
	sb.WriteString("\n")
	for _, f := range missing {
		sb.WriteString("\n" + f.Name + ": ")
		if f.Desc != "" {
			sb.WriteString(f.Desc)
		} else {
			sb.WriteString("(required, see tool schema)")
		}
	}
	sb.WriteString("\n\nresend the call with these parameters included.")
	return sb.String()
}

// preflightRequiredCheck runs before Execute in safeExecute. Returns a
// non-nil Result when the call must be rejected pre-dispatch.
func preflightRequiredCheck(t tool.Tool, args json.RawMessage) *tool.Result {
	info := globalRequiredSchemaCache.get(t)
	missing := missingRequiredFields(info, args)
	if len(missing) == 0 {
		return nil
	}
	debug.Log("agent", "preflight required-check rejected %s: missing %d param(s)", t.Name(), len(missing))
	r := tool.Result{IsError: true, Content: formatMissingRequired(t.Name(), missing)}
	return &r
}
