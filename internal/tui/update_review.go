package tui

// #2422 knife E (review/commit ready): extracted verbatim from
// Model.Update's inline case bodies - zero behavior change. The two bodies
// are near-duplicates kept separate deliberately - dedupe is a behavioral
// review away, not part of this mechanical pass.

import (
	"bytes"
	"time"

	tea "charm.land/bubbletea/v2"
)

func (m Model) handleReviewReadyMsg(msg reviewReadyMsg) (tea.Model, tea.Cmd) {
	// #1744 case 3: the async git subprocess takes seconds - an agent run
	// starting (or a /commit while busy, which is whitelisted) in that
	// window made this handler startAgent CONCURRENTLY, overwriting
	// cancelFunc. Queue behind the run instead.
	if m.loading {
		m.queuePendingSubmission("/review")
		return m, nil
	}
	// The /review command prepared the full prompt text; start the agent with it.
	m.chatWriteUser(nextChatID(), "/review")
	m.appendUserMessage("/review")
	m.streamBuffer = &bytes.Buffer{}
	m.streamPrefixWritten = false
	m.setLoading(true)
	m.loopStart = time.Now()
	m.statusActivity = m.t("status.thinking")
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	return m, m.startAgent(msg.text)
}

func (m Model) handleCommitReadyMsg(msg commitReadyMsg) (tea.Model, tea.Cmd) {
	// #1744 case 3: same async-window guard as reviewReadyMsg - /commit
	// is whitelisted to RUN while busy, so its ready message can land
	// mid-run and must queue, not start concurrently.
	if m.loading {
		m.queuePendingSubmission("/commit")
		return m, nil
	}
	// The /commit command prepared the full prompt text; start the agent with it.
	m.chatWriteUser(nextChatID(), "/commit")
	m.appendUserMessage("/commit")
	m.streamBuffer = &bytes.Buffer{}
	m.streamPrefixWritten = false
	m.setLoading(true)
	m.loopStart = time.Now()
	m.statusActivity = m.t("status.thinking")
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	return m, m.startAgent(msg.text)
}
