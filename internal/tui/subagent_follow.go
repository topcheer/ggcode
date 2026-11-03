package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/util"
)

type followSlotKind int

const (
	followSlotSubAgent followSlotKind = iota
	followSlotTeammate
)

type followSlot struct {
	ID       string
	Name     string
	Model    string
	Kind     followSlotKind
	Phase    string
	Terminal bool
}

type followViewEntry struct {
	list            *chat.List
	dropped         int
	eventsProcessed int
	status          string
	task            string
}

const stripRefreshInterval = 2 * time.Second

type subAgentFollowState struct {
	activeID         string
	slots            []followSlot
	views            map[string]*followViewEntry
	dirty            map[string]bool
	lastRebuild      map[string]time.Time
	lastStripRefresh time.Time
	stripDirty       bool
}

const subAgentGracePeriod = 1 * time.Minute

func (f *subAgentFollowState) refreshSlots(saMgr *subagent.Manager) {
	var newSlots []followSlot

	if saMgr != nil {
		statuses := saMgr.Statuses()
		slices.SortFunc(statuses, func(a, b subagent.StatusInfo) int {
			return cmp.Compare(a.ID, b.ID)
		})
		now := time.Now()
		for _, s := range statuses {
			isTerminal := s.Status == subagent.StatusCompleted || s.Status == subagent.StatusFailed || s.Status == subagent.StatusCancelled
			if isTerminal {

				if !s.EndedAt.IsZero() && now.Sub(s.EndedAt) < subAgentGracePeriod {
					newSlots = append(newSlots, followSlot{
						ID:       s.ID,
						Name:     s.Name,
						Model:    s.Model,
						Kind:     followSlotSubAgent,
						Phase:    "done",
						Terminal: true,
					})
				}
				continue
			}
			newSlots = append(newSlots, followSlot{
				ID:       s.ID,
				Name:     s.Name,
				Model:    s.Model,
				Kind:     followSlotSubAgent,
				Phase:    s.CurrentPhase,
				Terminal: false,
			})
		}
	}

	f.slots = newSlots
}

func (f *subAgentFollowState) refreshSwarmSlots(swMgr *swarm.Manager) {
	if swMgr == nil {
		return
	}

	var subOnly []followSlot
	for _, s := range f.slots {
		if s.Kind == followSlotSubAgent {
			subOnly = append(subOnly, s)
		}
	}
	f.slots = subOnly

	for _, ts := range swMgr.ListTeamStatuses() {
		for _, tm := range ts.Teammates {
			f.slots = append(f.slots, followSlot{
				ID:       tm.ID,
				Name:     tm.Name,
				Kind:     followSlotTeammate,
				Phase:    string(tm.Status),
				Terminal: tm.Status == swarm.TeammateIdle || tm.Status == swarm.TeammateShuttingDown,
			})
		}
	}

	slices.SortFunc(f.slots, func(a, b followSlot) int {
		return cmp.Compare(a.ID, b.ID)
	})
}

func (f *subAgentFollowState) markDirty(agentID string) {
	if f.dirty == nil {
		f.dirty = make(map[string]bool)
	}
	f.dirty[agentID] = true
}

func (f *subAgentFollowState) markStripDirty() {
	f.stripDirty = true
}

func (f *subAgentFollowState) refreshStripIfNeeded(saMgr *subagent.Manager, swMgr *swarm.Manager) bool {
	if !f.stripDirty {
		return false
	}
	now := time.Now()
	if !f.lastStripRefresh.IsZero() && now.Sub(f.lastStripRefresh) < stripRefreshInterval {
		return false
	}
	f.refreshSlots(saMgr)
	f.refreshSwarmSlots(swMgr)
	f.lastStripRefresh = now
	f.stripDirty = false
	return true
}

func (f *subAgentFollowState) isActive() bool {
	return f.activeID != ""
}

func (f *subAgentFollowState) activate(index int) {
	if index < 0 || index >= len(f.slots) {
		return
	}
	f.activeID = f.slots[index].ID
}

func (f *subAgentFollowState) deactivate() string {
	prev := f.activeID
	f.activeID = ""
	return prev
}

