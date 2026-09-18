package tool

// OpenAI Responses apply_patch tool (client-executed harness) - sa-67.
//
// The Responses API's apply_patch tool (tools=[{"type":"apply_patch"}]) makes
// the model emit structured V4A diffs as apply_patch_call output items. The
// call is NOT executed server-side: the client must apply the patch to its
// working tree and report back an apply_patch_call_output item. ggcode wires
// this through the standard agent loop: the Responses provider converts each
// apply_patch_call into a call to this hidden tool, and maps this tool's
// Result back into apply_patch_call_output (status completed/failed).
//
// The tool is registered but hidden from ToDefinitions (Available()==false):
// the model reaches it through the typed apply_patch tool declaration, never
// as an advertised function tool.
//
// V4A patch grammar (subset implemented here, matching the common model
// output; reference: openai-agents-python src/agents/apply_diff.py):
//
//	*** Begin Patch
//	*** Update File: src/app.py
//	@@ def main():
//	-    print("Hello")
//	+    print("Hello, world!")
//	*** Add File: docs/new.md
//	+# Title
//	*** Delete File: old.txt
//	*** Move to: renamed.txt   (optional, follows Update File)
//	*** End Patch
//
// Update hunks are located by their context/delete lines, searched forward
// from the previous hunk's position (monotonic, like real V4A). Context must
// match exactly; a trailing-whitespace-insensitive retry runs before failing.
//
// References:
//   - https://developers.openai.com/api/docs/guides/tools-apply-patch
//   - https://github.com/openai/openai-agents-python/blob/main/src/agents/apply_diff.py

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ApplyPatch applies OpenAI V4A patches emitted via the Responses apply_patch
// tool to the local working tree.
type ApplyPatch struct {
	SandboxCheck AllowedPathChecker
	WorkingDir   string
}

func (t ApplyPatch) Name() string { return "apply_patch" }

// Available hides the tool from ToDefinitions: it is invoked only through the
// provider's apply_patch_call mapping, never advertised as a function tool.
func (t ApplyPatch) Available() bool { return false }

func (t ApplyPatch) Description() string {
	return "Internal executor for the OpenAI Responses apply_patch tool: applies a V4A diff " +
		"(create/update/delete file operations) to the working tree. Not called directly by the model."
}

func (t ApplyPatch) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"type": {
			"type": "string",
			"enum": ["create_file", "update_file", "delete_file"],
			"description": "Patch operation type from the apply_patch_call item."
		},
		"path": {
			"type": "string",
			"description": "File path the operation targets, relative to the working directory."
		},
		"diff": {
			"type": "string",
			"description": "V4A patch body (Begin/End Patch envelope optional). Required for create_file and update_file."
		}
	},
	"required": ["path"]
}`)
}

func (t ApplyPatch) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Type string `json:"type"`
		Path string `json:"path"`
		Diff string `json:"diff"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("Error: invalid apply_patch input: %v", err)}, nil
	}
	// Also accept the nested {"operation":{...}} shape for robustness.
	if args.Path == "" {
		var nested struct {
			Operation struct {
				Type string `json:"type"`
				Path string `json:"path"`
				Diff string `json:"diff"`
			} `json:"operation"`
		}
		if json.Unmarshal(input, &nested) == nil && nested.Operation.Path != "" {
			args.Type, args.Path, args.Diff = nested.Operation.Type, nested.Operation.Path, nested.Operation.Diff
		}
	}

	if args.Path == "" {
		return Result{IsError: true, Content: "Error: apply_patch operation is missing 'path'"}, nil
	}
	opType := args.Type
	if opType == "" {
		if args.Diff != "" {
			opType = "update_file"
		} else {
			return Result{IsError: true, Content: "Error: apply_patch operation needs 'type' (create_file, update_file, or delete_file)"}, nil
		}
	}

	resolved, err := resolveToolPath(args.Path, t.WorkingDir)
	if err != nil {
		return Result{IsError: true, Content: "Error: " + err.Error()}, nil
	}
	if t.SandboxCheck != nil && !t.SandboxCheck(resolved) {
		return Result{IsError: true, Content: fmt.Sprintf("Error: path %q is outside the allowed sandbox", args.Path)}, nil
	}

	sections, err := parseV4APatch(args.Diff)
	if err != nil {
		return Result{IsError: true, Content: "Error: invalid patch: " + err.Error()}, nil
	}
	sec, err := selectV4ASection(sections, opType, args.Path)
	if err != nil {
		return Result{IsError: true, Content: "Error: " + err.Error()}, nil
	}

	summary, err := applyV4ASection(resolved, sec)
	if err != nil {
		return Result{IsError: true, Content: "Error: " + err.Error()}, nil
	}
	return Result{Content: summary}, nil
}

// ---- V4A parsing ----

type v4aSection struct {
	kind   string   // "add", "update", "delete"
	path   string   // target path as written in the patch
	moveTo string   // optional relocation target for update sections
	lines  []string // body lines (context/-/+ for updates, +content for adds)
}

