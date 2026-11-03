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

	oldSes := m.session
	oldStore := m.sessionStore

	m.snapshotTasksInto(oldSes)
	safego.Go("tui.rewind.metaFlush", func() {
		if jsonlStore, ok := oldStore.(*session.JSONLStore); ok {
			if err := jsonlStore.AppendMetaToDisk(oldSes); err != nil {
				debug.Log("tui", "rewind oldSes meta persist: %v", err)
			}
		}
	})

	rewound := session.NewSession(oldSes.Vendor, oldSes.Endpoint, oldSes.Model)
	rewound.Workspace = oldSes.Workspace
	rewound.TokenUsage = oldSes.TokenUsage
	rewound.CostJSON = append([]byte(nil), oldSes.CostJSON...)
	rewound.PermissionMode = oldSes.PermissionMode
	rewound.TasksJSON = append([]byte(nil), oldSes.TasksJSON...)

	rewound.TasksEnvJSON = append([]byte(nil), oldSes.TasksEnvJSON...)
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
