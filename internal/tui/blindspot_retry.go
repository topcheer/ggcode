package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Blind-spot error handling: when a provider/agent error matches none of the
// known categories in provider.UserFacingErrorLang, the user would otherwise
// see only "请求失败，请稍后重试" — undiagnosable and unactionable. Two
// things happen instead:
//
//  1. provider.IsBlindSpotError detects the fallback branch and the raw
//     error is embedded in the user-facing message (user_error.go).
//  2. File logging is force-enabled on first occurrence so the full
//     context survives for diagnosis (debug.EnsureFileLogging).
//
// There is deliberately NO automatic resubmission: an earlier version
// auto-retried by re-injecting the user's last submission as a new run,
// and that design had three compounding failure modes (screenshot-reported):
//
//   - Retry budget never burned: the counter was written inside a
//     value-receiver model copy, so the parent never saw it — every retry
//     displayed (1/5) and the loop was literally unbounded (a permanent
//     gateway failure auto-looped forever).
//   - Duplicate user turns: the retry path called submitText which
//     persisted the same user message again, so an agent-side failure
//     polluted the session with N identical user turns.
//   - Dropped attachments: retries replayed only the text; images attached
//     to the original submission were silently lost.
//
// Add on top: errors that slip past the typed HTTP-status extraction
// (e.g. gateway-wrapped 429s) land here UNCLASSIFIED — zhipu-style
// double-meaning 429s (transient limit vs quota exhaustion) were then
// auto-looped pointlessly against a permanently exhausted quota.
//
// The user can still resend manually with /retry (which replays the exact
// same submission through the normal path, attachments included by the
// normal flow).

// maybeBlindSpotRetry is invoked from the agent error handlers after the
// standard failure cleanup, with the original error. For blind-spot errors
// it force-enables file logging (idempotent) and surfaces a /retry hint.
// It never schedules an automatic resubmission (see package comment above).
func (m *Model) maybeBlindSpotRetry(err error) tea.Cmd {
	if !provider.IsBlindSpotError(err) {
		return nil
	}

	wasEnabled, logPath := debug.EnsureFileLogging()
	notice := m.blindSpotLogNotice(wasEnabled, logPath)
	if m.lastUserSubmission != "" {
		notice += " " + m.t("error.blindspot_retry_hint")
	}
	m.chatWriteSystem(nextSystemID(), notice)
	m.chatListScrollToBottom()
	return nil
}

// blindSpotLogNotice builds the no-retry notice (no submission to retry).
func (m *Model) blindSpotLogNotice(wasEnabled bool, logPath string) string {
	if logPath == "" {
		logPath = m.t("error.blindspot_log_failed")
	}
	if wasEnabled {
		return fmt.Sprintf(m.t("error.blindspot_log_only"), logPath)
	}
	return fmt.Sprintf(m.t("error.blindspot_log_enabled"), logPath)
}

// handleBlindSpotRetryMsg is retained as a compile-time no-op anchor: no
// code path produces blindSpotRetryMsg anymore (auto-resubmit removed);
// the dispatch registration below is gone too. Kept function removed.
//
// resetBlindSpotRetry clears the (now inert) counter on a successful run.
func (m *Model) resetBlindSpotRetry() {
	m.blindSpotRetries = 0
}