// parseV4APatch parses a V4A patch body. The Begin Patch / End Patch envelope
// is optional; unknown directives are rejected (fail closed) so a
// misunderstood patch never half-applies silently.
func parseV4APatch(diff string) ([]v4aSection, error) {
	if strings.TrimSpace(diff) == "" {
		return nil, fmt.Errorf("patch body is empty")
	}
	raw := strings.Split(diff, "\n")
	// Trim the optional envelope and surrounding blank lines.
	for len(raw) > 0 && strings.TrimSpace(raw[0]) == "" {
		raw = raw[1:]
	}
	if len(raw) > 0 && strings.TrimSpace(raw[0]) == "*** Begin Patch" {
		raw = raw[1:]
	}
	for len(raw) > 0 && strings.TrimSpace(raw[len(raw)-1]) == "" {
		raw = raw[:len(raw)-1]
	}
	if len(raw) > 0 && strings.TrimSpace(raw[len(raw)-1]) == "*** End Patch" {
		raw = raw[:len(raw)-1]
	}

	sections := make([]v4aSection, 0, 2)
	var cur *v4aSection
	ended := false
	for _, line := range raw {
		if ended {
			break
		}
		trimmed := strings.TrimRight(line, "\r")
		if strings.HasPrefix(trimmed, "*** ") {
			body := strings.TrimSpace(strings.TrimPrefix(trimmed, "*** "))
			switch {
			case strings.HasPrefix(body, "Add File: "):
				sections = append(sections, v4aSection{kind: "add", path: strings.TrimSpace(strings.TrimPrefix(body, "Add File: "))})
				cur = &sections[len(sections)-1]
			case strings.HasPrefix(body, "Update File: "):
				sections = append(sections, v4aSection{kind: "update", path: strings.TrimSpace(strings.TrimPrefix(body, "Update File: "))})
				cur = &sections[len(sections)-1]
			case strings.HasPrefix(body, "Delete File: "):
				sections = append(sections, v4aSection{kind: "delete", path: strings.TrimSpace(strings.TrimPrefix(body, "Delete File: "))})
				cur = &sections[len(sections)-1]
			case strings.HasPrefix(body, "Move to: "):
				if cur == nil || cur.kind != "update" {
					return nil, fmt.Errorf("'Move to:' only valid after an Update File header")
				}
				cur.moveTo = strings.TrimSpace(strings.TrimPrefix(body, "Move to: "))
			case body == "End Patch":
				ended = true
			default:
				return nil, fmt.Errorf("unsupported patch directive %q", trimmed)
			}
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("patch content %q appears before any file header", trimmed)
		}
		cur.lines = append(cur.lines, trimmed)
	}
	if len(sections) == 0 {
		return nil, fmt.Errorf("patch contains no file sections")
	}
	return sections, nil
}

// selectV4ASection picks the section matching the apply_patch_call operation.
// The Responses protocol sends one operation per call, but a model-generated
// diff may bundle several file sections; only the addressed one applies.
func selectV4ASection(sections []v4aSection, opType, opPath string) (v4aSection, error) {
	for _, s := range sections {
		if s.path == opPath {
			return s, nil
		}
	}
	if len(sections) == 1 && (opPath == "" || opPath == ".") {
		return sections[0], nil
	}
	return v4aSection{}, fmt.Errorf("patch has no section for %q (op %s)", opPath, opType)
}

// ---- V4A application ----

type v4aLine struct {
	kind byte // ' ' context, '-' delete, '+' add
	text string
}

type v4aHunk struct {
	lines []v4aLine
}

// applyV4ASection applies one parsed section to disk. root is the resolved
// target path from the operation (already sandbox-checked by Execute).
func applyV4ASection(root string, sec v4aSection) (string, error) {
	switch sec.kind {
	case "add":
		if _, err := os.Stat(root); err == nil {
			return "", fmt.Errorf("create_file: %s already exists", sec.path)
		}
		var b strings.Builder
		for _, l := range sec.lines {
			b.WriteString(strings.TrimPrefix(l, "+"))
			b.WriteString("\n")
		}
		if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
			return "", fmt.Errorf("create_file: %v", err)
		}
		if err := os.WriteFile(root, []byte(b.String()), 0o644); err != nil {
			return "", fmt.Errorf("create_file: %v", err)
		}
		return fmt.Sprintf("Created %s (%d lines)", sec.path, len(sec.lines)), nil

	case "delete":
		if _, err := os.Stat(root); err != nil {
			return "", fmt.Errorf("delete_file: %s not found", sec.path)
		}
		if err := os.Remove(root); err != nil {
			return "", fmt.Errorf("delete_file: %v", err)
		}
		return fmt.Sprintf("Deleted %s", sec.path), nil

	case "update":
		data, err := os.ReadFile(root)
		if err != nil {
			return "", fmt.Errorf("update_file: %s not found", sec.path)
		}
		hunks, err := parseV4AHunks(sec.lines)
		if err != nil {
			return "", fmt.Errorf("update_file: %v", err)
		}
		updated, err := applyV4AHunks(string(data), hunks)
		if err != nil {
			return "", fmt.Errorf("update_file %s: %v", sec.path, err)
		}
		mode := os.FileMode(0o644)
		if info, statErr := os.Stat(root); statErr == nil {
			mode = info.Mode()
		}
		target := root
		if sec.moveTo != "" && filepath.Clean(sec.moveTo) != filepath.Clean(sec.path) {
			target = filepath.Clean(filepath.Join(filepath.Dir(root), sec.moveTo))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", fmt.Errorf("move to %q: %v", sec.moveTo, err)
			}
		}
		if err := os.WriteFile(target, []byte(updated), mode); err != nil {
			return "", fmt.Errorf("update_file: %v", err)
		}
		if target != root {
			if err := os.Remove(root); err != nil {
				return "", fmt.Errorf("move: removing old %s: %v", sec.path, err)
			}
			return fmt.Sprintf("Moved %s -> %s (%d hunks applied)", sec.path, sec.moveTo, len(hunks)), nil
		}
		return fmt.Sprintf("Updated %s (%d hunks applied)", sec.path, len(hunks)), nil
	default:
		return "", fmt.Errorf("unknown section kind %q", sec.kind)
	}
}

