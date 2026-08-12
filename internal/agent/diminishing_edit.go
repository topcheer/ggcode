package agent

// Diminishing Returns Edit Detector -- Polish Spiral Awareness
//
// Research basis:
//   - "Semantic Early-Stopping for Iterative LLM Agent Loops" (arXiv:2606.27009):
//     Fixed iteration caps are "syntactic kill-switches" blind to whether the answer
//     is still improving. Agents over-spend tokens on easy/done tasks.
//   - "Agentic Confidence Calibration" (2026): agents continue editing after
//     improvements have plateaued -- diminishing marginal returns per edit.
//   - "Tokenomics: Quantifying Where Tokens Are Used" (2026): late-iteration
//     edits are dominated by cosmetic/polish work (comments, formatting, renames).
//
// Problem this solves:
//   Existing detectors check: *are we editing at all?* (momentum_loss),
//   *did we pass verify?* (convergence_lock), *are we on pace?* (velocity_forecast).
//   NONE detect the "polish spiral" -- the agent keeps editing (looks productive to
//   other detectors) but each edit is tinier than the last. The agent is polishing
//   whitespace, tweaking comments, renaming variables -- burning context for zero
//   substantive value. This is the most common reason agents hit iteration limits
//   on tasks that were effectively complete 5 iterations ago.
//
// Approach:
//   Track a sliding window of the last N edits, measuring each edit's "substance
//   size" (bytes of old_text+new_text for edits, content length for writes). When
//   the most recent 3+ edits are each smaller than the ones before them (strictly
//   decreasing trend) AND the latest edits are below a "trivial" threshold, inject
//   guidance to stop polishing and finalize.
//
// Interaction with existing guards:
//   - convergence_lock: fires AFTER verification passes; this fires regardless
//   - momentum_loss: fires when edits STOP entirely; this fires when edits SHRINK
//   - file_churn_detect: fires when same file is edited repeatedly; this fires on
//     edit SIZE trajectory across any files
//   - edit_oscillation: detects semantic content reversal; this detects quantity decline

import (
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// diminishingWindow tracks the last N edit sizes for trend analysis.
	diminishingWindow = 6

	// diminishingMinEdits: minimum edits in the window before analysis kicks in.
	// Avoids false positives on naturally small initial edits.
	diminishingMinEdits = 4

	// diminishingTrivialSize: edits below this byte count are considered "trivial"
	// (whitespace, single-line comments, short renames). ~200 bytes ≈ 3-4 lines.
	diminishingTrivialSize = 200

	// diminishingShrinkRatio: if the average recent edit size is less than this
	// fraction of the average earlier edit size, the trend is "diminishing."
	diminishingShrinkRatio = 0.4
)

// editSizeEntry records the substance size of a single edit.
type editSizeEntry struct {
	toolName string
	size     int
	filePath string
}

// diminishingEditState tracks edit substance sizes for trend analysis.
type diminishingEditState struct {
	entries []editSizeEntry
	warned  bool
}

func newDiminishingEditState() *diminishingEditState {
	return &diminishingEditState{}
}

func (s *diminishingEditState) reset() {
	s.entries = nil
	s.warned = false
}

// recordEdit logs a successful edit with its computed substance size.
func (s *diminishingEditState) recordEdit(toolName string, size int, filePath string) {
	if size < 0 {
		size = 0
	}
	s.entries = append(s.entries, editSizeEntry{
		toolName: toolName,
		size:     size,
		filePath: filePath,
	})
	// Keep window bounded.
	if len(s.entries) > diminishingWindow {
		s.entries = s.entries[len(s.entries)-diminishingWindow:]
	}
}

// check returns guidance if the polish-spiral pattern is detected.
func (s *diminishingEditState) check() string {
	if s.warned {
		return ""
	}
	n := len(s.entries)
	if n < diminishingMinEdits {
		return ""
	}

	// Split window into "earlier" (first half) and "recent" (second half).
	mid := n / 2
	if mid < 1 {
		return ""
	}
	earlier := s.entries[:mid]
	recent := s.entries[mid:]

	avgEarlier := avgEditSize(earlier)
	avgRecent := avgEditSize(recent)

	// Need enough earlier substance to have a meaningful decline.
	if avgEarlier < diminishingTrivialSize*2 {
		return ""
	}

	// Check: recent edits are significantly smaller than earlier edits.
	ratio := 1.0
	if avgEarlier > 0 {
		ratio = float64(avgRecent) / float64(avgEarlier)
	}

	// Check: at least one recent edit is trivially small.
	hasTrivial := false
	for _, e := range recent {
		if e.size <= diminishingTrivialSize {
			hasTrivial = true
			break
		}
	}

	if ratio > diminishingShrinkRatio || !hasTrivial {
		return ""
	}

	s.warned = true
	debug.Log("diminishing_edit", "polish spiral: avg earlier=%d bytes, avg recent=%d bytes (ratio=%.2f), trivial edits present",
		avgEarlier, avgRecent, ratio)

	return fmt.Sprintf("%s%d%s%d%s%.0f%s",
		"Diminishing returns: Your recent edits (avg ", avgRecent, " bytes) are much smaller than earlier edits (avg ", avgEarlier, " bytes) -- a ", (1.0-ratio)*100, "%% decline. "+
			"At least one recent edit is trivially small (whitespace, comments, or minor renames). "+
			"This is a polish spiral: each edit adds less value than the last while consuming context and iterations. "+
			"If the core task is complete, stop editing and summarize what was done. "+
			"If substantive work remains, refocus on the highest-impact change rather than incremental polish.")
}