func (f *subAgentFollowState) currentSlotIndex() int {
	if !f.isActive() {
		return -1
	}
	for i, slot := range f.slots {
		if slot.ID == f.activeID {
			return i
		}
	}
	return -1
}

func (f *subAgentFollowState) getOrCreateView(agentID string, width, height int) *followViewEntry {
	if f.views == nil {
		f.views = make(map[string]*followViewEntry)
	}
	entry, ok := f.views[agentID]
	if !ok {
		entry = &followViewEntry{
			list: chat.NewList(width, height),
		}
		f.views[agentID] = entry
	}
	return entry
}

type followEventData struct {
	ID            string
	Name          string
	Model         string
	Task          string
	Status        string
	Events        []followEvent
	EventsDropped int
}

type followEvent struct {
	Type            int
	Text            string
	ToolID          string
	ToolName        string
	ToolArgs        string
	ToolDisplayName string
	ToolDetail      string
	Result          string
	IsError         bool
}

const (
	followEventText       = 0
	followEventToolCall   = 1
	followEventToolResult = 2
	followEventError      = 3
)

func buildFollowList(data followEventData, list *chat.List, styles chat.Styles) {
	list.SetItems(nil)

	name := data.Name
	if name == "" {
		name = shortID(data.ID)
	}
	if data.Model != "" {
		name = name + " [" + data.Model + "]"
	}
	header := fmt.Sprintf("%s  ·  %s", name, data.Task)
	switch data.Status {
	case "completed":
		header += "  ✓ completed"
	case "failed":
		header += "  ✗ failed"
	case "running", "working":
		header += "  ● running"
	}
	list.Append(chat.NewSystemItem("header", header, styles))

	if data.EventsDropped > 0 {
		list.Append(chat.NewSystemItem("trunc",
			fmt.Sprintf("⚠ Early output truncated (%d events dropped)", data.EventsDropped),
			styles,
		))
	}

	flushText := func(buf *strings.Builder) {
		text := buf.String()
		buf.Reset()
		if text == "" {
			return
		}
		ai := chat.NewAssistantItem("", styles)
		ai.SetText(text)

		ai.SetFinished()
		list.Append(ai)
	}

	toolCalls := make(map[string]int)
	toolCallCount := make(map[string]int)
	toolResultCount := make(map[string]int)

	pendingByName := make(map[string][]string)
	var textBuf strings.Builder

	for _, ev := range data.Events {
		switch ev.Type {
		case followEventText:
			textBuf.WriteString(ev.Text)

		case followEventToolCall:
			flushText(&textBuf)
			toolCallCount[ev.ToolName]++
			key := ev.ToolID
			if key == "" {
				key = fmt.Sprintf("%s-%d", ev.ToolName, toolCallCount[ev.ToolName])
			}
			toolCalls[key] = list.Len()
			if ev.ToolID == "" {
				pendingByName[ev.ToolName] = append(pendingByName[ev.ToolName], key)
			}
			present := describeTool("en", ev.ToolName, ev.ToolArgs)
			if ev.ToolDisplayName != "" {
				present.DisplayName = ev.ToolDisplayName
			}
			if ev.ToolDetail != "" {
				present.Detail = ev.ToolDetail
			}
			item := chat.NewToolItem(
				key,
				chat.ToolContext{
					ToolName:    ev.ToolName,
					DisplayName: present.DisplayName,
					Detail:      present.Detail,
					RawArgs:     ev.ToolArgs,
					Lang:        "en",
				},
				chat.StatusRunning,
				styles,
			)
			list.Append(item)

		case followEventToolResult:
			flushText(&textBuf)
			status := chat.StatusSuccess
			if ev.IsError {
				status = chat.StatusError
			}
			key := ev.ToolID
			if key == "" {

				if q := pendingByName[ev.ToolName]; len(q) > 0 {
					key = q[0]
					pendingByName[ev.ToolName] = q[1:]
				} else {
					toolResultCount[ev.ToolName]++
					key = fmt.Sprintf("%s-%d", ev.ToolName, toolResultCount[ev.ToolName])
				}
			}
			if idx, ok := toolCalls[key]; ok {
				existing := list.ItemAt(idx)
				if setter, ok := existing.(interface{ SetResult(string, bool) }); ok {
					setter.SetResult(ev.Result, ev.IsError)
				}
				if statusSetter, ok := existing.(interface{ SetStatus(chat.ToolStatus) }); ok {
					statusSetter.SetStatus(status)
				}
			} else {
				item := chat.NewGenericToolItem("result", ev.ToolName, status, util.Truncate(ev.Result, 200), styles)
				list.Append(item)
			}

		case followEventError:
			flushText(&textBuf)
			list.Append(chat.NewSystemItem("error", "Error: "+ev.Text, styles))
		}
	}
	flushText(&textBuf)
}