// parseV4AHunks splits an update section body into hunks. Lines starting with
// "@@" are context anchors; they carry no match semantics here (the hunk's
// own context lines locate it) but delimit independent hunks.
func parseV4AHunks(lines []string) ([]v4aHunk, error) {
	hunks := make([]v4aHunk, 0, 2)
	var cur *v4aHunk
	flush := func() {
		if cur != nil && len(cur.lines) > 0 {
			hunks = append(hunks, *cur)
		}
		cur = nil
	}
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if strings.HasPrefix(line, "@@") {
			flush()
			cur = &v4aHunk{}
			continue
		}
		if cur == nil {
			// Tolerate hunks without an @@ separator: start one implicitly.
			cur = &v4aHunk{}
		}
		if line == "" {
			cur.lines = append(cur.lines, v4aLine{kind: ' ', text: ""})
			continue
		}
		switch line[0] {
		case ' ', '-', '+':
			cur.lines = append(cur.lines, v4aLine{kind: line[0], text: line[1:]})
		default:
			return nil, fmt.Errorf("invalid hunk line %q (want ' ', '-', or '+' prefix)", line)
		}
	}
	flush()
	if len(hunks) == 0 {
		return nil, fmt.Errorf("no hunks found in update section")
	}
	return hunks, nil
}

// applyV4AHunks applies hunks sequentially to src. Each hunk is located by
// its context/delete lines, searched forward from the previous hunk's end
// (monotonic scan, mirroring V4A semantics). Context must match exactly; a
// trailing-whitespace-insensitive retry runs before the hunk is declared
// unapplicable.
func applyV4AHunks(src string, hunks []v4aHunk) (string, error) {
	lines := strings.Split(src, "\n")
	pos := 0
	for hi, h := range hunks {
		var pattern []string
		for _, l := range h.lines {
			if l.kind == ' ' || l.kind == '-' {
				pattern = append(pattern, l.text)
			}
		}
		if len(pattern) == 0 {
			return "", fmt.Errorf("hunk %d has no context or deletions; cannot locate insertion point", hi+1)
		}
		idx := matchV4APattern(lines[pos:], pattern)
		if idx < 0 {
			return "", fmt.Errorf("hunk %d: context not found (%d line(s) starting %q); re-read the file and retry", hi+1, len(pattern), pattern[0])
		}
		// Replacement = hunk lines minus deletions (context stays in place,
		// additions splice in wherever '+' lines appear among them).
		replacement := make([]string, 0, len(h.lines))
		for _, l := range h.lines {
			if l.kind != '-' {
				replacement = append(replacement, l.text)
			}
		}
		newLines := make([]string, 0, len(lines)+len(replacement))
		newLines = append(newLines, lines[:pos+idx]...)
		newLines = append(newLines, replacement...)
		newLines = append(newLines, lines[pos+idx+len(pattern):]...)
		lines = newLines
		pos = pos + idx + len(replacement)
	}
	return strings.Join(lines, "\n"), nil
}

// matchV4APattern finds pattern in lines, first exact, then
// trailing-whitespace-insensitive. Returns the index relative to lines, or -1.
func matchV4APattern(lines []string, pattern []string) int {
	if len(pattern) > len(lines) {
		return -1
	}
	match := func(normalize func(string) string) int {
		for i := 0; i+len(pattern) <= len(lines); i++ {
			ok := true
			for j := range pattern {
				if normalize(lines[i+j]) != normalize(pattern[j]) {
					ok = false
					break
				}
			}
			if ok {
				return i
			}
		}
		return -1
	}
	if idx := match(func(s string) string { return s }); idx >= 0 {
		return idx
	}
	return match(func(s string) string { return strings.TrimRight(s, " \t") })
}
