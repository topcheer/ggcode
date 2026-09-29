package subagent

import (
	"fmt"
	"strings"
)

// failure_attribution.go -- Automated failure attribution for sub-agent runs.
//
// Research basis:
//   - MAST: "Why Do Multi-Agent LLM Systems Fail?" (arXiv:2503.13657, NeurIPS
//     2025). The taxonomy's highest-value diagnostic is not THAT a run failed
//     but which step and which failure class produced the failure; post-hoc
//     analysis of the execution trail recovers this mechanically.
//   - "Which Agent Causes Task Failures and When? On Automated Failure
//     Attribution of LLM Multi-Agent Systems" (Who&When, ICML 2025): frames
//     fine-grained failure attribution (responsible step + timestamp) as an
//     automatable primitive for multi-agent debugging.
//
// The gap this addresses: when a sub-agent terminates with StatusFailed, the
// parent currently sees only a bare error line ("exit status 1", "context
// deadline exceeded"). The full per-run event trail (AgentEvent, capped at
// maxAgentEvents) is recorded but its diagnostic content is never
// summarized: the parent cannot tell a repeated tool-failure loop (retrying
// won't help) from a one-off late error (retry is reasonable) from a
// provider/stream crash with no tool involvement at all. The parent either
// re-spawns an identical doomed run or gives up on feasible work.
//
// The pattern: at the failure boundary, mechanically classify the retained
// event trail into one MAST-flavored category and emit a single compact
// attribution line naming the failing step, the tool, and the error. The
// line is attached to the run's Snapshot so wait_agent / list_agents /
// cascade hints surface it exactly where the parent already consumes
// failure information.
//
// Distinct from:
//   - subagent_cascade.go CascadeHintTracker: attributes model-capability
//     boundaries (failed on an alternate model); that hint fires on bare
//     model-tier signals, this summarizes the run's own event trail.
//   - internal/agent consensus.go: cross-detector co-occurrence on the
//     parent's live loop; this is post-mortem, per sub-agent run.

// Failure attribution categories. Exported for tests and downstream
// classification switches.
const (
	// FailCatToolErrorLoop: the tail of the run is a tight loop of repeated
	// tool errors (>= 3 errors within the last 5 tool results, matching
	// error class). Retrying the same task shape will fail the same way.
	FailCatToolErrorLoop = "tool-error-loop"
	// FailCatToolError: tool errors occurred but are not clustered at the
	// tail (recoverable hiccup; a retry may succeed).
	FailCatToolError = "tool-error"
	// FailCatLLMError: the run died on LLM/stream errors with no tool
	// failures recorded (provider outage, context overflow, stream reset).
	FailCatLLMError = "llm-error"
	// FailCatSilent: the run executed but failed without any error event
	// in the retained trail (watchdog reap, unobservable infra kill).
	FailCatSilent = "silent-failure"
)

// attribution bounds: keep the emitted line compact no matter how noisy the
// trail was.
const (
	attributionMaxArgs = 60
	attributionMaxErr  = 120
	// loopWindow / loopMinErrors: a run whose last window tool results are
	// mostly identical errors is a doomed retry loop, not a transient slip.
	attributionLoopWindow    = 5
	attributionLoopMinErrors = 3
)

// FailureAttribution is the compact post-mortem attached to a failed
// sub-agent run's Snapshot.
type FailureAttribution struct {
	Category       string // one of the FailCat* constants
	ErrorToolCalls int    // tool results with IsError in the retained trail
	TotalToolCalls int    // tool calls in the retained trail
	FirstErrorStep int    // 1-based step of the first error; 0 when none
	Sample         string // most recent error rendering: tool(args): err
	TrailDropped   int    // events evicted from the ring before the failure
}

