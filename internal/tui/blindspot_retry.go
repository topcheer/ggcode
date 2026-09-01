package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Blind-spot error handling: when a provider/agent error matches none of the
// known categories in provider.UserFacingErrorLang, the user would otherwise
// see only "请求失败，请稍后重试" — undiagnosable and unactionable. Three
// things happen instead:
//
//  1. provider.IsBlindSpotError detects the fallback branch and the raw
//     error is embedded in the user-facing message (user_error.go).
//  2. File logging is force-enabled on first occurrence so the full
//     context survives for diagnosis (debug.EnsureFileLogging).
//  3. The failed submission is auto-retried up to blindSpotMaxRetries
//     times, blindSpotRetryDelay apart — unknown shapes are most often
//     transient (proxy hiccups, malformed gateway responses).

const (
	blindSpotMaxRetries = 5
	blindSpotRetryDelay = 5 * time.Second
)

// maybeBlindSpotRetry is invoked from the agent error handlers after the
// standard failure cleanup, with the original error. When the error is a
// blind spot it enables file logging (idempotent) and schedules an
// auto-retry of the same submission. The returned bool reports whether a
// retry was scheduled; callers merge the returned cmd into theirs.
func (m *Model) maybeBlindSpotRetry(err error) tea.Cmd {
	if !provider.IsBlindSpotError(err) {
		return nil
	}

	wasEnabled, logPath := debug.EnsureFileLogging()

	// Queued submissions took over the input on the error path — retrying
	// here would race the queue drain; let the user drive instead.
	if m.lastUserSubmission == "" || m.pendingSubmissionCount() > 0 {
		m.chatWriteSystem(nextSystemID(), m.blindSpotLogNotice(wasEnabled, logPath))
		m.chatListScrollToBottom()
		return nil
	}

	if m.blindSpotRetries >= blindSpotMaxRetries {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf(
			m.t("error.blindspot_retry_exhausted"),
			m.blindSpotRetries, blindSpotMaxRetries, logPath))
		m.chatListScrollToBottom()
		return nil
	}

	m.blindSpotRetries++
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf(
		m.t("error.blindspot_retry"),
		int(blindSpotRetryDelay.Seconds()), m.blindSpotRetries, blindSpotMaxRetries, logPath))
	m.chatListScrollToBottom()

	text := m.lastUserSubmission
	return tea.Tick(blindSpotRetryDelay, func(time.Time) tea.Msg {
		return blindSpotRetryMsg{Text: text}
	})
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

// handleBlindSpotRetryMsg executes a scheduled auto-retry. If the user
// submitted something else while the timer ran (loading), the retry is
// dropped rather than racing the new run.
func (m *Model) handleBlindSpotRetryMsg(msg blindSpotRetryMsg) tea.Cmd {
	if m.loading {
		return nil
	}
	return m.submitText(msg.Text, true)
}

// resetBlindSpotRetry clears the retry budget on a successful run so the
// next failure gets a full set of attempts again.
func (m *Model) resetBlindSpotRetry() {
	m.blindSpotRetries = 0
}
