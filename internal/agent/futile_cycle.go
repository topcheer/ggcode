package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	futileMinReadSet = 3

	futileOverlapThreshold = 0.7

	futileMaxWarnings = 1

	futileMaxEpochs = 3
)

type futileCycleState struct {
	currentEpoch map[string]bool

	pastEpochs []map[string]bool

	warningsFired int

	prevWarnedEpoch int
	canRevert       bool

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

func (f *futileCycleState) recordRead(filePath string) {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return
	}

	parts := strings.Split(strings.ReplaceAll(filePath, "\\", "/"), "/")
	if len(parts) > 3 {
		filePath = strings.Join(parts[len(parts)-3:], "/")
	}
	f.currentEpoch[filePath] = true
}

func (f *futileCycleState) recordWrite() {
	if len(f.currentEpoch) == 0 {
		return
	}
	f.pastEpochs = append(f.pastEpochs, f.currentEpoch)
	if len(f.pastEpochs) > futileMaxEpochs {
		f.pastEpochs = f.pastEpochs[1:]
	}
	f.currentEpoch = make(map[string]bool)
}

func (f *futileCycleState) markUndelivered() {
	if !f.canRevert || f.warningsFired == 0 {
		return
	}
	f.canRevert = false
	f.warningsFired--
	f.lastWarnedEpoch = f.prevWarnedEpoch
}

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

	for i, past := range f.pastEpochs {
		if len(past) < futileMinReadSet {
			continue
		}
		jaccard := futileJaccard(f.currentEpoch, past)
		if jaccard >= futileOverlapThreshold {

			if i == f.lastWarnedEpoch && f.warningsFired > 0 {
				continue
			}
			f.warningsFired++

			f.prevWarnedEpoch = f.lastWarnedEpoch
			f.canRevert = true
			f.lastWarnedEpoch = i

			overlapFiles := futileIntersection(f.currentEpoch, past)
			sample := futileTopFiles(overlapFiles, 4)

			debug.Log("futile_cycle", "warning at iter %d: Jaccard=%.2f, %d shared files", iteration, jaccard, len(overlapFiles))

			return fmt.Sprintf(
				"[futile-cycle] Re-reading same files (%s) in a similar pattern. Act on existing knowledge or explore new files.",
				sample,
			)
		}
	}

	return ""
}

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

func futileIntersection(a, b map[string]bool) map[string]bool {
	result := make(map[string]bool)
	for k := range a {
		if b[k] {
			result[k] = true
		}
	}
	return result
}

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