// attributeFailure classifies a retained event trail. events must be the
// run's event slice in recorded order; dropped counts events evicted from
// the ring before these. Returns nil when the trail carries no signal worth
// attributing (empty trail: lifecycle rejections never ran, the plain Error
// field already tells that story).
func attributeFailure(events []AgentEvent, dropped int) *FailureAttribution {
	var (
		totalCalls int
		errCalls   int
		firstErr   int
		llmErrs    int
		// recent tool-result error flags in call order (bounded scan window
		// is applied over this slice at classification time).
		errFlags   []bool
		lastSample string
	)
	for _, ev := range events {
		switch ev.Type {
		case AgentEventToolCall:
			totalCalls++
		case AgentEventToolResult:
			errFlags = append(errFlags, ev.IsError)
			if ev.IsError {
				errCalls++
				if firstErr == 0 {
					firstErr = totalCalls
				}
				lastSample = renderErrorEvent(ev)
			}
		case AgentEventError:
			llmErrs++
			if lastSample == "" {
				lastSample = renderErrorEvent(ev)
			}
		}
	}
	if totalCalls == 0 && llmErrs == 0 {
		return nil
	}

	attr := &FailureAttribution{
		ErrorToolCalls: errCalls,
		TotalToolCalls: totalCalls,
		FirstErrorStep: firstErr,
		Sample:         lastSample,
		TrailDropped:   dropped,
	}
	switch {
	case errCalls == 0 && llmErrs > 0:
		attr.Category = FailCatLLMError
	case errCalls == 0:
		attr.Category = FailCatSilent
	case tailIsErrorLoop(errFlags):
		attr.Category = FailCatToolErrorLoop
	default:
		attr.Category = FailCatToolError
	}
	return attr
}

// tailIsErrorLoop reports whether the last window tool results are mostly
// errors (>= loopMinErrors of the last loopWindow).
func tailIsErrorLoop(errFlags []bool) bool {
	lookback := len(errFlags)
	if lookback > attributionLoopWindow {
		lookback = attributionLoopWindow
	}
	errs := 0
	for _, isErr := range errFlags[len(errFlags)-lookback:] {
		if isErr {
			errs++
		}
	}
	return errs >= attributionLoopMinErrors
}

// renderErrorEvent renders one error event as a compact "tool(args): err"
// sample line.
func renderErrorEvent(ev AgentEvent) string {
	if ev.Type == AgentEventError {
		msg := firstLine(ev.Text)
		return truncateAttr(msg, attributionMaxErr)
	}
	tool := ev.ToolName
	if tool == "" {
		tool = ev.ToolDisplayName
	}
	var sb strings.Builder
	sb.WriteString(tool)
	if args := firstLine(ev.ToolArgs); args != "" {
		sb.WriteString("(")
		sb.WriteString(truncateAttr(args, attributionMaxArgs))
		sb.WriteString(")")
	}
	if msg := firstLine(ev.Result); msg != "" {
		sb.WriteString(": ")
		sb.WriteString(truncateAttr(msg, attributionMaxErr))
	}
	return sb.String()
}

// String renders the one-line attribution the parent sees on the snapshot.
// Example:
//
//	tool-error-loop at step 12/15: bash(go build ./...): exit status 1
//	(5 errors in last 5 calls, +40 earlier events evicted)
func (a *FailureAttribution) String() string {
	if a == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(a.Category)
	switch {
	case a.TotalToolCalls > 0 && a.FirstErrorStep > 0:
		fmt.Fprintf(&sb, " at step %d/%d", a.FirstErrorStep, a.TotalToolCalls)
	case a.TotalToolCalls > 0:
		fmt.Fprintf(&sb, " across %d tool calls", a.TotalToolCalls)
	}
	if a.Sample != "" {
		sb.WriteString(": ")
		sb.WriteString(a.Sample)
	}
	if a.Category == FailCatToolErrorLoop {
		fmt.Fprintf(&sb, " (%d errors in last %d calls", a.ErrorToolCalls, attributionLoopWindow)
		if a.TrailDropped > 0 {
			fmt.Fprintf(&sb, ", +%d earlier events evicted", a.TrailDropped)
		}
		sb.WriteString(")")
	} else if a.TrailDropped > 0 {
		fmt.Fprintf(&sb, " (+%d earlier events evicted)", a.TrailDropped)
	}
	return sb.String()
}

// firstLine returns the first non-empty line of s.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// truncateAttr bounds s to max runes.
func truncateAttr(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}
