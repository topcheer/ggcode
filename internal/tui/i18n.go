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
	jaModuleCatalogs = make(map[string]string)
	koModuleCatalogs = make(map[string]string)
	esModuleCatalogs = make(map[string]string)
	frModuleCatalogs = make(map[string]string)
	deModuleCatalogs = make(map[string]string)
	ruModuleCatalogs = make(map[string]string)
	ptModuleCatalogs = make(map[string]string)
	viModuleCatalogs = make(map[string]string)
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
	case LangJa:
		v, ok := jaModuleCatalogs[key]
		if ok {
			return v, true
		}
	case LangKo:
		v, ok := koModuleCatalogs[key]
		if ok {
			return v, true
		}
	case LangEs:
		v, ok := esModuleCatalogs[key]
		if ok {
			return v, true
		}
	case LangFr:
		v, ok := frModuleCatalogs[key]
		if ok {
			return v, true
		}
	case LangDe:
		v, ok := deModuleCatalogs[key]
		if ok {
			return v, true
		}
	case LangRu:
		v, ok := ruModuleCatalogs[key]
		if ok {
			return v, true
		}
	case LangPt:
		v, ok := ptModuleCatalogs[key]
		if ok {
			return v, true
		}
	case LangVi:
		v, ok := viModuleCatalogs[key]
		if ok {
			return v, true
		}
	}
	v, ok := enModuleCatalogs[key]
	return v, ok
}

type Language string

const (
	LangEnglish Language = "en"
	LangZhCN    Language = "zh-CN"
	LangJa      Language = "ja"
	LangKo      Language = "ko"
	LangEs      Language = "es"
	LangFr      Language = "fr"
	LangDe      Language = "de"
	LangRu      Language = "ru"
	LangPt      Language = "pt"
	LangVi      Language = "vi"
)

func normalizeLanguage(s string) Language {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "zh", "zh-cn", "zh_hans", "zh-hans", "cn", "zh-sg":
		return LangZhCN
	case "ja", "ja-jp", "japanese", "jp":
		return LangJa
	case "ko", "ko-kr", "korean", "kr":
		return LangKo
	case "es", "es-es", "spanish":
		return LangEs
	case "fr", "fr-fr", "french":
		return LangFr
	case "de", "de-de", "german":
		return LangDe
	case "ru", "ru-ru", "russian":
		return LangRu
	case "pt", "pt-br", "pt-pt", "portuguese":
		return LangPt
	case "vi", "vi-vn", "vietnamese":
		return LangVi
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
	case LangJa:
		return "日本語"
	case LangKo:
		return "한국어"
	case LangEs:
		return "Español"
	case LangFr:
		return "Français"
	case LangDe:
		return "Deutsch"
	case LangRu:
		return "Русский"
	case LangPt:
		return "Português"
	case LangVi:
		return "Tiếng Việt"
	default:
		return "English"
	}
}

func supportedLanguageUsage(lang Language) string {
	switch lang {
	case LangZhCN:
		return "支持: en, zh-CN, ja, ko, es, fr, de, ru, pt, vi"
	case LangJa:
		return "対応: en, zh-CN, ja, ko, es, fr, de, ru, pt, vi"
	case LangKo:
		return "지원: en, zh-CN, ja, ko, es, fr, de, ru, pt, vi"
	case LangEs:
		return "Soportados: en, zh-CN, ja, ko, es, fr, de, ru, pt, vi"
	case LangFr:
		return "Supportés: en, zh-CN, ja, ko, es, fr, de, ru, pt, vi"
	case LangDe:
		return "Unterstützt: en, zh-CN, ja, ko, es, fr, de, ru, pt, vi"
	case LangRu:
		return "Поддерживаются: en, zh-CN, ja, ko, es, fr, de, ru, pt, vi"
	case LangPt:
		return "Suportados: en, zh-CN, ja, ko, es, fr, de, ru, pt, vi"
	case LangVi:
		return "Hỗ trợ: en, zh-CN, ja, ko, es, fr, de, ru, pt, vi"
	default:
		return "Supported: en, zh-CN, ja, ko, es, fr, de, ru, pt, vi"
	}
}