func subagentSnapshotToFollowData(snap subagent.Snapshot) followEventData {
	events := make([]followEvent, len(snap.Events))
	for i, ev := range snap.Events {
		var t int
		switch ev.Type {
		case subagent.AgentEventText:
			t = followEventText
		case subagent.AgentEventToolCall:
			t = followEventToolCall
		case subagent.AgentEventToolResult:
			t = followEventToolResult
		case subagent.AgentEventError:
			t = followEventError
		}
		events[i] = followEvent{
			Type:            t,
			Text:            ev.Text,
			ToolID:          ev.ToolID,
			ToolName:        ev.ToolName,
			ToolArgs:        ev.ToolArgs,
			ToolDisplayName: ev.ToolDisplayName,
			ToolDetail:      ev.ToolDetail,
			Result:          ev.Result,
			IsError:         ev.IsError,
		}
	}
	status := string(snap.Status)
	return followEventData{
		ID:            snap.ID,
		Name:          snap.Name,
		Model:         snap.Model,
		Task:          snap.DisplayTask,
		Status:        status,
		Events:        events,
		EventsDropped: snap.EventsDropped,
	}
}

func teammateSnapshotToFollowData(snap swarm.TeammateSnapshot) followEventData {
	events := make([]followEvent, len(snap.Events))
	for i, ev := range snap.Events {
		var t int
		switch ev.Type {
		case swarm.TeammateEventText:
			t = followEventText
		case swarm.TeammateEventToolCall:
			t = followEventToolCall
		case swarm.TeammateEventToolResult:
			t = followEventToolResult
		case swarm.TeammateEventError:
			t = followEventError
		}
		events[i] = followEvent{
			Type:     t,
			Text:     ev.Text,
			ToolID:   ev.ToolID,
			ToolName: ev.ToolName,
			ToolArgs: ev.ToolArgs,
			Result:   ev.Result,
			IsError:  ev.IsError,
		}
	}
	status := string(snap.Status)
	if status == "working" {
		status = "running"
	}
	return followEventData{
		ID:            snap.ID,
		Name:          snap.Name,
		Task:          snap.CurrentTask,
		Status:        status,
		Events:        events,
		EventsDropped: snap.EventsDropped,
	}
}

func (f *subAgentFollowState) rebuildActiveView(saMgr *subagent.Manager, swMgr *swarm.Manager, styles chat.Styles) {
	if !f.isActive() {
		return
	}
	entry := f.getOrCreateView(f.activeID, 0, 0)

	if saMgr != nil {
		if snap, ok := saMgr.Snapshot(f.activeID); ok {
			data := subagentSnapshotToFollowData(snap)

			if entry.eventsProcessed == len(data.Events) && entry.status == data.Status && entry.dropped == data.EventsDropped && entry.task == data.Task {
				f.markRebuilt(f.activeID)
				return
			}
			buildFollowList(data, entry.list, styles)
			entry.eventsProcessed = len(data.Events)
			entry.status = data.Status
			entry.dropped = data.EventsDropped
			entry.task = data.Task
			f.markRebuilt(f.activeID)
			return
		}
	}

	if swMgr != nil {
		if snap, ok := swMgr.TeammateSnapshot(f.activeID); ok {
			data := teammateSnapshotToFollowData(snap)

			if entry.eventsProcessed == len(data.Events) && entry.status == data.Status && entry.dropped == data.EventsDropped && entry.task == data.Task {
				f.markRebuilt(f.activeID)
				return
			}
			buildFollowList(data, entry.list, styles)
			entry.eventsProcessed = len(data.Events)
			entry.status = data.Status
			entry.dropped = data.EventsDropped
			entry.task = data.Task
			f.markRebuilt(f.activeID)
		}
	}
}

