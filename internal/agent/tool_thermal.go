package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	thermalMinSamples = 12

	explorationThreshold = 0.55

	modificationFloor = 0.15

	thermalWarnCooldown = 8
)

type thermalCategory int

const (
	thermalExplore thermalCategory = iota
	thermalModify
	thermalExecute
	thermalVerify
	thermalOther
)

var thermalCategoryNames = map[thermalCategory]string{
	thermalExplore: "exploration",
	thermalModify:  "modification",
	thermalExecute: "execution",
	thermalVerify:  "verification",
	thermalOther:   "other",
}

var toolCategoryMap = map[string]thermalCategory{

	"read_file":                  thermalExplore,
	"multi_file_read":            thermalExplore,
	"search_files":               thermalExplore,
	"grep":                       thermalExplore,
	"glob":                       thermalExplore,
	"list_directory":             thermalExplore,
	"code_search":                thermalExplore,
	"code_execution":             thermalExplore,
	"lsp_symbols":                thermalExplore,
	"lsp_definition":             thermalExplore,
	"lsp_references":             thermalExplore,
	"lsp_hover":                  thermalExplore,
	"lsp_workspace_symbols":      thermalExplore,
	"lsp_document_highlights":    thermalExplore,
	"lsp_implementation":         thermalExplore,
	"lsp_prepare_call_hierarchy": thermalExplore,
	"lsp_incoming_calls":         thermalExplore,
	"lsp_outgoing_calls":         thermalExplore,
	"lsp_code_actions":           thermalExplore,
	"web_search":                 thermalExplore,
	"web_fetch":                  thermalExplore,

	"screenshot": thermalExplore,

	"edit_file":        thermalModify,
	"write_file":       thermalModify,
	"multi_edit_file":  thermalModify,
	"multi_file_edit":  thermalModify,
	"multi_file_write": thermalModify,
	"batch_replace":    thermalModify,
	"lsp_rename":       thermalModify,
	"file_ops":         thermalModify,
	"notebook_edit":    thermalModify,

	"run_command":         thermalExecute,
	"start_command":       thermalExecute,
	"write_command_input": thermalExecute,
	"stop_command":        thermalExecute,

	"git_diff":        thermalVerify,
	"git_status":      thermalVerify,
	"git_log":         thermalVerify,
	"git_show":        thermalVerify,
	"git_blame":       thermalVerify,
	"lsp_diagnostics": thermalVerify,
	"code_health":     thermalVerify,
	"scan_todos":      thermalVerify,
}

type thermalState struct {
	categories [5]int

	total int

	lastExploreWarnIter int
	lastVerifyWarnIter  int
}

func newThermalState() *thermalState {
	return &thermalState{

		lastExploreWarnIter: -thermalWarnCooldown,
		lastVerifyWarnIter:  -thermalWarnCooldown,
	}
}

func (t *thermalState) reset() {
	for i := range t.categories {
		t.categories[i] = 0
	}
	t.total = 0
	t.lastExploreWarnIter = -thermalWarnCooldown
	t.lastVerifyWarnIter = -thermalWarnCooldown
}

func (t *thermalState) recordToolCall(toolName string) {
	cat, ok := toolCategoryMap[toolName]
	if !ok {
		cat = thermalOther
	}
	t.categories[cat]++
	t.total++
}

func (t *thermalState) fraction(cat thermalCategory) float64 {
	if t.total == 0 {
		return 0
	}
	return float64(t.categories[cat]) / float64(t.total)
}

func (t *thermalState) maybeWarn(iteration int) string {
	if t.total < thermalMinSamples {
		return ""
	}

	exploreFrac := t.fraction(thermalExplore)
	modifyFrac := t.fraction(thermalModify)
	verifyFrac := t.fraction(thermalVerify)

	if exploreFrac > explorationThreshold && modifyFrac < modificationFloor {

		if iteration-t.lastExploreWarnIter < thermalWarnCooldown {
			return ""
		}
		t.lastExploreWarnIter = iteration
		debug.Log("thermal-profile", "explore-heavy: explore=%.0f%% modify=%.0f%% total=%d",
			exploreFrac*100, modifyFrac*100, t.total)

		return fmt.Sprintf(
			"[tool balance] Exploration calls dominate (%.0f%% of %d calls) with only %.0f%% modification. "+
				"You may be over-reading. Apply what you've already learned: make changes, then verify. "+
				"Avoid re-reading files you've already inspected this run.",
			exploreFrac*100, t.total, modifyFrac*100,
		)
	}

	if verifyFrac > 0.30 && modifyFrac < modificationFloor {

		if iteration-t.lastVerifyWarnIter < thermalWarnCooldown {
			return ""
		}
		t.lastVerifyWarnIter = iteration
		debug.Log("thermal-profile", "verify-heavy: verify=%.0f%% modify=%.0f%% total=%d",
			verifyFrac*100, modifyFrac*100, t.total)

		return fmt.Sprintf(
			"[tool balance] Verification calls high (%.0f%% of %d) with minimal changes (%.0f%%). "+
				"Make your edits first, then verify once. Repeated checking without changes wastes iterations.",
			verifyFrac*100, t.total, modifyFrac*100,
		)
	}

	return ""
}

func (t *thermalState) categoryBreakdown() string {
	if t.total == 0 {
		return "no tool calls recorded"
	}
	var parts []string
	for cat := thermalExplore; cat <= thermalOther; cat++ {
		if t.categories[cat] > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d(%.0f%%)",
				thermalCategoryNames[cat], t.categories[cat], t.fraction(cat)*100))
		}
	}
	return strings.Join(parts, " ")
}
