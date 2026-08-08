package agent

// Foresight Calibration Tracker (WorldEvolver-inspired)
//
// Research basis:
//   - WorldEvolver (arXiv:2606.30639, Jun 2026): "Self-Evolving World Models
//     for LLM Agent Planning." World models give agents foresight -- predictions
//     of action consequences before execution. However, unreliable foresight
//     can be ignored, misused, or degrade decision-making. The key insight:
//     prediction-observation mismatches are the strongest learning signal.
//   - WorldEvolver's Semantic Memory module extracts persistent heuristic rules
//     from prediction-observation mismatches, improving calibration over time.
//   - Selective Foresight module filters low-confidence predictions before
//     injecting them into agent reasoning context.
//
// Problem: AI coding agents frequently make explicit predictions about what
// a tool call will return, then receive results that contradict those
// predictions -- but no detector tracks this calibration gap:
//
//  1. "This file should contain the database config" → file is empty/missing
//  2. "The test will pass after my fix" → test fails
//  3. "This function returns a string" → returns an int
//  4. "The build should succeed now" → build errors
//  5. "I expect the API to return JSON" → returns HTML error page
//
// Each mismatch reveals the agent's world model is wrong. Repeated mismatches
// in a single run indicate the agent is operating with a fundamentally
// incorrect mental model of the codebase/API/system -- a high-risk situation
// where subsequent actions are likely based on flawed assumptions.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - outcome_misattribution.go: checks success claims vs failure indicators
//     in results (narrative vs result), not prediction vs observation.
//   - tool_target_mismatch.go: checks stated intent vs actual tool target
//     (text vs tool call), not prediction vs result content.
//   - tool_claim_verify.go: checks if agent misread existing tool output,
//     not predictions made BEFORE seeing the output.
//   - error_strategy_loop.go: checks repeated error patterns, not
//     prediction-observation gaps.
//
// Gap: No detector captures the agent's explicit predictions about tool
// outcomes and compares them against actual results. This is the core
// "prediction-observation mismatch" signal from WorldEvolver that identifies
// when the agent's world model is miscalibrated.
//
// Design:
//   - Phase 1 (recordPrediction): scans assistant text BEFORE tool execution
//     for prediction language ("should", "expect", "will", "probably returns").
//     Extracts the predicted outcome category (success/failure/content type).
//   - Phase 2 (checkCalibration): after tool execution, compares the predicted
//     category against the actual result. Mismatches accumulate.
//   - Threshold: 3+ mismatches in a single run → inject calibration guidance
//   - Zero LLM cost -- pure deterministic text + result pattern matching
//   - Fires at most 2 times per run (advisory, non-blocking)

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// ---------------------------------------------------------------------------
// Prediction patterns -- phrases where the agent commits to a specific outcome
// before seeing the result. These are the agent's "world model" in action.
// ---------------------------------------------------------------------------

// predictionRe matches sentences containing forward-looking predictions about
// tool outcomes. Captures the prediction context for later comparison.
var foresightPredictionRe = regexp.MustCompile(`(?i)(?:this\s+(?:should|will|should)\s+(?:contain|return|show|reveal|have|include)|the\s+(?:test|build|command|file|function|api|response)\s+(?:should|will|must)\s+(?:pass|fail|succeed|work|return|contain|show|be)|i\s+expect\s+(?:this|the|it)\s+to|this\s+will\s+(?:likely\s+)?(?:pass|fail|work|succeed|return|contain)|the\s+output\s+(?:should|will)\s+be|should\s+(?:now\s+)?(?:pass|work|succeed|compile|build)|probably\s+(?:returns?|contains?|is)|likely\s+(?:returns?|contains?|is|will))`)

// successPredictionRe matches predictions that the outcome will be positive
// (pass, succeed, work, return expected content).
var foresightSuccessPredRe = regexp.MustCompile(`(?i)(?:should|will|must|expect)\s+(?:it\s+to\s+)?(?:pass|succeed|work|compile|build\s+successfully|return\s+(?:the\s+)?(?:expected\s+)?(?:value|result|data|content|json)|contain\s+(?:the\s+)?(?:config|definition|function|key|field)|be\s+(?:present|found|there|valid|correct|empty))|will\s+(?:likely\s+)?(?:pass|succeed|work)`)

// failurePredictionRe matches predictions that the outcome will be negative
// (fail, error, not found). These are rarer but important -- when an agent
// predicts failure and gets success, it may be surprised/confused.
var foresightFailurePredRe = regexp.MustCompile(`(?i)(?:should|will|might|may|expect)\s+(?:it\s+to\s+)?(?:fail|error|not\s+(?:work|exist|be\s+found|compile|pass)|throw|crash|return\s+(?:an?\s+)?error|be\s+(?:missing|absent|null|undefined))`)

// resultFailureRe detects failure indicators in tool results.
var foresightResultFailureRe = regexp.MustCompile(`(?i)(?:error|failed|failure|panic|fatal|exception|not\s+found|no\s+such\s+(?:file|directory)|cannot|undefined|null\s+pointer|traceback|exit\s+code\s+[1-9]|status[:\s]+(?:4\d\d|5\d\d)|compilation\s+failed)`)

// resultEmptyRe detects empty or minimal results (prediction said content exists).
var foresightResultEmptyRe = regexp.MustCompile(`(?i)(?:^$|no\s+(?:results?|matches?|content|output|data\s+found)|empty|0\s+(?:bytes|lines|matches|results)|nothing\s+(?:found|returned|matched))`)

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

