package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/util"
)

func (m Model) renderContextPanel() string {
	if m.qrOverlay != nil {
		return m.renderQROverlay()
	}
	switch {
	case m.modelPanel != nil:
		return m.renderModelPanel()
	case m.tgPanel != nil:
		return m.renderTGPanel()
	case m.qqPanel != nil:
		return m.renderQQPanel()
	case m.pcPanel != nil:
		return m.renderPCPanel()
	case m.discordPanel != nil:
		return m.renderDiscordPanel()
	case m.feishuPanel != nil:
		return m.renderFeishuPanel()
	case m.slackPanel != nil:
		return m.renderSlackPanel()
	case m.dingtalkPanel != nil:
		return m.renderDingtalkPanel()
	case m.wechatPanel != nil:
		return m.renderWechatPanel()
	case m.wecomPanel != nil:
		return m.renderWeComPanel()
	case m.mattermostPanel != nil:
		return m.renderMattermostPanel()
	case m.matrixPanel != nil:
		return m.renderMatrixPanel()
	case m.signalPanel != nil:
		return m.renderSignalPanel()
	case m.ircPanel != nil:
		return m.renderIRCPanel()
	case m.nostrPanel != nil:
		return m.renderNostrPanel()
	case m.twitchPanel != nil:
		return m.renderTwitchPanel()
	case m.whatsappPanel != nil:
		return m.renderWhatsAppPanel()
	case m.imPanel != nil:
		return m.renderIMPanel()
	case m.mcpPanel != nil:
		return m.renderMCPPanel()
	case m.streamPanel != nil:
		return m.renderStreamPanel()
	case m.knightPanel != nil:
		return m.renderKnightPanel()
	case m.skillsPanel != nil:
		return m.renderSkillsPanel()
	case m.statsPanel != nil:
		return m.renderStatsPanel()
	case m.inspectorPanel != nil:
		return m.renderInspectorPanel()
	case m.harnessContextPrompt != nil:
		return m.renderHarnessContextPrompt()
	case m.harnessPanel != nil:
		return m.renderHarnessPanel()
	case m.impersonatePanel != nil:
		return m.renderImpersonatePanel()
	case m.lanChatPanel != nil:
		return m.renderLanChatPanel()
	case m.providerPanel != nil:
		return m.renderProviderPanel()
	case m.pendingPairingChallenge() != nil:
		return m.renderIMPairingPanel()
	case m.pendingQuestionnaire != nil:
		return m.renderQuestionnairePanel()
	case m.pendingApproval != nil:
		title := m.t("panel.approval_required")
		accent := lipgloss.Color("11")
		if m.mode == permission.BypassMode || m.mode == permission.AutopilotMode {
			title = m.t("panel.bypass_approval")
			accent = lipgloss.Color("9")
		}
		present := describeTool(m.currentLanguage(), m.pendingApproval.ToolName, m.pendingApproval.Input)
		toolLine := formatToolInline(present.DisplayName, present.Detail)
		body := fmt.Sprintf(" %s   %s\n %s  %s\n\n%s\n%s",
			m.t("label.tool"),
			toolLine,
			m.t("label.input"),
			util.Truncate(compactToolArgsPreview(strings.ReplaceAll(m.pendingApproval.Input, "\n", " ")), 220),
			m.renderApprovalOptions(m.approvalOptions, m.approvalCursor),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" Tab/j/k move • Enter confirm • y/n/a shortcuts"),
		)
		return m.renderContextBox(title, body, accent)
	case m.pendingDiffConfirm != nil:
		body := fmt.Sprintf(" %s   %s\n\n%s\n\n%s\n%s",
			m.t("label.file"),
			displayToolFileTarget(m.pendingDiffConfirm.FilePath),
			truncateLines(strings.TrimSpace(FormatDiff(m.pendingDiffConfirm.DiffText)), 12),
			m.renderApprovalOptions(m.diffOptions, m.diffCursor),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" Tab/j/k move • Enter confirm • y/n shortcuts"),
		)
		return m.renderContextBox(m.t("panel.review_file_change"), body, lipgloss.Color("13"))
	case m.pendingHarnessCheckpointConfirm != nil:
		checkpoint := m.pendingHarnessCheckpointConfirm.Checkpoint
		body := fmt.Sprintf(" Dirty workspace\n\n %s\n %s   %s\n\n%s\n%s",
			truncateLines(strings.TrimSpace(checkpoint.Summary), 6),
			"commit",
			checkpoint.CommitMessage,
			m.renderApprovalOptions(m.diffOptions, m.diffCursor),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" Tab/j/k move • Enter confirm • y/n shortcuts"),
		)
		return m.renderContextBox("Confirm harness checkpoint", body, lipgloss.Color("13"))
	case len(m.langOptions) > 0:
		title := languageSwitchLabel(m.currentLanguage())
		bodyLine := m.t("lang.selection.current", m.languageLabel())
		hint := m.t("lang.selection.hint")
		accent := lipgloss.Color("10")
		if m.languagePromptRequired {
			title = m.t("lang.first_use.title")
			bodyLine = m.t("lang.first_use.body")
			hint = m.t("lang.first_use.hint")
			accent = lipgloss.Color("11")
		}
		body := fmt.Sprintf("%s\n\n%s\n%s",
			bodyLine,
			m.renderLanguageOptions(m.langOptions, m.langCursor),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(hint),
		)
		return m.renderContextBox(title, body, accent)
	case m.autoCompleteActive && len(m.autoCompleteItems) > 0:
		return m.renderAutoComplete()
	case m.initPromptActive:
		return m.renderInitPromptPanel()
	default:
		return ""
	}
}