// avgEditSize computes the mean substance size across entries.
func avgEditSize(entries []editSizeEntry) int {
	if len(entries) == 0 {
		return 0
	}
	total := 0
	for _, e := range entries {
		total += e.size
	}
	return total / len(entries)
}

// measureEditSize computes the substance size (total bytes of content being changed)
// from the tool call arguments. For edit_file, this is len(old_text)+len(new_text).
// For multi_edit_file/multi_file_edit, it sums across all sub-edits.
// For write_file, it is len(content).
func measureEditSize(toolName string, args json.RawMessage) int {
	if len(args) == 0 {
		return 0
	}

	switch toolName {
	case "edit_file":
		var p struct {
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
		}
		if json.Unmarshal(args, &p) != nil {
			return 0
		}
		// Measure only the delta, not the anchor. The old_text in edit_file is
		// a context anchor required to locate the edit, not the change itself.
		// Counting it caused systematic false positives in overcorrection
		// detection (issue #26).
		delta := len(p.NewText) - len(p.OldText)
		if delta < 0 {
			delta = -delta
		}
		return delta

	case "multi_edit_file":
		var p struct {
			Edits []struct {
				OldText string `json:"old_text"`
				NewText string `json:"new_text"`
			} `json:"edits"`
		}
		if json.Unmarshal(args, &p) != nil {
			return 0
		}
		total := 0
		for _, e := range p.Edits {
			total += len(e.OldText) + len(e.NewText)
		}
		return total

	case "multi_file_edit":
		var p struct {
			Files []struct {
				Edits []struct {
					OldText string `json:"old_text"`
					NewText string `json:"new_text"`
				} `json:"edits"`
			} `json:"files"`
		}
		if json.Unmarshal(args, &p) != nil {
			return 0
		}
		total := 0
		for _, f := range p.Files {
			for _, e := range f.Edits {
				total += len(e.OldText) + len(e.NewText)
			}
		}
		return total

	case "write_file":
		var p struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(args, &p) != nil {
			return 0
		}
		return len(p.Content)

	case "batch_replace":
		var p struct {
			Files []struct {
				Pattern     string `json:"pattern"`
				Replacement string `json:"replacement"`
			} `json:"files"`
		}
		if json.Unmarshal(args, &p) != nil {
			return 0
		}
		total := 0
		for _, f := range p.Files {
			total += len(f.Pattern) + len(f.Replacement)
		}
		return total

	default:
		return 0
	}
}

// --- Agent integration ---

// diminishingRecordEdit records a successful edit's substance size.
func (a *Agent) diminishingRecordEdit(toolName string, args []byte) {
	if a.diminishingEdit == nil {
		return
	}
	if !productiveEditTools[toolName] {
		return
	}
	size := measureEditSize(toolName, json.RawMessage(args))
	filePath := firstEditFilePath(toolName, args)
	a.diminishingEdit.recordEdit(toolName, size, filePath)
}

// diminishingCheck returns polish-spiral guidance if applicable.
func (a *Agent) diminishingCheck() string {
	if a.diminishingEdit == nil {
		return ""
	}
	return a.diminishingEdit.check()
}

// resetDiminishingEdit clears state for a new run.
func (a *Agent) resetDiminishingEdit() {
	if a.diminishingEdit != nil {
		a.diminishingEdit.reset()
	}
}

// firstEditFilePath extracts the first file path from edit arguments (for logging).
func firstEditFilePath(toolName string, args []byte) string {
	if len(args) == 0 {
		return ""
	}
	switch toolName {
	case "edit_file", "write_file":
		var p struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(args, &p) == nil {
			return p.Path
		}
	case "multi_edit_file":
		var p struct {
			Path string `json:"file_path"`
		}
		if json.Unmarshal(args, &p) == nil {
			return p.Path
		}
	case "multi_file_edit":
		var p struct {
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		if json.Unmarshal(args, &p) == nil && len(p.Files) > 0 {
			return p.Files[0].Path
		}
	}
	return ""
}