func languageSwitchLabel(lang Language) string {
	switch lang {
	case LangZhCN:
		return "切换界面语言"
	case LangJa:
		return "言語を切り替え"
	case LangKo:
		return "언어 전환"
	case LangEs:
		return "Cambiar idioma"
	case LangFr:
		return "Changer de langue"
	case LangDe:
		return "Sprache wechseln"
	case LangRu:
		return "Сменить язык"
	case LangPt:
		return "Mudar idioma"
	case LangVi:
		return "Đổi ngôn ngữ"
	default:
		return "Switch interface language"
	}
}

func languageOptionsFor(lang Language) []languageOption {
	all := []languageOption{
		{label: "English", shortcut: "e", lang: LangEnglish},
		{label: "简体中文", shortcut: "z", lang: LangZhCN},
		{label: "日本語", shortcut: "2", lang: LangJa},
		{label: "한국어", shortcut: "3", lang: LangKo},
		{label: "Español", shortcut: "4", lang: LangEs},
		{label: "Français", shortcut: "5", lang: LangFr},
		{label: "Deutsch", shortcut: "6", lang: LangDe},
		{label: "Русский", shortcut: "7", lang: LangRu},
		{label: "Português", shortcut: "8", lang: LangPt},
		{label: "Tiếng Việt", shortcut: "9", lang: LangVi},
	}
	// Move current language to top
	result := make([]languageOption, 0, len(all))
	for _, opt := range all {
		if opt.lang == lang {
			result = append(result, opt)
		}
	}
	for _, opt := range all {
		if opt.lang != lang {
			result = append(result, opt)
		}
	}
	return result
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
	case "/files":
		return tr(lang, "slash.files")
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
	case "/diff":
		return tr(lang, "slash.diff")
	case "/hooks":
		return tr(lang, "slash.hooks")
	case "/cost":
		return tr(lang, "slash.cost")
	case "/review":
		return tr(lang, "slash.review")
	case "/copy":
		return tr(lang, "slash.copy")
	case "/context":
		return tr(lang, "slash.context")
	case "/inspector":
		return tr(lang, "slash.inspector")
	case "/chat":
		return tr(lang, "slash.chat")
	case "/nick":
		return tr(lang, "slash.nick")
	case "/stats":
		return tr(lang, "slash.stats")
	case "/tmux":
		return tr(lang, "slash.tmux")
	case "/share":
		return tr(lang, "slash.share")
	case "/unshare":
		return tr(lang, "slash.unshare")
	case "/tunnel":
		return tr(lang, "slash.tunnel")
	case "/retry":
		return tr(lang, "slash.retry")
	case "/edit":
		return tr(lang, "slash.edit")
	case "/regenerate", "/regen":
		return tr(lang, "slash.regenerate")
	case "/branch", "/fork":
		return tr(lang, "slash.branch")
	case "/reflect":
		return tr(lang, "slash.reflect")
	case "/rules":
		return tr(lang, "slash.rules")
	case "/cron":
		return tr(lang, "slash.cron")
	default:
		return cmd
	}
}

func tr(lang Language, key string, args ...any) string {
	var msg string
	switch lang {
	case LangZhCN:
		msg = zhCatalog(key)
	case LangJa:
		msg = jaCatalog(key)
	case LangKo:
		msg = koCatalog(key)
	case LangEs:
		msg = esCatalog(key)
	case LangFr:
		msg = frCatalog(key)
	case LangDe:
		msg = deCatalog(key)
	case LangRu:
		msg = ruCatalog(key)
	case LangPt:
		msg = ptCatalog(key)
	case LangVi:
		msg = viCatalog(key)
	default:
		msg = enCatalog(key)
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}