func (m Model) renderInitPromptPanel() string {
	body := fmt.Sprintf("%s\n\n%s\n%s",
		m.t("init.prompt.body"),
		fmt.Sprintf("%s  %s",
			lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true).Render("[y] "+m.t("init.prompt.yes")),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("[n] "+m.t("init.prompt.no")),
		),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.t("init.prompt.hint")),
	)
	return m.renderContextBox(m.t("init.prompt.title"), body, lipgloss.Color("11"))
}
func (m Model) renderIMPairingPanel() string {
	challenge := m.pendingPairingChallenge()
	if challenge == nil {
		return ""
	}
	platformName := platformDisplayName(challenge.Platform)
	title := fmt.Sprintf("%s pairing required", platformName)
	bodyText := fmt.Sprintf("A %s channel is requesting to bind this workspace. Ask the user to enter the 4-digit code shown below in %s.", platformName, platformName)
	channelLabel := "request channel"
	boundLabel := "currently bound"
	codeLabel := "pairing code"
	hint := fmt.Sprintf("Esc reject • the correct code in %s will complete binding automatically", platformName)
	if m.currentLanguage() == LangZhCN {
		cnName := platformCNName(challenge.Platform)
		title = fmt.Sprintf("%s 绑定验证", cnName)
		bodyText = fmt.Sprintf("有一个 %s 渠道正在请求绑定当前工作区。请让用户在 %s 中输入下方 4 位绑定码。", cnName, cnName)
		channelLabel = "请求渠道"
		boundLabel = "当前绑定"
		codeLabel = "绑定码"
		hint = fmt.Sprintf("Esc 拒绝 • %s 中输入正确绑定码后会自动完成绑定", cnName)
	}
	if challenge.Kind == im.PairingKindRebind {
		if m.currentLanguage() == LangZhCN {
			cnName := platformCNName(challenge.Platform)
			title = fmt.Sprintf("%s 重新绑定验证", cnName)
			bodyText = fmt.Sprintf("当前 bot 已经绑定到其他渠道。新渠道在 %s 中输入下方 4 位绑定码后，将解绑旧渠道并切换到当前渠道。", cnName)
		} else {
			title = fmt.Sprintf("%s rebind requested", platformName)
			bodyText = fmt.Sprintf("This bot is already bound to another channel. Entering the 4-digit code below in %s will unbind the old channel and switch to the new channel.", platformName)
		}
	}

	codeDigits := strings.Join(strings.Split(challenge.Code, ""), "   ")
	codeStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("230")).
		Background(lipgloss.Color("57")).
		Padding(1, 3).
		Margin(1, 0).
		Render(codeDigits)

	lines := []string{bodyText, ""}
	lines = append(lines, fmt.Sprintf(" %s   %s", channelLabel, util.FirstNonEmpty(strings.TrimSpace(challenge.ChannelID), "-")))
	if challenge.ExistingBinding != nil && strings.TrimSpace(challenge.ExistingBinding.ChannelID) != "" {
		lines = append(lines, fmt.Sprintf(" %s   %s", boundLabel, challenge.ExistingBinding.ChannelID))
	}
	lines = append(lines,
		"",
		lipgloss.NewStyle().Bold(true).Render(codeLabel),
		codeStyle,
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(hint),
	)
	return m.renderContextBox(title, strings.Join(lines, "\n"), lipgloss.Color("11"))
}

func platformDisplayName(p im.Platform) string {
	switch p {
	case im.PlatformQQ:
		return "QQ"
	case im.PlatformFeishu:
		return "Feishu"
	case im.PlatformTelegram:
		return "Telegram"
	case im.PlatformDiscord:
		return "Discord"
	case im.PlatformDingTalk:
		return "DingTalk"
	case im.PlatformSlack:
		return "Slack"
	case im.PlatformWechat:
		return "WeChat"
	case im.PlatformWeCom:
		return "WeCom"
	case im.PlatformMattermost:
		return "Mattermost"
	case im.PlatformMatrix:
		return "Matrix"
	case im.PlatformSignal:
		return "Signal"
	case im.PlatformIRC:
		return "IRC"
	case im.PlatformNostr:
		return "Nostr"
	case im.PlatformTwitch:
		return "Twitch"
	case im.PlatformWhatsApp:
		return "WhatsApp"
	default:
		return "IM"
	}
}

func platformCNName(p im.Platform) string {
	switch p {
	case im.PlatformQQ:
		return "QQ"
	case im.PlatformFeishu:
		return "飞书"
	case im.PlatformTelegram:
		return "Telegram"
	case im.PlatformDiscord:
		return "Discord"
	case im.PlatformDingTalk:
		return "钉钉"
	case im.PlatformSlack:
		return "Slack"
	case im.PlatformWechat:
		return "微信"
	case im.PlatformWeCom:
		return "企业微信"
	case im.PlatformMattermost:
		return "Mattermost"
	case im.PlatformSignal:
		return "Signal"
	case im.PlatformIRC:
		return "IRC"
	case im.PlatformNostr:
		return "Nostr"
	case im.PlatformTwitch:
		return "Twitch"
	case im.PlatformWhatsApp:
		return "WhatsApp"
	default:
		return "IM"
	}
}

func (m Model) renderAuxColumn() string {
	if m.sidebarEnabled() {
		return m.renderSidebar()
	}
	return ""
}
