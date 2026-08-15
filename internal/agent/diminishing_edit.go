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

// editChangeDistance measures the substance of a single old->new text
// replacement without counting anchor context (#26) and without scoring
// equal-length rewrites as zero (#151). It trims the longest common prefix
// and suffix, then measures the remaining differing region (max of the two
// remainder lengths, so pure insertions/deletions and equal-length swaps
// both score proportionally to changed content).
func editChangeDistance(oldText, newText string) int {
	// Binary-safe common prefix/suffix trim over bytes.
	n, m := len(oldText), len(newText)
	maxLen := n
	if m > maxLen {
		maxLen = m
	}
	minLen := n
	if m < minLen {
		minLen = m
	}
	p := 0
	for p < minLen && oldText[p] == newText[p] {
		p++
	}
	s := 0
	for s < minLen-p && oldText[n-1-s] == newText[m-1-s] {
		s++
	}
	oldRem := n - p - s
	newRem := m - p - s
	if oldRem < 0 {
		oldRem = 0
	}
	if newRem < 0 {
		newRem = 0
	}
	if oldRem > newRem {
		return oldRem
	}
	return newRem
}

// measureEditSize computes the substance size (total bytes of content being changed)
// from the tool call arguments. For edit_file, this is editChangeDistance(old,new)
// — the differing region after common prefix/suffix trimming, so equal-length
// substantive rewrites (renames, operator fixes, API swaps) score non-zero (#151)
// while pure anchor context is not counted (#26).
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
		// #26: common prefix/suffix trimming keeps anchors out of the metric.
		// #151: the differing region is measured with max(oldRem,newRem), so
		// equal-length rewrites (e.g. "if x > 0" -> "if y < 9") score ~ their
		// changed bytes instead of 0.
		return editChangeDistance(p.OldText, p.NewText)

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
			total += editChangeDistance(e.OldText, e.NewText)
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
				total += editChangeDistance(e.OldText, e.NewText)
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

	case "multi_file_write":
		// #470: this tool is in productiveEditTools but had NO branch —
		// batch writes scored 0 bytes, dragging avgRecent toward the
		// trivial threshold and flagging high-impact refactors as polish
		// spirals. (The old batch_replace branch parsed the wrong arg shape
		// and returned 0 unconditionally; it is removed — batch_replace is
		// not in productiveEditTools anyway.)
		var p struct {
			Files []struct {
				Content string `json:"content"`
			} `json:"files"`
		}
		if json.Unmarshal(args, &p) != nil {
			return 0
		}
		total := 0
		for _, f := range p.Files {
			total += len(f.Content)
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
