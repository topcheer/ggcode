package webui

import (
	"strings"
)

// panelSpec 描述一个 webui 面板:当 TUI 面板命令(无参形态)经
// /api/chat/commands 提交时,不把命令送进 TUI bubbletea 环(那会在
// TUI 屏幕弹面板而 webui 用户不可见),而是返回面板指令让前端渲染
// 对应的 webui 面板;用户在面板里的选择最终回填成带参命令二次提交,
// 走原有 SubmitCommand 直通链(校验/反馈/保存语义单源不变)。
type panelSpec struct {
	Kind    string `json:"kind"`    // select | list-detail | form | stats
	Title   string `json:"title"`   // 面板标题
	DataURL string `json:"dataUrl"` // GET 数据端点(既有 API,形状由前端 adapter 归一)
	// CmdPrefix: 回填命令前缀(如 "/model"),前端拼 `/model <selection>`。
	CmdPrefix string `json:"cmdPrefix,omitempty"`
	// NoArg: true=该命令只有面板形态(任何提交都返回面板指令);
	// false=有直通形态,仅无参时返回面板指令(带参仍走命令链)。
	NoArg bool `json:"noArg,omitempty"`
}

// panelRegistry: TUI 面板命令全集 → webui 面板原型映射。
// 数据端点全部复用既有 /api/* handler(server.go 注册面),本层零新数据面;
// 缺口数据端点(plugins/memory/tags/stats/usage)由 server_panels_data.go 补齐。
var panelRegistry = map[string]panelSpec{
	// ── 原型 select:单选/级联,选项源=config 域 ──────────────────
	"model":       {Kind: "select", Title: "Model", DataURL: "/api/panel/model", CmdPrefix: "/model"},
	"provider":    {Kind: "select", Title: "Provider", DataURL: "/api/vendors", CmdPrefix: "/provider"},
	"impersonate": {Kind: "select", Title: "Impersonate", DataURL: "/api/impersonate", CmdPrefix: "/impersonate", NoArg: true},
	"effort":      {Kind: "select", Title: "Reasoning Effort", DataURL: "/api/chat/controls", CmdPrefix: "/effort"},
	"mode":        {Kind: "select", Title: "Permission Mode", DataURL: "/api/chat/controls", CmdPrefix: "/mode"},
	"lang":        {Kind: "select", Title: "Language", DataURL: "/api/panel/lang", CmdPrefix: "/lang"},

	// ── 原型 list-detail:列表+详情+行内动作 ─────────────────────
	"sessions":  {Kind: "list-detail", Title: "Sessions", DataURL: "/api/sessions", CmdPrefix: "/resume", NoArg: true},
	"resume":    {Kind: "list-detail", Title: "Resume Session", DataURL: "/api/sessions", CmdPrefix: "/resume", NoArg: true},
	"skills":    {Kind: "list-detail", Title: "Skills", DataURL: "/api/knight/skills", CmdPrefix: "/skill", NoArg: true},
	"plugins":   {Kind: "list-detail", Title: "Plugins", DataURL: "/api/panel/plugins", CmdPrefix: "", NoArg: true},
	"memory":    {Kind: "list-detail", Title: "Memory", DataURL: "/api/panel/memory", CmdPrefix: "", NoArg: true},
	"tags":      {Kind: "list-detail", Title: "Tags", DataURL: "/api/panel/tags", CmdPrefix: "", NoArg: true},
	"cron":      {Kind: "list-detail", Title: "Scheduled Jobs", DataURL: "/api/cron/jobs", CmdPrefix: "/cron", NoArg: true},
	"mcp":       {Kind: "list-detail", Title: "MCP Servers", DataURL: "/api/mcp", CmdPrefix: "/mcp", NoArg: true},
	"inspector": {Kind: "list-detail", Title: "Inspector", DataURL: "/api/sessions", CmdPrefix: "", NoArg: true},

	// ── 原型 form:IM 适配器配置(15 家) ─────────────────────────
	"qq":         {Kind: "form", Title: "QQ", DataURL: "/api/im/adapters/qq", CmdPrefix: "/qq", NoArg: true},
	"pc":         {Kind: "form", Title: "PC (PushChannel)", DataURL: "/api/im/adapters/pc", CmdPrefix: "/pc", NoArg: true},
	"telegram":   {Kind: "form", Title: "Telegram", DataURL: "/api/im/adapters/telegram", CmdPrefix: "/telegram", NoArg: true},
	"tg":         {Kind: "form", Title: "Telegram", DataURL: "/api/im/adapters/telegram", CmdPrefix: "/telegram", NoArg: true},
	"discord":    {Kind: "form", Title: "Discord", DataURL: "/api/im/adapters/discord", CmdPrefix: "/discord", NoArg: true},
	"feishu":     {Kind: "form", Title: "Feishu", DataURL: "/api/im/adapters/feishu", CmdPrefix: "/feishu", NoArg: true},
	"lark":       {Kind: "form", Title: "Feishu", DataURL: "/api/im/adapters/feishu", CmdPrefix: "/feishu", NoArg: true},
	"slack":      {Kind: "form", Title: "Slack", DataURL: "/api/im/adapters/slack", CmdPrefix: "/slack", NoArg: true},
	"dingtalk":   {Kind: "form", Title: "DingTalk", DataURL: "/api/im/adapters/dingtalk", CmdPrefix: "/dingtalk", NoArg: true},
	"ding":       {Kind: "form", Title: "DingTalk", DataURL: "/api/im/adapters/dingtalk", CmdPrefix: "/dingtalk", NoArg: true},
	"wechat":     {Kind: "form", Title: "WeChat", DataURL: "/api/im/adapters/wechat", CmdPrefix: "/wechat", NoArg: true},
	"wecom":      {Kind: "form", Title: "WeCom", DataURL: "/api/im/adapters/wecom", CmdPrefix: "/wecom", NoArg: true},
	"mattermost": {Kind: "form", Title: "Mattermost", DataURL: "/api/im/adapters/mattermost", CmdPrefix: "/mattermost", NoArg: true},
	"mm":         {Kind: "form", Title: "Mattermost", DataURL: "/api/im/adapters/mattermost", CmdPrefix: "/mattermost", NoArg: true},
	"matrix":     {Kind: "form", Title: "Matrix", DataURL: "/api/im/adapters/matrix", CmdPrefix: "/matrix", NoArg: true},
	"signal":     {Kind: "form", Title: "Signal", DataURL: "/api/im/adapters/signal", CmdPrefix: "/signal", NoArg: true},
	"nostr":      {Kind: "form", Title: "Nostr", DataURL: "/api/im/adapters/nostr", CmdPrefix: "/nostr", NoArg: true},
	"whatsapp":   {Kind: "form", Title: "WhatsApp", DataURL: "/api/im/adapters/whatsapp", CmdPrefix: "/whatsapp", NoArg: true},
	"wa":         {Kind: "form", Title: "WhatsApp", DataURL: "/api/im/adapters/whatsapp", CmdPrefix: "/whatsapp", NoArg: true},
	"twitch":     {Kind: "form", Title: "Twitch", DataURL: "/api/im/adapters/twitch", CmdPrefix: "/twitch", NoArg: true},
	"im":         {Kind: "form", Title: "IM Adapters", DataURL: "/api/im/adapters", CmdPrefix: "", NoArg: true},

	// ── 原型 stats:只读仪表 ────────────────────────────────────
	"stats": {Kind: "stats", Title: "Session Stats", DataURL: "/api/panel/stats", CmdPrefix: "", NoArg: true},
	"usage": {Kind: "stats", Title: "Usage", DataURL: "/api/panel/usage", CmdPrefix: "", NoArg: true},

	// ── 特例:lanchat(/chat)不进面板注册表——它是双向交互对话面板而非
	// 一次性选择,不适用"选择回填命令"语义;走直通链(TUI 侧打开面板,
	// webui 用户经 WS 系统消息获知)。webui 原生 LAN 聊天界面属独立特性。
}

