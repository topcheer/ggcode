package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
)

// handleRewindCommand implements coordinated rewind (Claude Code checkpoint
// semantics, AgentRewind arXiv:2608.14380): drop the last N user turns from
// the conversation AND revert the file edits those runs made, in lockstep,
// then continue in a fresh forked session that keeps the original intact.
//
// Unlike /branch (conversation-only fork, disk untouched) and /undo-run
// (files-only, conversation untouched), /rewind is the coordinated restore:
// conversation and disk move back together, and a rewind-memory breadcrumb is
// injected so the next run knows what was tried and rolled back instead of
// blindly repeating it.
func (m *Model) handleRewindCommand(parts []string) tea.Cmd {
	if m.loading {
		m.chatWriteSystem(nextSystemID(), m.t("branch.busy"))
		m.chatListScrollToBottom()
		return nil
	}
	if m.session == nil || m.sessionStore == nil {
		m.chatWriteSystem(nextSystemID(), m.t("branch.no_session"))
		m.chatListScrollToBottom()
		return nil
	}

	// Default: rewind the last turn.
	dropRounds := 1
	if len(parts) > 1 {
		n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || n < 1 {
			m.chatWriteSystem(nextSystemID(), m.t("rewind.bad_arg"))
			m.chatListScrollToBottom()
			return nil
		}
		dropRounds = n
	}
	cutoff, ok := session.ComputeBranchCutoff(m.session.Messages, dropRounds)
	if !ok {
		m.chatWriteSystem(nextSystemID(), m.t("branch.back_too_far"))
		m.chatListScrollToBottom()
		return nil
	}

	// Coordinated file rollback FIRST: if it refuses (evicted baselines,
	// split runs), abort before creating anything - a half-rewound session
	// would silently violate the coordinated-restore promise. The user can
	// still /branch to rewind the conversation alone.
	var reverted []checkpoint.Checkpoint
	var revertErr error
	if m.agent != nil {
		if cpMgr := m.agent.CheckpointManager(); cpMgr != nil {
			reverted, revertErr = cpMgr.RevertRecentRuns(dropRounds)
			if revertErr != nil {
				m.chatWriteSystem(nextSystemID(), m.t("rewind.revert_failed", revertErr))
				m.chatListScrollToBottom()
				return nil
			}
		}
	}

	// Flush final metadata for the current session before forking.
	oldSes := m.session
	oldStore := m.sessionStore
	// #3721: snapshot the task board BEFORE spawning the metaFlush
	// goroutine. AppendMetaToDisk json.Marshals oldSes.TasksJSON/
	// TasksEnvJSON on the goroutine; snapshotTasksInto WRITES both on the
	// main thread - the old order raced a slice-header write against the
	// marshal read. Snapshot-then-flush is also the order the persistence
	// helper's own contract documents ("call it before a session's metadata
	// is flushed"), and it makes the goroutine read-only.
	m.snapshotTasksInto(oldSes)
	safego.Go("tui.rewind.metaFlush", func() {
		if jsonlStore, ok := oldStore.(*session.JSONLStore); ok {
			if err := jsonlStore.AppendMetaToDisk(oldSes); err != nil {
				debug.Log("tui", "rewind oldSes meta persist: %v", err)
			}
		}
	})

	// Fork the conversation at the pre-rewind point (same proven session
	// continuation path as /branch; cannot copy Session by value because it
	// contains a sync.RWMutex).
	rewound := session.NewSession(oldSes.Vendor, oldSes.Endpoint, oldSes.Model)
	rewound.Workspace = oldSes.Workspace
	rewound.TokenUsage = oldSes.TokenUsage
	rewound.CostJSON = append([]byte(nil), oldSes.CostJSON...)
	rewound.PermissionMode = oldSes.PermissionMode
	rewound.TasksJSON = append([]byte(nil), oldSes.TasksJSON...)
	if oldSes.SidebarVisible != nil {
		val := *oldSes.SidebarVisible
		rewound.SidebarVisible = &val
	}
	rewound.Messages = make([]provider.Message, cutoff)
	copy(rewound.Messages, oldSes.Messages[:cutoff])
	rewound.ParentSessionID = oldSes.ID
	rewound.ForkPoint = cutoff
	if len(oldSes.UsageHistory) > 0 {
		rewound.UsageHistory = make([]session.UsageEntry, len(oldSes.UsageHistory))
		copy(rewound.UsageHistory, oldSes.UsageHistory)
	}
	if len(oldSes.Metrics) > 0 {
		rewound.Metrics = make([]metrics.MetricEvent, len(oldSes.Metrics))
		copy(rewound.Metrics, oldSes.Metrics)
	}
	rewound.EndpointUsage = make(map[string]provider.TokenUsage, len(oldSes.EndpointUsage))
	for k, v := range oldSes.EndpointUsage {
		rewound.EndpointUsage[k] = v
	}
	rewound.EndpointMetrics = make(map[string][]metrics.MetricEvent, len(oldSes.EndpointMetrics))
	for k, v := range oldSes.EndpointMetrics {
		cp := make([]metrics.MetricEvent, len(v))
		copy(cp, v)
		rewound.EndpointMetrics[k] = cp
	}

	// Rewind memory: one bounded user-role breadcrumb so the continued
	// session knows what was tried and rolled back (AgentRewind's "continue
	// with information from previous attempts") instead of repeating it.
	rewound.Messages = append(rewound.Messages, provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: buildRewindMemory(reverted, dropRounds),
		}},
	})

	origTitle := oldSes.Title
	if origTitle == "" {
		origTitle = oldSes.ID
	}
	rewound.Title = fmt.Sprintf("Rewind (-%d turns): %s", dropRounds, origTitle)

	if jsonlStore, ok := m.sessionStore.(*session.JSONLStore); ok {
		if err := jsonlStore.Save(rewound); err != nil {
			m.chatWriteSystem(nextSystemID(), m.t("branch.save_failed", err))
			m.chatListScrollToBottom()
			return nil
		}
		if err := jsonlStore.AppendMetaToDisk(rewound); err != nil {
			m.chatWriteSystem(nextSystemID(), m.t("branch.save_failed", err))
			m.chatListScrollToBottom()
			return nil
		}
		if err := jsonlStore.AppendMessagesBatchToDisk(rewound, rewound.Messages); err != nil {
			m.chatWriteSystem(nextSystemID(), m.t("branch.save_failed", err))
			m.chatListScrollToBottom()
			return nil
		}
	} else {
		if err := m.sessionStore.Save(rewound); err != nil {
			m.chatWriteSystem(nextSystemID(), m.t("branch.save_failed", err))
			m.chatListScrollToBottom()
			return nil
		}
	}

	// Switch to the rewound session.
	m.applyResumedSession(rewound)

	files := rewindFileList(reverted)
	if len(files) == 0 {
		m.chatWriteSystem(nextSystemID(), m.t("rewind.success_no_edits", dropRounds, rewound.ID))
	} else {
		m.chatWriteSystem(nextSystemID(), m.t("rewind.success", dropRounds, strings.Join(files, ", "), rewound.ID))
	}
	m.chatListScrollToBottom()
	return nil
}

