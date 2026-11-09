package agent

import "encoding/json"

// needsApplyPatchNormalize reports whether the args map carries the nested
// gateway/replay form {"operation":{"type","path","diff"}} without a usable
// top-level path — the exact condition under which internal/tool
// apply_patch.go Execute lifts the nested operation (#5321).
func needsApplyPatchNormalize(m map[string]any) bool {
	if m == nil {
		return false
	}
	if p, ok := m["path"].(string); ok && p != "" {
		return false
	}
	op, ok := m["operation"].(map[string]any)
	if !ok {
		return false
	}
	p, ok := op["path"].(string)
	return ok && p != ""
}

// normalizeApplyPatchArgs mirrors internal/tool/apply_patch.go Execute's
// nested-argument fallback: gateway/replay paths can forward the
// apply_patch_call payload as a tool call whose arguments are
// {"operation":{"type","path","diff"}} instead of the schema-advertised
// top-level shape. When the top-level form carries no path, the nested
// operation is lifted so every edit-tracking extractor sees the same
// shape the tool itself executes (#5321). Non-apply_patch maps and
// malformed input pass through untouched; top-level values win on key
// collisions.
func normalizeApplyPatchArgs(toolName string, m map[string]any) map[string]any {
	if toolName != "apply_patch" || !needsApplyPatchNormalize(m) {
		return m
	}
	op := m["operation"].(map[string]any)
	out := make(map[string]any, len(m)+3)
	for k, v := range m {
		out[k] = v
	}
	for k, v := range op {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return out
}

// normalizeApplyPatchRawArgs is the json.RawMessage entry point for
// extractors that have not unmarshalled yet.
func normalizeApplyPatchRawArgs(toolName string, args json.RawMessage) json.RawMessage {
	if toolName != "apply_patch" || len(args) == 0 {
		return args
	}
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return args
	}
	if !needsApplyPatchNormalize(m) {
		return args
	}
	out, err := json.Marshal(normalizeApplyPatchArgs(toolName, m))
	if err != nil {
		return args
	}
	return out
}
