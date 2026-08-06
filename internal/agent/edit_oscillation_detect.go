package agent

// Edit Oscillation Detector - Semantic Back-and-Forth Detection
//
// Research basis:
//   - "Convergence Detection in Iterative Agent Refinement" (agentpatterns.ai, 2026):
//     identifies OSCILLATION as a critical failure pattern: "output alternates
//     between two versions across passes; the agent cannot resolve a trade-off
//     without external input." When detected, a restart is needed rather than
//     continued iteration.
//   - RefineBench (Lee et al., 2025): self-refinement without external feedback
//     yields +1.8pp or less over 5 iterations; models routinely get stuck in
//     oscillation loops - re-adding code they previously removed, or re-removing
//     code they previously added.
//   - "Evaluating Goal Drift in Language Model Agents" (Arike et al., 2025):
//     pattern-matching behavior deep in context drives oscillation - the agent
//     matches the pattern of its earlier edits rather than converging.
//
// What it detects: When the agent semantically reverts its own work - editing
// the same file's old_text to contain what it previously had as new_text, or
// vice versa. This indicates the agent is oscillating between two approaches
// without resolving the underlying trade-off.
//
// This is different from:
//   - fileChurn: tracks edit COUNT per file (assumption invalidation)
//   - convergenceLock: tracks edits AFTER successful verification
//   - fixCascade: tracks edit-verify-fail cycles
//   - convergence_lock: unnecessary edits post-verification
// Edit oscillation tracks SEMANTIC REVERSAL - the agent undoing its own prior
// change, specifically via content signature matching.
//
// The detector extracts content fingerprints from edit_file/multi_edit_file
// old_text and new_text fields. When it detects that an edit's old_text matches
// a prior new_text (or new_text matches a prior old_text) for the same file,
// it counts a reversal. After 2+ reversals on the same file, it fires.
//
// Zero LLM cost. Non-blocking. Fires at most once per run.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// oscillationReversalThreshold: number of content reversals needed to trigger
	oscillationReversalThreshold = 2

	// oscillationMaxTracked: maximum files to track signatures for
	oscillationMaxTracked = 30

	// oscillationSigMaxLen: truncate content to this many bytes before hashing
	// to avoid excessive memory use on large edits
	oscillationSigMaxLen = 2048

	// oscillationMaxWarnings: max times the detector fires per run
	oscillationMaxWarnings = 1
)

// oscillationState tracks content signatures per file to detect reversals.
type oscillationState struct {
	// signatures maps file path to a list of content signatures seen,
	// alternating between old_text sigs and new_text sigs.
	signatures map[string][]sigEntry
	fired      int
}

type sigEntry struct {
	sig     string // content fingerprint
	isNew   bool   // true = new_text sig, false = old_text sig
	iterTag int    // iteration number for debugging
}

func newOscillationState() *oscillationState {
	return &oscillationState{
		signatures: make(map[string][]sigEntry),
	}
}

func (o *oscillationState) reset() {
	o.signatures = make(map[string][]sigEntry)
	o.fired = 0
}

// contentSig produces a short fingerprint of edit content for comparison.
func contentSig(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > oscillationSigMaxLen {
		text = text[:oscillationSigMaxLen]
	}
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:8])
}

// recordEdit tracks old_text/new_text content signatures for edited files.
// args is the raw JSON arguments from the tool call.
func (o *oscillationState) recordEdit(toolName string, args json.RawMessage, iteration int) {
	if len(args) == 0 {
		return
	}
	var parsed map[string]any
	if json.Unmarshal(args, &parsed) != nil {
		return
	}

	switch toolName {
	case "edit_file":
		path, oldT, newT := extractEditContent(parsed)
		if path == "" {
			return
		}
		o.addSignature(path, oldT, newT, iteration)

	case "multi_edit_file":
		path, edits := extractMultiEditContent(parsed)
		if path == "" || len(edits) == 0 {
			return
		}
		// Combine all old/new text into aggregate signatures
		var allOld, allNew strings.Builder
		for _, e := range edits {
			allOld.WriteString(e.oldText)
			allNew.WriteString(e.newText)
		}
		o.addSignature(path, allOld.String(), allNew.String(), iteration)
	}
}

type multiEditPair struct {
	oldText string
	newText string
}