// buildRewindMemory renders the injected breadcrumb message. Bounded: at
// most rewindMemoryMaxFiles files listed, so a large multi-file rewind
// cannot blow the breadcrumb into a second screen of context.
func buildRewindMemory(reverted []checkpoint.Checkpoint, turns int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[rewind memory] The last %d turn(s) of this conversation were rewound and their file edits were reverted. Treat everything below as tried-and-rolled-back, not as current state - do not repeat the same approaches blindly.\n", turns)
	files := rewindFileList(reverted)
	if len(files) == 0 {
		b.WriteString("The rewound turns made no tracked file edits.\n")
	} else {
		fmt.Fprintf(&b, "Reverted files (%d): %s\n", len(files), strings.Join(files, ", "))
	}
	b.WriteString("Continue from the pre-rewind conversation state above.\n")
	return b.String()
}

const rewindMemoryMaxFiles = 10

// rewindFileList dedupes reverted checkpoints by file path (a file edited
// several times in the rewound runs yields one entry) and returns a stable
// base-name-qualified list, capped at rewindMemoryMaxFiles entries with a
// "+k more" tail.
func rewindFileList(reverted []checkpoint.Checkpoint) []string {
	seen := make(map[string]struct{}, len(reverted))
	var files []string
	for _, cp := range reverted {
		if cp.FilePath == "" {
			continue
		}
		if _, dup := seen[cp.FilePath]; dup {
			continue
		}
		seen[cp.FilePath] = struct{}{}
		files = append(files, filepath.Base(cp.FilePath))
	}
	sort.Strings(files)
	if len(files) > rewindMemoryMaxFiles {
		extra := len(files) - rewindMemoryMaxFiles
		files = append(files[:rewindMemoryMaxFiles], fmt.Sprintf("+%d more", extra))
	}
	return files
}
