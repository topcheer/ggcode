package main

// Linear-frontend i18n (#4604): every user-visible string of the plain REPL
// goes through plainTr. Two catalogs are maintained (en, zh-CN) keyed by
// tui.Language; other configured languages fall back to English. The catalog
// lives here (not in internal/tui's 1300-line table) so the linear frontend
// stays self-contained and its strings can be audited separately.

import (
	"strings"

	"github.com/topcheer/ggcode/internal/config"

	"github.com/topcheer/ggcode/internal/tui"
)

var plainCatalogs = map[tui.Language]map[string]string{
	tui.LangEnglish: {
		"banner":             "ggcode linear frontend (no TUI) - /help for commands, Ctrl-D to exit",
		"askuser.off":        "[plain] ask_user disabled - the agent ends its turn and you answer by typing the next line",
		"notice":             "[notice]",
		"prompt":             "ggcode> ",
		"help.title":         "commands:",
		"help.status":        "  /status                       show active vendor/endpoint/model",
		"help.model":         "  /model <name>                 hot-switch model (persists)",
		"help.provider":      "  /provider <vendor> [ep]       switch vendor (optionally endpoint)",
		"help.plist":         "  /provider --list [--all]      list endpoints+models (default: keyed+modelled only)",
		"help.padd":          "  /provider --add-endpoint ...  add endpoint to active vendor",
		"help.endpoint":      "  /endpoint <id>                switch endpoint on current vendor",
		"help.emodels":       "  /endpoint --show-models       all models on the active endpoint",
		"help.erefresh":      "  /endpoint --refresh-models    force model refetch, then show",
		"help.eadd":          "  /endpoint --add-model ...     add a model to the active endpoint",
		"help.ekey":          "  /endpoint --set-apikey ...    set the API key on the active endpoint",
		"help.lang":          "  /lang <en|zh-CN>              switch interface language (persists)",
		"help.exit":          "  /exit                         leave the REPL (also Ctrl-D)",
		"help.tail":          "anything else is sent to the agent; approvals default to Accept",
		"help.tail2":         "(GGCODE_PLAIN_APPROVE=deny rejects instead); ask_user is disabled.",
		"cfg.unavailable":    "[plain] config unavailable",
		"resolve.failed":     "[plain] resolve failed:",
		"switch.failed":      "[plain] switch failed:",
		"switch.nopersist":   "[plain] switch applied but not persisted:",
		"switch.ok":          "[plain] now on vendor=%s endpoint=%s model=%s",
		"usage.model":        "usage: /model <name>",
		"usage.provider":     "usage: /provider <vendor> [endpoint] | --list [--all] | --add-endpoint ...",
		"usage.endpoint":     "usage: /endpoint <id> | --show-models | --refresh-models | --add-model ... | --set-apikey ...",
		"cmd.unknown":        "[plain] unknown command:",
		"opt.unknown":        "[plain] unknown /provider option:",
		"opt.unknown.ep":     "[plain] unknown /endpoint option:",
		"flags.bad":          "[plain] bad flags:",
		"lang.usage":         "usage: /lang <en|zh-CN>",
		"lang.unknown":       "[plain] unsupported language:",
		"lang.switched":      "[plain] language switched to %s (persisted)",
		"lang.nopersist":     "[plain] language switched but not persisted:",
		"add.req":            "[plain] --endpoint-name/--endpoint-type/--endpoint-url are required",
		"add.proto":          "[plain] unsupported endpoint-type %q (want openai|openai-responses|anthropic|gemini|copilot)",
		"add.novendor":       "[plain] no active vendor to add the endpoint to",
		"add.duplicate":      "[plain] endpoint %q already exists on vendor %q",
		"add.ok":             "[plain] endpoint %q added to vendor %q%s",
		"add.switchhint":     " - switch with: /endpoint ",
		"list.vendor":        "vendor %s:",
		"list.noendpoints":   "vendor %s: (no endpoints)",
		"list.nokey":         "  (no api key)",
		"list.nomodels":      "  (no models configured)",
		"list.more":          "    (+%d more - use /endpoint --show-models)",
		"models.title":       "models on %s/%s:",
		"models.discovered":  "(discovered)",
		"models.none":        "  (none - use /endpoint --add-model --model <id>)",
		"models.nodiscovery": "[plain] model discovery unavailable (showing configured list):",
		"addmodel.req":       "[plain] --model is required",
		"addmodel.noactive":  "[plain] no active endpoint to add the model to",
		"addmodel.nofound":   "[plain] active endpoint %q not found on vendor %q",
		"addmodel.duplicate": "[plain] model %q already on endpoint %q",
		"addmodel.ok":        "[plain] model %q added to endpoint %q - switch with: /model %s",
		"setkey.req":         "[plain] --apikey is required",
		"setkey.noactive":    "[plain] no active endpoint to set the key on",
		"setkey.nofound":     "[plain] active endpoint %q not found on vendor %q",
		"setkey.ok":          "[plain] api key set on endpoint %q",
	},
	tui.LangZhCN: {
		"banner":             "ggcode 线性前端（无 TUI）- /help 查看命令，Ctrl-D 退出",
		"askuser.off":        "[plain] ask_user 已禁用 - 模型结束回合后，输入下一行即是你的回答",
		"notice":             "[提示]",
		"prompt":             "ggcode> ",
		"help.title":         "命令：",
		"help.status":        "  /status                       查看当前 vendor/endpoint/模型",
		"help.model":         "  /model <名称>                 热切换模型（持久化）",
		"help.provider":      "  /provider <vendor> [ep]       切换 vendor（可选 endpoint）",
		"help.plist":         "  /provider --list [--all]      列出 endpoint 与模型（默认仅显示已配 key 且有模型的）",
		"help.padd":          "  /provider --add-endpoint ...  向当前 vendor 添加 endpoint",
		"help.endpoint":      "  /endpoint <id>                在当前 vendor 内切换 endpoint",
		"help.emodels":       "  /endpoint --show-models       当前 endpoint 的全部模型",
		"help.erefresh":      "  /endpoint --refresh-models    强制刷新模型列表后显示",
		"help.eadd":          "  /endpoint --add-model ...     向当前 endpoint 添加模型",
		"help.ekey":          "  /endpoint --set-apikey ...    设置当前 endpoint 的 API key",
		"help.lang":          "  /lang <en|zh-CN>              切换界面语言（持久化）",
		"help.exit":          "  /exit                         退出（也可 Ctrl-D）",
		"help.tail":          "其余输入直接发给模型；审批默认接受",
		"help.tail2":         "（GGCODE_PLAIN_APPROVE=deny 可改为拒绝）；ask_user 已禁用。",
		"cfg.unavailable":    "[plain] 配置不可用",
		"resolve.failed":     "[plain] 解析失败：",
		"switch.failed":      "[plain] 切换失败：",
		"switch.nopersist":   "[plain] 已切换但未持久化：",
		"switch.ok":          "[plain] 当前 vendor=%s endpoint=%s 模型=%s",
		"usage.model":        "用法：/model <名称>",
		"usage.provider":     "用法：/provider <vendor> [endpoint] | --list [--all] | --add-endpoint ...",
		"usage.endpoint":     "用法：/endpoint <id> | --show-models | --refresh-models | --add-model ... | --set-apikey ...",
		"cmd.unknown":        "[plain] 未知命令：",
		"opt.unknown":        "[plain] 未知的 /provider 选项：",
		"opt.unknown.ep":     "[plain] 未知的 /endpoint 选项：",
		"flags.bad":          "[plain] 参数错误：",
		"lang.usage":         "用法：/lang <en|zh-CN>",
		"lang.unknown":       "[plain] 不支持的语言：",
		"lang.switched":      "[plain] 已切换语言为 %s（已持久化）",
		"lang.nopersist":     "[plain] 已切换但未持久化：",
		"add.req":            "[plain] --endpoint-name/--endpoint-type/--endpoint-url 为必填",
		"add.proto":          "[plain] 不支持的 endpoint-type %q（应为 openai|openai-responses|anthropic|gemini|copilot）",
		"add.novendor":       "[plain] 没有活动 vendor 可添加 endpoint",
		"add.duplicate":      "[plain] endpoint %q 已存在于 vendor %q",
		"add.ok":             "[plain] endpoint %q 已添加到 vendor %q%s",
		"add.switchhint":     " - 切换命令：/endpoint ",
		"list.vendor":        "vendor %s：",
		"list.noendpoints":   "vendor %s：（无 endpoint）",
		"list.nokey":         "  （未配 api key）",
		"list.nomodels":      "  （无已配置模型）",
		"list.more":          "    （还有 %d 个 - 用 /endpoint --show-models 查看）",
		"models.title":       "%s/%s 上的模型：",
		"models.discovered":  "（在线发现）",
		"models.none":        "  （暂无 - 用 /endpoint --add-model --model <id> 添加）",
		"models.nodiscovery": "[plain] 模型发现不可用（显示已配置列表）：",
		"addmodel.req":       "[plain] 必须指定 --model",
		"addmodel.noactive":  "[plain] 没有活动 endpoint 可添加模型",
		"addmodel.nofound":   "[plain] 活动 endpoint %q 不在 vendor %q 上",
		"addmodel.duplicate": "[plain] 模型 %q 已在 endpoint %q 上",
		"addmodel.ok":        "[plain] 模型 %q 已添加到 endpoint %q - 切换命令：/model %s",
		"setkey.req":         "[plain] 必须指定 --apikey",
		"setkey.noactive":    "[plain] 没有活动 endpoint 可设置 key",
		"setkey.nofound":     "[plain] 活动 endpoint %q 不在 vendor %q 上",
		"setkey.ok":          "[plain] 已设置 endpoint %q 的 api key",
	},
}

// plainLang is the linear frontend's active language: initialized from
// cfg.Language at startup, hot-switched by /lang (which also persists to
// cfg.Language so TUI/pipe sessions inherit the choice).
var plainLang = tui.LangEnglish

// plainTr looks up key in the active language catalog, falling back to
// English, then to the key itself (missing translation should never blank
// the output).
func plainTr(lang tui.Language, key string) string {
	if c, ok := plainCatalogs[lang]; ok {
		if v, ok := c[key]; ok {
			return v
		}
	}
	if v, ok := plainCatalogs[tui.LangEnglish][key]; ok {
		return v
	}
	return key
}

// plainLangFromConfig derives the frontend language from cfg.Language,
// defaulting to English (same convention as the TUI's startup path).
func plainLangFromConfig(cfgLanguage string) tui.Language {
	if strings.TrimSpace(cfgLanguage) == "" {
		return tui.LangEnglish
	}
	return tui.NormalizeLanguage(cfgLanguage)
}

// cfgLang extracts cfg.Language nil-safely.
func cfgLang(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Language
}
