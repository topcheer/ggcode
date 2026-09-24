package agent

// Meltdown Onset Detector - Sliding-Window Tool-Sequence Entropy Monitor
//
// Research basis:
//   - "Beyond pass@1: A Reliability Science Framework for Long-Horizon LLM
//     Agents" (arXiv:2603.29231, 2026): behavioral collapse ("meltdown onset",
//     MOP) is detected through sliding-window entropy over tool-call
//     sequences. When an agent's invocation pattern becomes high-entropy
//     (erratic, non-repeating, unpredictable), it has entered meltdown.
//     Frontier models exhibit the highest meltdown rates (~19% at very-long
//     horizons) because aggressive multi-step strategies generate entropy
//     spikes when exploration spirals. Early intervention at the MOP
//     recovers a meaningful share of otherwise-failed long-horizon runs.
//   - "Silent Failure in LLM Agent Systems: The Entropy Principle"
//     (arXiv:2606.08162, 2026): erratic behavioral signatures precede
//     quality degradation that no individual tool error exposes.
//
// Problem: every existing behavioral detector in this package is ANCHORED:
//   - error-anchored: strategyExhaustion / errStrategyLoop / compounding
//     failure / recurringError require a repeating error fingerprint or
//     elevated failure rate,
//   - path-anchored: attentionFragment tracks directory switches,
//     solutionFixation tracks failed edits per file,
//   - edit-anchored: editOscillation / diminishingEdit track file contents
//     and edit sizes.
// None of them see the meltdown signature: a sustained, error-free stretch
// where the agent hops erratically across many unrelated tools without
// converging on anything. Each individual call "succeeds", so failure-rate
// detectors stay silent while the trajectory as a whole has lost structure.
//
// This detector fills that gap: maintain a sliding window of the last W tool
// names and compute Shannon entropy over the name distribution. Sustained
// entropy above threshold (with a minimum distinct-tool count to avoid
// firing on small uniform samples) indicates meltdown onset; inject bounded
// guidance suggesting pause-and-replan, checkpoint restore, or compaction
// re-grounding.
//
// Anti-false-positive design (deterministic, zero LLM cost):
//   - Persistence: the condition must hold on consecutive full-window
//     evaluations (a single researchy exploration burst does not fire).
//   - Cooldown: after firing, at least cooldownCalls further tool calls must
//     pass before the condition can fire again.
//   - Finite alerts: at most maxWarnings guidance messages per run.
//
// Interaction with existing detectors: strategyExhaustion covers "diverse
// recovery strategies failing for the SAME error" (error-anchored diversity);
// this detector covers "erratic diversity with no error anchor at all". The
// EEA framing quoted in strategy_exhaustion.go ("persistently HIGH entropy
// with no convergence = flailing") is implemented HERE.

import (
	"fmt"
	"math"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// moWindow: sliding-window length in tool calls. 16 calls is long
	// enough to average out batch parallel calls, short enough to react
	// within one work stretch.
	moWindow = 16

	// moEntropyThreshold: Shannon entropy (bits) over the tool-name
	// distribution in the window above which the pattern is considered
	// erratic. Reference points for W=16: a tight read/edit/verify loop
	// over 3 tools yields ~1.5 bits; a healthy 5-tool research pattern
	// yields ~2.3 bits; uniform use of 8+ distinct tools approaches and
	// exceeds 3.0 bits. 2.9 bits means an effective diversity of ~7.5
	// distinct tools in 16 calls - structure has collapsed.
	moEntropyThreshold = 2.9

	// moMinDistinct: minimum distinct tool names in the window before the
	// entropy figure is trusted at all (small samples are noisy).
	moMinDistinct = 6

	// moConsecutiveHits: the condition must hold this many consecutive
	// full-window evaluations before firing (persistence gate).
	moConsecutiveHits = 2

	// moCooldownCalls: tool calls required between two firings.
	moCooldownCalls = 12

	// moMaxWarnings: cap guidance injections per run to avoid noise.
	moMaxWarnings = 2
)

// meltdownOnsetState tracks the recent tool-call name sequence and fires
// bounded guidance when sustained high entropy indicates meltdown onset.
type meltdownOnsetState struct {
	mu sync.Mutex

	// window holds the last moWindow tool names.
	window []string

	// consecutiveHits counts consecutive full-window evaluations that
	// exceeded the entropy threshold (persistence gate).
	consecutiveHits int

	// callsSinceAlert counts tool calls since the last guidance injection
	// (cooldown gate).
	callsSinceAlert int

	// warningCount caps warnings per run.
	warningCount int
}

func newMeltdownOnsetState() *meltdownOnsetState {
	return &meltdownOnsetState{
		window: make([]string, 0, moWindow),
	}
}

func (s *meltdownOnsetState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.window = s.window[:0]
	s.consecutiveHits = 0
	s.callsSinceAlert = 0
	s.warningCount = 0
}

// recordToolCall records a completed tool call and evaluates the sliding
// window. Returns a guidance message when meltdown onset is detected, empty
// string otherwise.
func (s *meltdownOnsetState) recordToolCall(toolName string, iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.callsSinceAlert++

	s.window = append(s.window, toolName)
	if len(s.window) > moWindow {
		s.window = s.window[len(s.window)-moWindow:]
	}
	if len(s.window) < moWindow {
		return "" // not enough samples yet
	}

	bits, distinct := moEntropy(s.window)
	if bits >= moEntropyThreshold && distinct >= moMinDistinct {
		s.consecutiveHits++
	} else {
		// Decay instead of hard reset: one structured call inside an
		// otherwise erratic stretch should not fully disarm the gate.
		s.consecutiveHits /= 2
	}
	if s.consecutiveHits < moConsecutiveHits {
		return ""
	}
	if s.warningCount >= moMaxWarnings || s.callsSinceAlert < moCooldownCalls {
		return ""
	}

	s.consecutiveHits = 0
	s.callsSinceAlert = 0
	s.warningCount++
	debug.Log("agent", "Iteration %d: meltdown onset detector triggered (entropy %.2f bits, %d distinct tools)",
		iteration, bits, distinct)
	return moBuildWarning(bits, distinct)
}

// moEntropy computes Shannon entropy (bits) over the tool-name distribution
// of the window, along with the number of distinct names.
func moEntropy(window []string) (bits float64, distinct int) {
	if len(window) == 0 {
		return 0, 0
	}
	counts := make(map[string]int, len(window))
	for _, name := range window {
		counts[name]++
	}
	distinct = len(counts)
	n := float64(len(window))
	for _, c := range counts {
		p := float64(c) / n
		bits -= p * math.Log2(p)
	}
	return bits, distinct
}

// moBuildWarning constructs the guidance message.
func moBuildWarning(bits float64, distinct int) string {
	return fmt.Sprintf(
		"[Meltdown Onset] Entropy over your last %d tool calls is %.2f bits "+
			"across %d distinct tools - erratic, non-converging tool-switching "+
			"is the behavioral signature of meltdown onset, even when every "+
			"individual call succeeds (arXiv:2603.29231 sliding-window entropy; "+
			"early intervention recovers a large share of failing long-horizon "+
			"runs). Before the next tool call: (1) stop and restate the single "+
			"next concrete step from your plan, then do exactly that step; "+
			"(2) if you are lost, restore the last checkpoint or compact and "+
			"re-ground on the original user goal; (3) batch related "+
			"reads/searches into one pass instead of hopping between tools.",
		moWindow, bits, distinct)
}
