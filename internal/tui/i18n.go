package tui

import (
	"fmt"
	"strings"
	"sync"
)

var (
	moduleCatalogsMu sync.RWMutex
	enModuleCatalogs = make(map[string]string)
	zhModuleCatalogs = make(map[string]string)
)

// registerCatalog merges a module's key-value pairs into the global extension catalogs.
// Called from init() in i18n module files.
func registerCatalog(en, zh map[string]string) {
	moduleCatalogsMu.Lock()
	defer moduleCatalogsMu.Unlock()
	for k, v := range en {
		enModuleCatalogs[k] = v
	}
	for k, v := range zh {
		zhModuleCatalogs[k] = v
	}
}

func lookupModuleCatalog(lang Language, key string) (string, bool) {
	moduleCatalogsMu.RLock()
	defer moduleCatalogsMu.RUnlock()
	switch lang {
	case LangZhCN:
		v, ok := zhModuleCatalogs[key]
		return v, ok
	default:
		v, ok := enModuleCatalogs[key]
		return v, ok
	}
}

type Language string

const (
	LangEnglish Language = "en"
	LangZhCN    Language = "zh-CN"
)

func normalizeLanguage(s string) Language {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "zh", "zh-cn", "zh_hans", "zh-hans", "cn", "zh-sg":
		return LangZhCN
	default:
		return LangEnglish
	}
}

func (m Model) currentLanguage() Language {
	if m.language != "" {
		return m.language
	}
	return LangEnglish
}

func (m Model) t(key string, args ...any) string {
	return tr(m.currentLanguage(), key, args...)
}

func (m *Model) setLanguage(lang string) {
	m.language = normalizeLanguage(lang)
	if m.config != nil {
		m.config.Language = string(m.language)
	}
	m.syncComposerMode()
	if m.providerPanel != nil {
		current := m.providerPanel.modelFilter.Value()
		focused := m.providerPanel.modelFilter.Focused()
		m.providerPanel.modelFilter = newModelFilterInput(m.currentLanguage())
		m.providerPanel.modelFilter.SetValue(current)
		if focused {
			m.providerPanel.modelFilter.Focus()
		}
	}
	if m.modelPanel != nil {
		current := m.modelPanel.filter.Value()
		focused := m.modelPanel.filter.Focused()
		m.modelPanel.filter = newModelFilterInput(m.currentLanguage())
		m.modelPanel.filter.SetValue(current)
		if focused {
			m.modelPanel.filter.Focus()
		}
	}
	if m.harnessPanel != nil {
		m.harnessPanel.actionInput.Placeholder = placeholderWithPasteShortcutHint(harnessPanelInputPlaceholder(m.harnessPanel.selectedSection, m.currentLanguage()), m.currentLanguage())
	}
	m.approvalOptions = defaultApprovalOptionsFor(m.currentLanguage())
	m.diffOptions = diffConfirmOptionsFor(m.currentLanguage())
	if len(m.langOptions) > 0 {
		m.langOptions = languageOptionsFor(m.currentLanguage())
	}
	if m.pendingQuestionnaire != nil {
		m.pendingQuestionnaire.loadActiveQuestion(m.currentLanguage())
		m.syncQuestionnaireInputWidth()
	}
}

func (m Model) languageLabel() string {
	switch m.currentLanguage() {
	case LangZhCN:
		return "简体中文"
	default:
		return "English"
	}
}

func supportedLanguageUsage(lang Language) string {
	if lang == LangZhCN {
		return "支持: en, zh-CN"
	}
	return "Supported: en, zh-CN"
}

func languageSwitchLabel(lang Language) string {
	if lang == LangZhCN {
		return "切换界面语言"
	}
	return "Switch interface language"
}

func languageOptionsFor(lang Language) []languageOption {
	switch lang {
	case LangZhCN:
		return []languageOption{
			{label: "简体中文", shortcut: "z", lang: LangZhCN},
			{label: "English", shortcut: "e", lang: LangEnglish},
		}
	default:
		return []languageOption{
			{label: "English", shortcut: "e", lang: LangEnglish},
			{label: "简体中文", shortcut: "z", lang: LangZhCN},
		}
	}
}

func localizeSlashDescription(lang Language, cmd string) string {
	switch cmd {
	case "/help", "/?":
		return tr(lang, "slash.help")
	case "/sessions":
		return tr(lang, "slash.sessions")
	case "/resume":
		return tr(lang, "slash.resume")
	case "/export":
		return tr(lang, "slash.export")
	case "/model":
		return tr(lang, "slash.model")
	case "/provider":
		return tr(lang, "slash.provider")
	case "/clear":
		return tr(lang, "slash.clear")
	case "/mcp":
		return tr(lang, "slash.mcp")
	case "/memory":
		return tr(lang, "slash.memory")
	case "/undo":
		return tr(lang, "slash.undo")
	case "/checkpoints":
		return tr(lang, "slash.checkpoints")
	case "/allow":
		return tr(lang, "slash.allow")
	case "/plugins":
		return tr(lang, "slash.plugins")
	case "/image":
		return tr(lang, "slash.image")
	case "/mode":
		return tr(lang, "slash.mode")
	case "/init":
		return tr(lang, "slash.init")
	case "/harness":
		return tr(lang, "slash.harness")
	case "/lang":
		return tr(lang, "slash.lang")
	case "/skills":
		return tr(lang, "slash.skills")
	case "/exit", "/quit":
		return tr(lang, "slash.exit")
	case "/compact":
		return tr(lang, "slash.compact")
	case "/todo":
		return tr(lang, "slash.todo")
	case "/bug":
		return tr(lang, "slash.bug")
	case "/config":
		return tr(lang, "slash.config")
	case "/status":
		return tr(lang, "slash.status")
	case "/update":
		return tr(lang, "slash.update")
	case "/restart":
		return tr(lang, "slash.restart")
	case "/qq":
		return tr(lang, "slash.qq")
	case "/telegram", "/tg":
		return tr(lang, "slash.telegram")
	case "/pc":
		return tr(lang, "slash.pc")
	case "/discord":
		return tr(lang, "slash.discord")
	case "/feishu", "/lark":
		return tr(lang, "slash.feishu")
	case "/slack":
		return tr(lang, "slash.slack")
	case "/dingtalk", "/ding":
		return tr(lang, "slash.dingtalk")
	case "/wechat":
		return tr(lang, "slash.wechat")
	case "/wecom":
		return tr(lang, "slash.wecom")
	case "/mattermost", "/mm":
		return tr(lang, "slash.mattermost")
	case "/matrix":
		return tr(lang, "slash.matrix")
	case "/signal":
		return tr(lang, "slash.signal")
	case "/irc":
		return tr(lang, "slash.irc")
	case "/nostr":
		return tr(lang, "slash.nostr")
	case "/twitch":
		return tr(lang, "slash.twitch")
	case "/whatsapp", "/wa":
		return tr(lang, "slash.whatsapp")
	case "/im":
		return tr(lang, "slash.im")
	case "/impersonate":
		return tr(lang, "slash.impersonate")
	case "/knight":
		return tr(lang, "slash.knight")
	case "/stream":
		return tr(lang, "slash.stream")
	default:
		return cmd
	}
}

func tr(lang Language, key string, args ...any) string {
	var msg string
	switch lang {
	case LangZhCN:
		msg = zhCatalog(key)
	default:
		msg = enCatalog(key)
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}