func extractEditContent(args map[string]any) (path, oldText, newText string) {
	if p, ok := args["path"].(string); ok {
		path = p
	}
	if p, ok := args["file_path"].(string); ok {
		path = p
	}
	if v, ok := args["old_text"].(string); ok {
		oldText = v
	}
	if v, ok := args["new_text"].(string); ok {
		newText = v
	}
	return
}

func extractMultiEditContent(args map[string]any) (path string, edits []multiEditPair) {
	if p, ok := args["path"].(string); ok {
		path = p
	}
	if p, ok := args["file_path"].(string); ok {
		path = p
	}
	if files, ok := args["edits"].([]any); ok {
		for _, f := range files {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			var pair multiEditPair
			if v, ok := fm["old_text"].(string); ok {
				pair.oldText = v
			}
			if v, ok := fm["new_text"].(string); ok {
				pair.newText = v
			}
			if pair.oldText != "" || pair.newText != "" {
				edits = append(edits, pair)
			}
		}
	}
	return
}

func (o *oscillationState) addSignature(path, oldText, newText string, iter int) {
	if len(o.signatures) >= oscillationMaxTracked {
		if _, exists := o.signatures[path]; !exists {
			return // at capacity, don't track new files
		}
	}

	oldSig := contentSig(oldText)
	newSig := contentSig(newText)

	// Skip if old and new are identical (no-op edit)
	if oldSig == newSig || (oldText == "" && newText == "") {
		return
	}

	sigs := o.signatures[path]

	// Track old_text and new_text signatures
	if oldSig != "" {
		sigs = append(sigs, sigEntry{sig: oldSig, isNew: false, iterTag: iter})
	}
	if newSig != "" {
		sigs = append(sigs, sigEntry{sig: newSig, isNew: true, iterTag: iter})
	}

	// Trim old entries to prevent unbounded growth
	if len(sigs) > 20 {
		sigs = sigs[len(sigs)-20:]
	}

	o.signatures[path] = sigs
}

// countReversals counts how many times a new_text signature matches a prior
// old_text signature (or vice versa) - indicating the agent reverted its change.
func (o *oscillationState) countReversals(path string) int {
	sigs := o.signatures[path]
	if len(sigs) < 3 {
		return 0
	}

	reversals := 0
	// Track new_text sigs we've seen
	seenNewSigs := make(map[string]bool)
	// Track old_text sigs we've seen
	seenOldSigs := make(map[string]bool)

	for _, s := range sigs {
		if s.isNew {
			// If this new_text sig was previously seen as an old_text sig,
			// the agent is re-adding something it previously removed
			if seenOldSigs[s.sig] {
				reversals++
			}
			seenNewSigs[s.sig] = true
		} else {
			// If this old_text sig was previously seen as a new_text sig,
			// the agent is removing something it previously added
			if seenNewSigs[s.sig] {
				reversals++
			}
			seenOldSigs[s.sig] = true
		}
	}

	return reversals
}

// check returns guidance if oscillation is detected.
func (o *oscillationState) check() string {
	if o.fired >= oscillationMaxWarnings {
		return ""
	}

	var oscillatingFiles []string
	for path := range o.signatures {
		if o.countReversals(path) >= oscillationReversalThreshold {
			oscillatingFiles = append(oscillatingFiles, path)
		}
	}

	if len(oscillatingFiles) == 0 {
		return ""
	}

	o.fired++
	debug.Log("edit-oscillation", "oscillation detected on %d files: %v", len(oscillatingFiles), oscillatingFiles)

	fileList := oscillatingFiles
	if len(fileList) > 3 {
		fileList = fileList[:3]
	}

	var b strings.Builder
	b.WriteString("[edit-oscillation] Detected semantic back-and-forth on ")
	switch len(oscillatingFiles) {
	case 1:
		b.WriteString("1 file")
	default:
		fmt.Fprintf(&b, "%d files", len(oscillatingFiles))
	}
	b.WriteString(":\n")
	for _, f := range fileList {
		fmt.Fprintf(&b, "  - %s\n", f)
	}
	b.WriteString("\n")
	b.WriteString("You are oscillating between two approaches - editing the same content back and forth ")
	b.WriteString("without resolving the underlying trade-off. This wastes tokens and risks instability.\n")
	b.WriteString("Recommended actions:\n")
	b.WriteString("1. Step back and re-read the file holistically to understand the current state\n")
	b.WriteString("2. Decide which approach is correct and commit to it - do not partially apply both\n")
	b.WriteString("3. If unsure which approach is better, explain the trade-off to the user rather than continuing to flip\n")

	return b.String()
}
