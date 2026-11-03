package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/provider"
)

var oversightCriticalFiles = []string{
	"go.mod", "go.sum",
	"package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
	"Cargo.toml", "Cargo.lock",
	"pubspec.yaml", "pubspec.lock",
	"requirements.txt", "Pipfile", "Pipfile.lock", "pyproject.toml",
	"build.gradle", "build.gradle.kts", "settings.gradle",
	"Info.plist",
	"Makefile", "Dockerfile", "docker-compose.yml", "docker-compose.yaml",
}

var oversightCriticalDirs = []string{
	".github/workflows/", ".gitea/workflows/", ".circleci/", ".gitlab-ci",
}

var oversightIrreversibleTools = map[string]bool{
	"git_push":  true,
	"git_reset": true,
}

type oversightItem struct {
	Tool   string
	Target string
	Reason string
}

type oversightTriageState struct {
	mu           sync.Mutex
	novel        []oversightItem
	routineCount int
	emitted      bool
}

func newOversightTriageState() *oversightTriageState {
	return &oversightTriageState{}
}

func (o *oversightTriageState) reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.novel = nil
	o.routineCount = 0
	o.emitted = false
}

func (o *oversightTriageState) record(tc provider.ToolCallDelta) {
	var args map[string]any
	if len(tc.Arguments) > 0 {
		_ = json.Unmarshal(tc.Arguments, &args)
	}
	item, novel := classifyOversight(tc.Name, args)
	o.mu.Lock()
	defer o.mu.Unlock()
	if !novel {
		o.routineCount++
		return
	}
	if len(o.novel) < 10 {
		o.novel = append(o.novel, item)
	}
}

func oversightArgStr(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := args[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func oversightExtractPath(tool string, args map[string]any) string {
	switch tool {
	case "edit_file", "write_file", "multi_edit_file":
		return oversightArgStr(args, "file_path")
	case "multi_file_edit", "multi_file_write":
		if files, ok := args["files"].([]any); ok {
			return fmt.Sprintf("%d files", len(files))
		}
		return ""
	case "batch_replace":
		if files, ok := args["files"].([]any); ok {
			return fmt.Sprintf("%d files", len(files))
		}
		return oversightArgStr(args, "pattern")
	case "file_ops":
		return oversightArgStr(args, "source")
	case "git_reset":
		return oversightArgStr(args, "mode")
	default:
		return ""
	}
}

func oversightIsCriticalPath(p string) bool {
	if p == "" {
		return false
	}
	low := strings.ToLower(p)
	for _, suffix := range oversightCriticalFiles {
		if strings.HasSuffix(low, suffix) {
			return true
		}
	}
	for _, dir := range oversightCriticalDirs {
		if strings.Contains(low, dir) {
			return true
		}
	}
	return false
}

func oversightWideBlast(tool string, args map[string]any) bool {
	if tool != "batch_replace" && tool != "multi_file_edit" && tool != "multi_file_write" {
		return false
	}
	files, ok := args["files"].([]any)
	return ok && len(files) > 5
}

func oversightFileOpsIsDelete(args map[string]any) bool {
	for _, opsKey := range []string{"operations", "ops"} {
		if raw, ok := args[opsKey]; ok {
			if ops, ok := raw.([]any); ok {
				for _, o := range ops {
					if m, ok := o.(map[string]any); ok {
						if a, ok := m["action"].(string); ok && a == "delete" {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func classifyOversight(tool string, args map[string]any) (oversightItem, bool) {
	target := oversightExtractPath(tool, args)
	switch {
	case tool == "file_ops" && oversightFileOpsIsDelete(args):
		return oversightItem{Tool: tool, Target: target, Reason: "destructive delete"}, true
	case oversightIrreversibleTools[tool]:
		reason := "irreversible remote/history op"
		if tool == "git_reset" && target == "hard" {
			reason = "hard reset (discards work permanently)"
		}
		return oversightItem{Tool: tool, Target: target, Reason: reason}, true
	case oversightWideBlast(tool, args):
		return oversightItem{Tool: tool, Target: target, Reason: "wide blast radius (>5 files in one call)"}, true
	case (tool == "edit_file" || tool == "write_file" || tool == "multi_edit_file") &&
		oversightIsCriticalPath(target):
		return oversightItem{Tool: tool, Target: target, Reason: "supply-chain / build-surface file"}, true
	default:
		return oversightItem{}, false
	}
}

func (o *oversightTriageState) digest() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.novel) == 0 || o.emitted {
		return ""
	}
	o.emitted = true
	var sb strings.Builder
	fmt.Fprintf(&sb, "[oversight digest] %d novel decision(s) this run deserve a human glance:\n", len(o.novel))
	for i, item := range o.novel {
		line := fmt.Sprintf("  %d. %s", i+1, item.Tool)
		if item.Target != "" {
			line += " " + item.Target
		}
		line += " - " + item.Reason
		sb.WriteString(line + "\n")
	}
	if o.routineCount > 0 {
		fmt.Fprintf(&sb, "(+%d routine actions folded - reads, single-file edits, builds, tests)", o.routineCount)
	}
	return sb.String()
}
