package agent

// Information Foraging Patch Exhaustion Detector
//
// Research basis: Information Foraging Theory (IFT), originally Pirolli & Card
// (1999), newly formalized for LLM agents by InForage (arXiv:2505.09316, 2025)
// which models retrieval-augmented reasoning as information foraging. A core
// IFT principle is the "give-up rule": a forager exploiting an information
// patch faces diminishing returns: each successive probe yields less novel
// information than the last. The rational strategy is to abandon an exhausted
// patch and seek a fresh one, rather than persistently re-probing the same
// source for data that is already in context.
//
// In coding agents this manifests as the "patch exhaustion" anti-pattern:
// the agent repeatedly reads files within the SAME directory (or the same
// file with slight offset variation) across consecutive iterations without
// producing any intermediate edit or output. Each re-read injects content
// that is largely redundant with what is already in context, a context-budget
// tax with near-zero information gain.
//
// How this differs from existing detectors:
//   - redundant_read_guard: flags re-reading ONE unchanged file (file-level,
//     mtime-based). Patch exhaustion operates at the DIRECTORY level and fires
//     even when different files are read (the patch is exhausted, not one file).
//   - serial_read: flags adjacent-file sequential reads (reading a/b/c in
//     alphabetical order). Patch exhaustion fires for reads concentrated in
//     one directory regardless of file ordering.
//   - wasted_explore: flags many reads with NO edits at all. Patch exhaustion
//     is more specific: it flags reads OVER-CONCENTRATED in one location even
//     when other reads happen elsewhere; the spatial concentration is the
//     signal that the agent is stuck in a depleted patch.
//   - tunnel_vision: tracks breadth of files touched. Patch exhaustion tracks
//     TEMPORAL concentration (consecutive reads into the same dir) combined
//     with absence of edits, which tunnel_vision alone does not detect.
//
// Detection heuristic:
//   1. Normalize each read path to its parent directory ("patch").
//   2. Track consecutive reads landing in the same patch with no edit
//      committed in between.
//   3. When the consecutive same-patch count exceeds the give-up threshold
//      (default 4) and no edit has occurred, emit a patch-exhaustion hint
//      advising the agent to move to a different area of the codebase or
//      to consolidate what it has and act on it.
//   4. Fires at most twice per run (avoid nagging; the first nudge may be
//      ignored legitimately while gathering final context).
//   5. Zero LLM cost — deterministic directory counting.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// patchGiveUpThreshold: number of consecutive reads into the same
	// directory patch (with no intervening edit) before flagging exhaustion.
	// 4 is chosen so that a legitimate "scan 3 files in a package" workflow
	// does not trigger, but persistent re-mining of one area does.
	patchGiveUpThreshold = 4

	// patchMaxFires: cap total hints per run to avoid nagging once the
	// pattern is established.
	patchMaxFires = 2
)

// patchExhaustState tracks directory-level read concentration for IFT
// patch-exhaustion detection.
type patchExhaustState struct {
	// currentPatch is the normalized directory of the most recent read.
	currentPatch string
	// consecutiveCount is how many reads in a row landed in currentPatch
	// without an edit resetting the counter.
	consecutiveCount int
	// lastPatch tracks the patch before the current one, so a single
	// excursion to another dir (read elsewhere) then back does not reset
	// the counter — only an EDIT resets it.
	lastPatch string
	// patchReadCounts tallies total reads per patch this run (for the hint
	// message and for detecting a dominant patch).
	patchReadCounts map[string]int
	// fires counts how many hints have been emitted this run.
	fires int
}

func newPatchExhaustState() *patchExhaustState {
	return &patchExhaustState{
		patchReadCounts: make(map[string]int),
	}
}

func (p *patchExhaustState) reset() {
	p.currentPatch = ""
	p.lastPatch = ""
	p.consecutiveCount = 0
	p.patchReadCounts = make(map[string]int)
	p.fires = 0
}

// patchOf normalizes a file path to its directory "patch".
// For files at the repo root, the patch is ".".
func patchOf(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	dir := filepath.Dir(path)
	// Normalize: lowercase, clean redundant separators, strip trailing slash.
	dir = filepath.Clean(dir)
	return dir
}

// recordRead accounts a read into the patch-exhaustion tracker and returns a
// non-empty hint if the agent is over-mining a single exhausted patch.
func (p *patchExhaustState) recordRead(path string) string {
	if path == "" {
		return ""
	}
	if p.patchReadCounts == nil {
		p.patchReadCounts = make(map[string]int)
	}
	patch := patchOf(path)
	if patch == "" {
		return ""
	}
	p.patchReadCounts[patch]++

	// Track consecutive reads into the same patch.
	if patch == p.currentPatch {
		p.consecutiveCount++
	} else {
		// Moved to a different patch. Stash the old one and start fresh.
		p.lastPatch = p.currentPatch
		p.currentPatch = patch
		p.consecutiveCount = 1
	}

	// Check give-up condition.
	if p.consecutiveCount >= patchGiveUpThreshold && p.fires < patchMaxFires {
		p.fires++
		n := p.consecutiveCount
		debug.Log("agent", "patch-exhaustion: %d consecutive reads into %s with no edit (IFT give-up rule)", n, patch)
		return fmt.Sprintf(
			"[Foraging hint] You have read %d files in %s without making any edits. "+
				"Information Foraging Theory calls this an exhausted patch: each further read yields diminishing new information. "+
				"Consider acting on what you have (edit/verify), or shift to a different area of the codebase where fresh information remains.",
			n, patch,
		)
	}
	return ""
}

// recordEdit resets the consecutive-read counter because the agent produced
// an intermediate output, indicating the patch was productively exploited.
func (p *patchExhaustState) recordEdit(path string) {
	if path == "" {
		return
	}
	// An edit means the agent acted on gathered information — the patch is
	// no longer being passively over-mined. Reset consecutive tracking.
	p.consecutiveCount = 0
	p.currentPatch = ""
}
