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
//   2. Track reads into the same patch with no edit committed in between.
//      A SINGLE one-read excursion to another directory does not reset the
//      streak — returning resumes the stashed count (#486: this tolerance
//      was documented since inception but never implemented; the lastPatch
//      field was write-only). A second departure from a resumed streak
//      hard-resets, so ping-ponging between two directories does not
//      accumulate. Only an EDIT fully resets the streak.
//   3. When the same-patch read count exceeds the give-up threshold
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
	// consecutiveCount is the read count accumulated for currentPatch
	// (including its pre-excursion stash when the streak was resumed).
	consecutiveCount int
	// lastPatch/lastPatchCount stash the prior patch and its count across a
	// SINGLE one-read excursion, so returning does not reset the streak
	// (#486 — implemented as documented; previously write-only dead state).
	lastPatch      string
	lastPatchCount int
	// resumedFromExcursion marks a streak that was resumed after an
	// excursion. Leaving a resumed streak again hard-resets (no second
	// tolerance), so two-directory ping-pong cannot accumulate.
	resumedFromExcursion bool
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
	p.lastPatchCount = 0
	p.resumedFromExcursion = false
	p.consecutiveCount = 0
	p.patchReadCounts = make(map[string]int)
	p.fires = 0
}

// patchOf normalizes a file path to its directory "patch".
// For files at the repo root, the patch is ".".
// Paths are canonicalized via weNormalizePath (#486, same normalization as
// wasted_explore #482): "./a/b.go" from one tool and "a/b.go" from another
// must land in the SAME patch, otherwise counts fragment per format.
// Case is NOT folded: on case-sensitive filesystems Agent.go and agent.go
// are distinct files; folding would merge unrelated patches.
func patchOf(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	// Directory-style input (trailing separator, e.g. a list_directory
	// target): the directory itself is the patch — Dir would strip one
	// level too many after Clean removed the trailing slash.
	if strings.HasSuffix(path, "/") || strings.HasSuffix(path, "\\") {
		if n := weNormalizePath(path); n != "" {
			return n
		}
		return "."
	}
	return filepath.Dir(weNormalizePath(path))
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

	// Track reads into the same patch, tolerating a SINGLE one-read
	// excursion (#486): returning to the prior patch resumes its stashed
	// count instead of restarting. A second departure from a resumed
	// streak hard-resets (two-dir ping-pong must not accumulate), and only
	// an EDIT fully resets the streak.
	if patch == p.currentPatch {
		p.consecutiveCount++
	} else if patch == p.lastPatch && p.lastPatchCount > 0 {
		// Back from a single-read excursion: resume the stashed streak.
		p.consecutiveCount = p.lastPatchCount + 1
		p.lastPatch, p.lastPatchCount = "", 0
		p.resumedFromExcursion = true
	} else if p.resumedFromExcursion {
		// Leaving a resumed streak: excursion tolerance is one-shot.
		p.lastPatch, p.lastPatchCount = "", 0
		p.resumedFromExcursion = false
		p.consecutiveCount = 1
	} else {
		// First departure from a fresh streak: stash it for potential resume.
		p.lastPatch, p.lastPatchCount = p.currentPatch, p.consecutiveCount
		p.consecutiveCount = 1
	}
	p.currentPatch = patch

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
	// no longer being passively over-mined. Reset streak AND excursion stash.
	p.consecutiveCount = 0
	p.currentPatch = ""
	p.lastPatch, p.lastPatchCount = "", 0
	p.resumedFromExcursion = false
}
