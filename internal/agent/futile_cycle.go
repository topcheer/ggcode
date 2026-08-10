package agent

// Futile Cycle Detector -- Read Working-Set Revisit Without Progress
//
// Research basis: IBM Research (2025), "Unsupervised Approaches to Futile Cycle
// Detection in AI Agents" -- introduces the concept of "repetitive futile cycles":
// loops of unproductive behavior where the agent revisits previously explored
// state-space regions without making forward progress. Unlike exact-match loops,
// futile cycles are semantically repetitive: the agent re-reads the same files,
// re-searches similar patterns, and re-examines the same code regions, but with
// different specific arguments each time.
//
// Forrester (2025) and the "Context Drift" literature (nool.dev, agiflow.io)
// identify a related failure: "durable state loss" -- after exploring broadly,
// the agent circles back to already-explored files because it lost track of what
// it already learned. This manifests as a read working-set cycle.
//
// Key insight from the IBM paper: futile cycles are distinguished from
// productive cycles by the ABSENCE of state mutation. If the agent reads files
// A, B, C, then reads A, B, C again without any edits in between, it's stuck in
// a futile cycle. The agent is "exploring" but achieving nothing.
//
// This is different from existing detectors:
//   - repetition_tracker: same tool + same file + error (needs failures)
//   - empty_search_tracker: zero-result searches
//   - wasted_explore: unused search results
//   - loop_detect: exact argument match
//   - redundant_read: re-reading same file without edit
//   - query_converge: repeated similar search queries
//
// Our approach: track the set of distinct file paths READ per "epoch" (a span
// between writes). When two consecutive epochs have >=70% Jaccard overlap in
// their read sets AND no writes occurred in the current epoch, inject guidance.
// This catches the "circular exploration" anti-pattern with zero LLM cost.

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// futileMinReadSet: minimum distinct files in an epoch's read set to
	// consider overlap meaningful. With <3 files, revisit is normal (reading
	// the same few files while iterating on a fix is productive).
	futileMinReadSet = 3

	// futileOverlapThreshold: Jaccard similarity threshold (0-1) for declaring
	// two epochs "circular". 0.7 means 70% of the union is shared.
	futileOverlapThreshold = 0.7

	// futileMaxWarnings: cap warnings per run to avoid noise.
	futileMaxWarnings = 2

	// futileMaxEpochs: retain this many epochs for comparison.
	futileMaxEpochs = 3
)

// futileCycleState tracks read working-sets between writes.
type futileCycleState struct {
	// currentEpoch: files read since the last write (or run start).
	currentEpoch map[string]bool

	// pastEpochs: read-sets from previous write-free spans, oldest first.
	pastEpochs []map[string]bool

	// warningsFired: how many times we've warned this run.
	warningsFired int

	// lastWarnedEpoch: prevent re-warning for the same pair of epochs.
	lastWarnedEpoch int
}

func newFutileCycleState() *futileCycleState {
	return &futileCycleState{
		currentEpoch: make(map[string]bool),
	}
}

func (f *futileCycleState) reset() {
	f.currentEpoch = make(map[string]bool)
	f.pastEpochs = nil
	f.warningsFired = 0
	f.lastWarnedEpoch = 0
}

// recordRead adds a file path to the current epoch's read set.
func (f *futileCycleState) recordRead(filePath string) {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return
	}
	// Normalize: keep last 3 path components for cross-platform consistency.
	// This also reduces noise from absolute path differences.
	parts := strings.Split(strings.ReplaceAll(filePath, "\\", "/"), "/")
	if len(parts) > 3 {
		filePath = strings.Join(parts[len(parts)-3:], "/")
	}
	f.currentEpoch[filePath] = true
}

// recordWrite finalizes the current epoch and starts a new one.
// A write breaks the futile cycle -- it means the agent acted on its exploration.
func (f *futileCycleState) recordWrite() {
	if len(f.currentEpoch) == 0 {
		return // nothing was read; no epoch to finalize
	}
	f.pastEpochs = append(f.pastEpochs, f.currentEpoch)
	if len(f.pastEpochs) > futileMaxEpochs {
		f.pastEpochs = f.pastEpochs[1:]
	}
	f.currentEpoch = make(map[string]bool)
}

// maybeWarn checks if the current read-set overlaps a previous epoch enough
// to constitute a futile cycle. Returns guidance if so.
func (f *futileCycleState) maybeWarn(iteration int) string {
	if f.warningsFired >= futileMaxWarnings {
		return ""
	}
	if len(f.currentEpoch) < futileMinReadSet {
		return ""
	}
	if len(f.pastEpochs) == 0 {
		return ""
	}

	// Compare current epoch against each past epoch.
	for i, past := range f.pastEpochs {
		if len(past) < futileMinReadSet {
			continue
		}
		jaccard := futileJaccard(f.currentEpoch, past)
		if jaccard >= futileOverlapThreshold {
			// Avoid re-warning for the same epoch pair.
			if i == f.lastWarnedEpoch && f.warningsFired > 0 {
				continue
			}
			f.warningsFired++
			f.lastWarnedEpoch = i

			overlapFiles := futileIntersection(f.currentEpoch, past)
			sample := futileTopFiles(overlapFiles, 4)

			debug.Log("futile_cycle", "warning at iter %d: Jaccard=%.2f, %d shared files", iteration, jaccard, len(overlapFiles))

			return fmt.Sprintf(
				"[futile-cycle] Re-reading same files (%s) without edits. Act on existing knowledge or explore new files.",
				sample,
			)
		}
	}

	return ""
}

// futileJaccard computes the Jaccard similarity between two sets: |A∩B| / |A∪B|.
func futileJaccard(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	intersection := 0
	for k := range a {
		if b[k] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// futileIntersection returns the set of keys present in both maps.
func futileIntersection(a, b map[string]bool) map[string]bool {
	result := make(map[string]bool)
	for k := range a {
		if b[k] {
			result[k] = true
		}
	}
	return result
}

// futileTopFiles returns up to n file paths from the set, formatted as a string.
func futileTopFiles(files map[string]bool, n int) string {
	var result []string
	count := 0
	for f := range files {
		result = append(result, f)
		count++
		if count >= n {
			break
		}
	}
	if len(files) > n {
		return strings.Join(result, ", ") + ", ..."
	}
	return strings.Join(result, ", ")
}