func (f *subAgentFollowState) shouldRebuild(agentID string) bool {
	if f.dirty == nil || !f.dirty[agentID] {
		return false
	}
	if f.lastRebuild == nil {
		return true
	}
	last, ok := f.lastRebuild[agentID]
	if !ok {
		return true
	}
	return time.Since(last) >= time.Second
}

func (f *subAgentFollowState) markRebuilt(agentID string) {
	if f.dirty != nil {
		delete(f.dirty, agentID)
	}
	if f.lastRebuild == nil {
		f.lastRebuild = make(map[string]time.Time)
	}
	f.lastRebuild[agentID] = time.Now()
}

func (f *subAgentFollowState) cleanup(saMgr *subagent.Manager, swMgr *swarm.Manager) {
	if f.views == nil || len(f.views) == 0 {
		return
	}
	active := make(map[string]bool)
	if saMgr != nil {
		for _, sa := range saMgr.List() {
			active[sa.ID] = true
		}
	}
	if swMgr != nil {
		for _, ts := range swMgr.ListTeams() {
			for _, tm := range ts.Teammates {
				active[tm.ID] = true
			}
		}
	}
	for id := range f.views {
		if !active[id] {
			delete(f.views, id)
			if f.dirty != nil {
				delete(f.dirty, id)
			}
			if f.lastRebuild != nil {
				delete(f.lastRebuild, id)
			}
		}
	}
}

func (m *Model) renderSubAgentFollowStrip() string {
	if m.subAgentFollow.slots == nil || len(m.subAgentFollow.slots) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("  ")

	maxShow := 5
	if len(m.subAgentFollow.slots) < maxShow {
		maxShow = len(m.subAgentFollow.slots)
	}

	start := 0
	activeIdx := -1
	for i, s := range m.subAgentFollow.slots {
		if s.ID == m.subAgentFollow.activeID {
			activeIdx = i
			break
		}
	}
	if activeIdx >= maxShow {
		start = activeIdx - maxShow + 1
	}
	if tailMax := len(m.subAgentFollow.slots) - maxShow; start > tailMax {
		start = tailMax
	}

	for i := start; i < start+maxShow; i++ {
		slot := m.subAgentFollow.slots[i]

		label := slot.Name
		if label == "" {
			label = shortID(slot.ID)
		}
		if slot.Model != "" {
			label = label + " [" + slot.Model + "]"
		}

		activity := slot.Phase
		if activity == "" {
			if slot.Terminal {
				activity = m.t("follow.status_done")
			} else {
				activity = m.t("follow.status_running")
			}
		}

		prefix := ""
		if slot.Kind == followSlotTeammate {
			prefix = "👤 "
		}

		chip := fmt.Sprintf("%s%s · %s", prefix, label, activity)

		if m.subAgentFollow.activeID == slot.ID {
			style := lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
			chip = fmt.Sprintf("▶ %s", chip)
			b.WriteString(style.Render(chip))
		} else {
			b.WriteString(chip)
		}

		if i < start+maxShow-1 && i < len(m.subAgentFollow.slots)-1 {
			b.WriteString("  │  ")
		}
	}

	if len(m.subAgentFollow.slots) > maxShow {
		b.WriteString(fmt.Sprintf(m.t("follow.more"), len(m.subAgentFollow.slots)-maxShow))
	}

	b.WriteString(m.t("follow.hint"))
	return lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render(b.String())
}

func (f *subAgentFollowState) hasTerminalSlots() bool {
	for _, s := range f.slots {
		if s.Terminal {
			return true
		}
	}
	return false
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

type textThrottleMap struct {
	mu    sync.Mutex
	last  map[string]time.Time
	delay time.Duration
}

func newTextThrottleMap(delay time.Duration) *textThrottleMap {
	return &textThrottleMap{last: make(map[string]time.Time), delay: delay}
}

func (t *textThrottleMap) Allow(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	last := t.last[id]
	if !last.IsZero() && now.Sub(last) < t.delay {
		return false
	}
	t.last[id] = now
	return true
}

func (t *textThrottleMap) Clear(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.last, id)
}
