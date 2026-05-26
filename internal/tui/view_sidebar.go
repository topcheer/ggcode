package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/muesli/reflow/wordwrap"

	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/util"
	"github.com/topcheer/ggcode/internal/version"
)

func (m Model) renderSidebar() string {
	if tracker := m.renderSidebarTaskTracker(); tracker != "" {
		return tracker
	}
	vendor, _, model := m.currentSelection()

	body := strings.Join([]string{
		"",
		renderSidebarLogo(m.sidebarWidth()-4, sidebarHomepageURL),
		"",
		m.styles.title.Render("ggcode (" + version.Version + ")"),
		m.renderSidebarDetailRow(m.t("label.model"), vendor+"/"+model, m.sidebarWidth()-4),
		m.renderSidebarDetailRow(m.t("label.context"), m.sidebarContextWindowLabel(), m.sidebarWidth()-4),
		m.renderSidebarDetailRow(m.t("label.branch"), util.FirstNonEmpty(m.sidebarGitBranch(), "-"), m.sidebarWidth()-4),
		m.renderSidebarDetailRow(m.t("label.skills"), fmt.Sprintf("%d", m.loadedSkillCount()), m.sidebarWidth()-4),
		"",
		m.renderSidebarSessionUsageSection(),
		m.renderSidebarUpdateSection(),
		"",
		m.renderSidebarIMSection(),
		"",
		m.renderSidebarMCPSection(),
		"",
		m.renderSidebarA2ASection(),
	}, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(chromeBorderColor).
		Padding(0, 1).
		Width(m.boxInnerWidth(m.sidebarWidth())).
		Render(body)
}

func (m Model) renderSidebarSessionUsageSection() string {
	width := max(12, m.sidebarWidth()-4)
	usage := m.sidebarSessionUsage()
	renderUsageRow := func(label, value string) string {
		return m.renderSidebarDetailRowWithLabelWidth(label, value, width, 11)
	}
	rows := []string{
		m.renderSidebarSectionTitle(m.t("panel.session_usage")),
		renderUsageRow(m.t("label.total"), humanizeTokenCount(usage.Total())),
		renderUsageRow(m.t("label.input"), humanizeTokenCount(usage.InputTokens)),
		renderUsageRow(m.t("label.output"), humanizeTokenCount(usage.OutputTokens)),
		renderUsageRow(m.t("label.cache_read"), humanizeTokenCount(usage.CacheRead)),
		renderUsageRow(m.t("label.cache_write"), humanizeTokenCount(usage.CacheWrite)),
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderSidebarContextSection() string {
	width := max(12, m.sidebarWidth()-4)
	rows := []string{m.renderSidebarSectionTitle(m.t("panel.context"))}
	stats, ok := m.sidebarContextStats()
	if !ok {
		rows = append(rows, util.Truncate(m.t("context.unavailable"), width))
		return strings.Join(rows, "\n")
	}

	rows = append(rows,
		m.renderSidebarDetailRow(m.t("label.window"), humanizeTokenCount(stats.maxTokens), width),
		m.renderSidebarDetailRow(m.t("label.usage"), fmt.Sprintf("%d%%", stats.usagePercent), width),
		m.renderSidebarDetailRow(m.t("label.compact"), fmt.Sprintf("%d%% %s", stats.remainingPercent, m.t("context.until_compact")), width),
	)
	return strings.Join(rows, "\n")
}

func (m Model) renderSidebarMCPSection() string {
	width := max(12, m.sidebarWidth()-4)
	rows := []string{m.renderSidebarSectionTitle(m.t("panel.mcp"))}
	// Filter out disabled MCP servers
	activeServers := make([]MCPInfo, 0, len(m.mcpServers))
	for _, srv := range m.mcpServers {
		if !srv.Disabled {
			activeServers = append(activeServers, srv)
		}
	}
	if len(activeServers) == 0 {
		rows = append(rows, util.Truncate(m.t("mcp.none"), width))
		return strings.Join(rows, "\n")
	}
	connected, pending, failed := 0, 0, 0
	for _, srv := range activeServers {
		switch {
		case srv.Connected:
			connected++
		case srv.Pending:
			pending++
		default:
			failed++
		}
	}
	rows = append(rows, util.Truncate(fmt.Sprintf("%d up • %d pending • %d failed", connected, pending, failed), width))
	visibleServers := activeServers
	if len(visibleServers) > 5 {
		visibleServers = visibleServers[:5]
	}
	for _, srv := range visibleServers {
		icon := "✕"
		switch {
		case srv.Connected:
			icon = "✓"
		case srv.Pending:
			icon = "…"
		}
		label := fmt.Sprintf("%s %s (%s)", icon, srv.Name, util.FirstNonEmpty(srv.Transport, "stdio"))
		rows = append(rows, util.Truncate(label, width))
	}
	if hidden := len(activeServers) - len(visibleServers); hidden > 0 {
		rows = append(rows, util.Truncate(m.t("mcp.more", hidden), width))
	}
	if active := m.activeMCPToolSummaries(); len(active) > 0 {
		rows = append(rows, "", util.Truncate(m.t("mcp.active_tools"), width))
		for _, item := range active {
			rows = append(rows, util.Truncate("• "+item, width))
		}
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderSidebarA2ASection() string {
	if m.a2aHandler == nil {
		return ""
	}
	active := m.a2aHandler.ActiveTaskCount()
	if active == 0 && len(m.a2aEventBuf) == 0 {
		return ""
	}

	width := max(12, m.sidebarWidth()-4)
	rows := []string{m.renderSidebarSectionTitle("A2A")}

	if active > 0 {
		rows = append(rows, m.renderSidebarDetailRow(
			m.t("label.active"), fmt.Sprintf("%d %s", active, m.t("label.tasks")), width,
		))
		tasks := m.a2aHandler.ActiveTasks()
		for _, t := range tasks {
			if len(tasks) > 3 {
				rows = append(rows, m.renderSidebarDetailRow(
					"  "+t.ID[:8], string(t.Status.State), width,
				))
			} else {
				rows = append(rows, m.renderSidebarDetailRow(
					fmt.Sprintf("  %s [%s]", t.ID[:8], t.Skill), string(t.Status.State), width,
				))
			}
		}
	}

	// Show last few events
	m.a2aMu.Lock()
	events := m.a2aEventBuf
	if len(events) > 3 {
		events = events[len(events)-3:]
	}
	m.a2aMu.Unlock()

	for _, evt := range events {
		icon := "●"
		switch evt.Type {
		case "start":
			icon = "▶"
		case "complete":
			icon = "✓"
		case "fail":
			icon = "✗"
		case "cancel":
			icon = "⊘"
		}
		msg := evt.Message
		if len(msg) > width-6 {
			msg = msg[:width-9] + "..."
		}
		rows = append(rows, fmt.Sprintf("  %s %s", icon, msg))
	}

	return strings.Join(rows, "\n")
}

func (m Model) renderSidebarIMSection() string {
	width := max(12, m.sidebarWidth()-4)
	rows := []string{m.renderSidebarSectionTitle(m.t("panel.im"))}
	if m.imManager == nil && (m.config == nil || !m.config.IM.Enabled) {
		rows = append(rows, util.Truncate(m.sidebarIMRuntimeStatus(), width))
		return strings.Join(rows, "\n")
	}
	adapters := m.sidebarIMAdapters()
	if len(adapters) == 0 {
		rows = append(rows, util.Truncate(m.t("im.none"), width))
		return strings.Join(rows, "\n")
	}
	healthy := 0
	for _, state := range adapters {
		if state.Healthy {
			healthy++
		}
	}
	rows = append(rows, util.Truncate(m.t("im.summary", len(adapters), healthy), width))
	visible := adapters
	if len(visible) > 5 {
		visible = visible[:5]
	}
	for _, state := range visible {
		rows = append(rows, util.Truncate(sidebarIMAdapterLabel(state), width))
	}
	if hidden := len(adapters) - len(visible); hidden > 0 {
		rows = append(rows, util.Truncate(m.t("im.more", hidden), width))
	}
	return strings.Join(rows, "\n")
}

func (m Model) sidebarIMSnapshot() (im.StatusSnapshot, bool) {
	if m.imManager == nil {
		return im.StatusSnapshot{}, false
	}
	return m.imManager.Snapshot(), true
}

func (m Model) sidebarIMAdapters() []im.AdapterState {
	statesByName := make(map[string]im.AdapterState)
	mutedAdapters := make(map[string]bool)
	var currentBindings []im.ChannelBinding
	if snapshot, ok := m.sidebarIMSnapshot(); ok {
		currentBindings = snapshot.CurrentBindings
		for _, state := range snapshot.Adapters {
			statesByName[state.Name] = state
		}
		for _, b := range currentBindings {
			if b.Muted {
				mutedAdapters[b.Adapter] = true
			}
		}
	}
	if len(currentBindings) == 0 {
		return nil
	}
	var result []im.AdapterState
	seen := make(map[string]bool)
	for _, binding := range currentBindings {
		name := strings.TrimSpace(binding.Adapter)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if state, ok := statesByName[name]; ok {
			if mutedAdapters[name] {
				state.Status = "muted"
				state.Healthy = false
			}
			result = append(result, state)
		} else if m.config != nil {
			if adapter, ok := m.config.IM.Adapters[name]; ok && adapter.Enabled {
				status := m.t("im.status.not_started")
				if mutedAdapters[name] {
					status = "muted"
				}
				result = append(result, im.AdapterState{
					Name:     name,
					Platform: im.Platform(strings.TrimSpace(adapter.Platform)),
					Status:   status,
				})
			}
		}
	}
	return result
}

func (m Model) sidebarIMRuntimeStatus() string {
	if m.imManager != nil {
		return m.t("im.runtime.available")
	}
	if m.config == nil || !m.config.IM.Enabled {
		return m.t("im.runtime.disabled")
	}
	return m.t("im.runtime.not_started")
}

func sidebarIMAdapterLabel(state im.AdapterState) string {
	icon := "✕"
	switch {
	case state.Healthy:
		icon = "✓"
	case strings.TrimSpace(state.Status) == "muted":
		icon = "○"
	case strings.TrimSpace(state.LastError) == "":
		icon = "…"
	}
	status := compactSingleLine(util.FirstNonEmpty(strings.TrimSpace(state.Status), strings.TrimSpace(state.LastError), "unknown"))
	if status == "" {
		status = "unknown"
	}
	platform := strings.TrimSpace(string(state.Platform))
	if platform == "" {
		platform = "im"
	}
	return fmt.Sprintf("%s %s (%s) %s", icon, util.FirstNonEmpty(strings.TrimSpace(state.Name), "adapter"), platform, status)
}

func (m Model) renderSidebarTaskTracker() string {
	tasks := m.sidebarTrackedTodos()
	if len(tasks) == 0 {
		return ""
	}
	width := max(12, m.sidebarWidth()-4)
	rows := []string{
		"",
		m.renderSidebarSectionTitle(sidebarTaskTrackerTitle(m.currentLanguage())),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(sidebarTaskTrackerHint(m.currentLanguage())),
		"",
	}
	for _, task := range tasks {
		rows = append(rows, m.renderSidebarTaskRow(task, width)...)
	}
	body := strings.Join(rows, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(chromeBorderColor).
		Padding(0, 1).
		Width(m.boxInnerWidth(m.sidebarWidth())).
		Render(body)
}

func (m Model) sidebarTrackedTodos() []todoStateItem {
	if len(m.todoSnapshot) == 0 {
		return nil
	}
	items := make([]todoStateItem, 0, len(m.todoOrder))
	for _, id := range m.todoOrder {
		if td, ok := m.todoSnapshot[id]; ok {
			items = append(items, td)
		}
	}
	return items
}

func (m Model) renderSidebarTaskRow(task todoStateItem, width int) []string {
	bullet, statusLabel := sidebarTaskStatusDecor(m.currentLanguage(), task.Status)
	titleWidth := max(8, width-2)
	title := compactSingleLine(task.Content)
	if title == "" {
		title = util.FirstNonEmpty(task.ID, "-")
	}
	wrapped := wordwrap.String(title, titleWidth)
	lines := strings.Split(wrapped, "\n")
	rows := make([]string, 0, len(lines)+1)
	for i, line := range lines {
		if i == 0 {
			rows = append(rows, bullet+" "+line)
		} else {
			// Continuation lines aligned with the title text (bullet + space = 2 chars)
			rows = append(rows, "  "+line)
		}
	}
	detail := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  " + statusLabel)
	rows = append(rows, detail)
	return rows
}

func sidebarTaskStatusDecor(lang Language, status string) (string, string) {
	switch status {
	case "done":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("●"), sidebarTaskStatusText(lang, status)
	case "blocked", "failed":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("●"), sidebarTaskStatusText(lang, status)
	case "in_progress":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Render("●"), sidebarTaskStatusText(lang, status)
	default: // pending and unknown
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("○"), sidebarTaskStatusText(lang, status)
	}
}

func sidebarTaskStatusText(lang Language, status string) string {
	switch lang {
	case LangZhCN:
		switch status {
		case "done":
			return "已完成"
		case "blocked", "failed":
			return "已失败"
		case "in_progress":
			return "进行中"
		default:
			return "待处理"
		}
	default:
		switch status {
		case "done":
			return "done"
		case "blocked", "failed":
			return "failed"
		case "in_progress":
			return "in progress"
		default:
			return "pending"
		}
	}
}

func sidebarTaskTrackerTitle(lang Language) string {
	if lang == LangZhCN {
		return "当前任务"
	}
	return "Current tasks"
}

func sidebarTaskTrackerHint(lang Language) string {
	if lang == LangZhCN {
		return "活动会话任务追踪（按启动时间倒序）"
	}
	return "Active-session task tracker (newest started first)"
}

func sidebarTaskTrackerMore(lang Language, remaining int) string {
	if lang == LangZhCN {
		return fmt.Sprintf("… 还有 %d 项", remaining)
	}
	return fmt.Sprintf("… %d more", remaining)
}

func (m Model) renderSidebarSectionTitle(title string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("111")).
		Italic(true).
		Bold(true).
		Render(title)
}

func (m Model) renderSidebarDetailRow(label, value string, width int) string {
	return m.renderSidebarDetailRowWithLabelWidth(label, value, width, 9)
}

func (m Model) renderSidebarDetailRowWithLabelWidth(label, value string, width, labelWidth int) string {
	if labelWidth < 1 {
		labelWidth = 1
	}
	key := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Width(labelWidth).
		MaxWidth(labelWidth).
		Render(truncateDisplayWidth(label, labelWidth))
	valueWidth := max(1, width-labelWidth-1)
	return lipgloss.JoinHorizontal(lipgloss.Top, key, " ", truncateDisplayWidth(value, valueWidth))
}

func (m Model) renderSidebarBadgeRow(label, badge string) string {
	const labelWidth = 9
	key := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Width(labelWidth).
		MaxWidth(labelWidth).
		Render(truncateDisplayWidth(label, labelWidth))
	return key + " " + badge
}

func truncateDisplayWidth(s string, maxWidth int) string {
	s = strings.TrimSpace(s)
	if maxWidth <= 0 || s == "" {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	if maxWidth <= 3 {
		return fitDisplayWidth(s, maxWidth)
	}
	return fitDisplayWidth(s, maxWidth-3) + "..."
}

func fitDisplayWidth(s string, maxWidth int) string {
	if maxWidth <= 0 || s == "" {
		return ""
	}
	var b strings.Builder
	currentWidth := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if currentWidth+rw > maxWidth {
			break
		}
		b.WriteRune(r)
		currentWidth += rw
	}
	return b.String()
}

func sidebarModeApprovalKey(mode permission.PermissionMode) string {
	switch mode {
	case permission.PlanMode:
		return "mode.approval.none"
	case permission.AutoMode:
		return "mode.approval.none"
	case permission.BypassMode:
		return "mode.approval.critical"
	case permission.AutopilotMode:
		return "mode.approval.none"
	default:
		return "mode.approval.ask"
	}
}

func sidebarModeToolsKey(mode permission.PermissionMode) string {
	switch mode {
	case permission.PlanMode:
		return "mode.tools.readonly"
	case permission.AutoMode:
		return "mode.tools.safe"
	case permission.BypassMode:
		return "mode.tools.open"
	case permission.AutopilotMode:
		return "mode.tools.open"
	default:
		return "mode.tools.rules"
	}
}

func sidebarModeAgentKey(mode permission.PermissionMode) string {
	switch mode {
	case permission.AutopilotMode:
		return "mode.agent.autocontinue"
	default:
		return "mode.agent.waits"
	}
}

type sidebarContextStatLine struct {
	maxTokens        int
	usagePercent     int
	remainingPercent int
}

func (m Model) sidebarContextStats() (sidebarContextStatLine, bool) {
	if m.agent == nil {
		return sidebarContextStatLine{}, false
	}
	cm := m.agent.ContextManager()
	if cm == nil {
		return sidebarContextStatLine{}, false
	}
	maxTokens := cm.ContextWindow()
	tokenCount := cm.TokenCount()
	threshold := cm.AutoCompactThreshold()
	if maxTokens <= 0 || threshold <= 0 {
		return sidebarContextStatLine{}, false
	}

	usagePercent := int(float64(tokenCount) / float64(maxTokens) * 100)
	if usagePercent < 0 {
		usagePercent = 0
	}
	if usagePercent > 100 {
		usagePercent = 100
	}

	remainingPercent := int(float64(threshold-tokenCount) / float64(threshold) * 100)
	if remainingPercent < 0 {
		remainingPercent = 0
	}
	if remainingPercent > 100 {
		remainingPercent = 100
	}

	return sidebarContextStatLine{
		maxTokens:        maxTokens,
		usagePercent:     usagePercent,
		remainingPercent: remainingPercent,
	}, true
}

func (m Model) sidebarSessionUsage() provider.TokenUsage {
	if m.session == nil {
		return provider.TokenUsage{}
	}
	return m.session.TokenUsage
}

func humanizeTokenCount(n int) string {
	if n >= 1000000 && n%1000000 == 0 {
		return fmt.Sprintf("%dm", n/1000000)
	}
	if n >= 1000 && n%1000 == 0 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%d", n)
}

func (m Model) sidebarWorkingDirectory() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	return shortenSidebarPath(cwd)
}

// sidebarContextWindowLabel returns a human-readable label for the current
// model's context window size, e.g. "128K" or "204.8K".
func (m Model) sidebarContextWindowLabel() string {
	if m.agent == nil {
		return "-"
	}
	cm := m.agent.ContextManager()
	if cm == nil {
		return "-"
	}
	cw := cm.ContextWindow()
	if cw <= 0 {
		return "-"
	}
	return humanizeTokenCount(cw)
}

func (m Model) sidebarGitBranch() string {
	gitDirCacheMu.RLock()
	v := gitBranchCache
	gitDirCacheMu.RUnlock()
	return v
}

// refreshCachedGitBranch reads the current git branch and caches the result.
// Called periodically from the Update loop via gitBranchTickMsg timer.
func (m *Model) refreshCachedGitBranch() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	branch, err := gitBranchForDir(cwd)
	if err != nil {
		return
	}
	gitDirCacheMu.Lock()
	gitBranchCache = branch
	gitDirCacheMu.Unlock()
}

func (m Model) loadedSkillCount() int {
	if m.commandMgr == nil {
		return 0
	}
	count := 0
	for _, cmd := range m.commandMgr.Commands() {
		if cmd == nil || !cmd.Enabled {
			continue
		}
		switch cmd.LoadedFrom {
		case commands.LoadedFromSkills, commands.LoadedFromBundled, commands.LoadedFromPlugin:
			count++
		}
	}
	return count
}

func shortenSidebarPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = filepath.ToSlash(filepath.Clean(value))
	home := config.HomeDir()
	home = filepath.ToSlash(filepath.Clean(home))
	if value == home {
		return "~"
	}
	if strings.HasPrefix(value, home+"/") {
		return "~/" + strings.TrimPrefix(value, home+"/")
	}
	return value
}

// gitBranchCache caches the resolved git branch string to avoid os.ReadFile
// on every View() render. Updated by gitBranchTickMsg timer every 2 seconds.
var (
	gitDirCacheDir   string
	gitDirCacheValue string
	gitBranchCache   string
	gitDirCacheMu    sync.RWMutex
)

func resolveGitDirCached(start string) (string, error) {
	gitDirCacheMu.RLock()
	if gitDirCacheDir == start && gitDirCacheValue != "" {
		v := gitDirCacheValue
		gitDirCacheMu.RUnlock()
		return v, nil
	}
	gitDirCacheMu.RUnlock()

	result, err := resolveGitDir(start)
	if err != nil {
		return "", err
	}
	gitDirCacheMu.Lock()
	gitDirCacheDir = start
	gitDirCacheValue = result
	gitDirCacheMu.Unlock()
	return result, nil
}

func gitBranchForDir(start string) (string, error) {
	gitDir, err := resolveGitDirCached(start)
	if err != nil {
		return "", err
	}
	headBytes, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(string(headBytes))
	const prefix = "ref: refs/heads/"
	if strings.HasPrefix(head, prefix) {
		return strings.TrimPrefix(head, prefix), nil
	}
	return "", nil
}

func resolveGitDir(start string) (string, error) {
	dir := start
	for {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitPath)
		if err == nil {
			if info.IsDir() {
				return gitPath, nil
			}
			data, readErr := os.ReadFile(gitPath)
			if readErr != nil {
				return "", readErr
			}
			line := strings.TrimSpace(string(data))
			const prefix = "gitdir:"
			if !strings.HasPrefix(strings.ToLower(line), prefix) {
				return "", fmt.Errorf("unsupported .git file format")
			}
			target := strings.TrimSpace(line[len(prefix):])
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			return target, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