// panelDirectiveFor 判定一条 slash 命令是否应走面板指令而非直通:
// 命中注册表且(NoArg 或无参形态)→ 返回 spec;否则 nil(带参直通)。
func panelDirectiveFor(text string) (panelSpec, string, bool) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "/") {
		return panelSpec{}, "", false
	}
	fields := strings.Fields(t)
	if len(fields) == 0 {
		return panelSpec{}, "", false
	}
	base := strings.TrimPrefix(fields[0], "/")
	spec, ok := panelRegistry[base]
	if !ok {
		return panelSpec{}, "", false
	}
	// 面板指令仅在无参形态(单字段);带参命令(/model x、/resume <id>)
	// 一律直通命令链——参数校验与执行语义单源在 TUI 命令层。
	if len(fields) == 1 {
		return spec, base, true
	}
	return panelSpec{}, "", false
}

// registerPanelRoutes 挂 /api/panel/* 缺口数据端点(既有端点之外的面板数据)。
func (s *Server) registerPanelRoutes() {
	s.mux.HandleFunc("/api/panel/", s.handlePanelData)
}

// SetPanelDataFn 注入面板数据供给面(root 装配处以闭包提供,最小提层:
// 数据源实例(自动记忆/会话仓/运行态)已在 root 作用域,取数逻辑不复制)。
// 命中后对应 /api/panel/<name> 返回真实数据;未注入的面板维持 501。
func (s *Server) SetPanelDataFn(name string, fn func() any) {
	if s.panelDataFns == nil {
		s.panelDataFns = map[string]func() any{}
	}
	s.panelDataFns[name] = fn
}