type foresightPrediction struct {
	iteration   int
	predictedOK bool   // true = predicted success, false = predicted failure
	snippet     string // prediction text for reporting
	toolName    string // tool that was about to be called
}

type foresightCalibrateState struct {
	predictions []foresightPrediction // pending predictions awaiting results
	mismatches  int                   // total prediction-observation mismatches
	warnCount   int                   // how many times we've injected guidance
}

func newForesightCalibrateState() *foresightCalibrateState {
	return &foresightCalibrateState{}
}

// ---------------------------------------------------------------------------
// Phase 1: recordPrediction -- called BEFORE tool execution
// Scans assistant text for predictions about the upcoming tool's outcome.
// ---------------------------------------------------------------------------

// recordPrediction scans assistant text for outcome predictions and stores
// them as pending predictions awaiting verification. Called before tool calls
// are executed.
func (s *foresightCalibrateState) recordPrediction(assistantText string, toolCalls []provider.ToolCallDelta, iteration int) {
	if s == nil || len(toolCalls) == 0 {
		return
	}

	// Only scan text near tool calls (the prediction context). Use the full
	// text -- predictions can appear anywhere but are usually near the call.
	if !foresightPredictionRe.MatchString(assistantText) {
		return
	}

	// Determine prediction polarity for each tool call in this turn.
	for _, tc := range toolCalls {
		var predictedOK bool
		hasSuccess := foresightSuccessPredRe.MatchString(assistantText)
		hasFailure := foresightFailurePredRe.MatchString(assistantText)

		if hasSuccess && !hasFailure {
			predictedOK = true
		} else if hasFailure && !hasSuccess {
			predictedOK = false
		} else {
			// Ambiguous or both -- skip this tool call.
			continue
		}

		// Extract a short snippet around the first prediction match.
		snippet := extractForesightSnippet(assistantText)

		s.predictions = append(s.predictions, foresightPrediction{
			iteration:   iteration,
			predictedOK: predictedOK,
			snippet:     snippet,
			toolName:    tc.Name,
		})
	}

	// Cap pending predictions to avoid unbounded growth.
	if len(s.predictions) > 30 {
		s.predictions = s.predictions[len(s.predictions)-30:]
	}
}

// extractForesightSnippet returns the first prediction-relevant sentence,
// truncated for reporting.
func extractForesightSnippet(text string) string {
	loc := foresightPredictionRe.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	// Find sentence boundary around the match.
	start := loc[0]
	end := loc[1]
	// Extend backwards to sentence start.
	for start > 0 && text[start-1] != '.' && text[start-1] != '\n' && start > loc[0]-80 {
		start--
	}
	// Extend forward to sentence end.
	for end < len(text) && text[end] != '.' && text[end] != '\n' && end < loc[1]+80 {
		end++
	}
	snippet := strings.TrimSpace(text[start:end])
	if len(snippet) > 120 {
		snippet = snippet[:117] + "..."
	}
	return snippet
}

// ---------------------------------------------------------------------------
// Phase 2: checkCalibration -- called AFTER tool execution
// Compares predicted outcome against actual result. Returns guidance if
// mismatch threshold exceeded.
// ---------------------------------------------------------------------------

// checkCalibration compares pending predictions against actual tool results.
// Called after tool execution. Returns guidance message if threshold exceeded.
func (s *foresightCalibrateState) checkCalibration(toolName string, resultContent string, isError bool, iteration int) string {
	if s == nil || len(s.predictions) == 0 {
		return ""
	}

	// Match predictions to this tool call. We consume the oldest matching
	// prediction per tool call.
	matched := -1
	for i, p := range s.predictions {
		if p.toolName == toolName || (p.toolName == "" && toolName != "") {
			matched = i
			break
		}
	}
	if matched < 0 {
		return ""
	}

	pred := s.predictions[matched]
	// Remove the matched prediction.
	s.predictions = append(s.predictions[:matched], s.predictions[matched+1:]...)

	// Determine actual outcome polarity.
	actualOK := !isError && !foresightResultFailureRe.MatchString(resultContent)
	actualEmpty := foresightResultEmptyRe.MatchString(resultContent)

	// Check for mismatch.
	mismatch := false
	mismatchDetail := ""

	if pred.predictedOK && !actualOK {
		// Predicted success, got failure.
		mismatch = true
		mismatchDetail = "predicted success but the result indicates failure"
	} else if pred.predictedOK && actualEmpty {
		// Predicted content/existence, got empty.
		mismatch = true
		mismatchDetail = "predicted content but the result is empty or not found"
	} else if !pred.predictedOK && actualOK && !actualEmpty {
		// Predicted failure, got success (rare but indicates miscalibration).
		mismatch = true
		mismatchDetail = "predicted failure but the result succeeded"
	}

	if mismatch {
		s.mismatches++
		debug.Log("foresight-calibrate", "Iteration %d: prediction-observation mismatch #%d (%s)", iteration, s.mismatches, mismatchDetail)
	}

	// Inject guidance when threshold is reached.
	if s.mismatches >= 3 && s.warnCount < 2 {
		s.warnCount++
		return fmt.Sprintf(
			"[Foresight Calibration] Your predictions about tool outcomes have been wrong %d times in this run "+
				"(e.g., \"%s\" -- %s). This suggests your mental model of the codebase/API/system may be incorrect. "+
				"Before your next action, verify your assumptions by reading the actual state rather than predicting it. "+
				"Stop assuming -- start observing.",
			s.mismatches, pred.snippet, mismatchDetail,
		)
	}

	return ""
}

// reset clears state for a new user turn.
func (s *foresightCalibrateState) reset() {
	if s == nil {
		return
	}
	s.predictions = nil
}
