package tui

func init() {
	registerCatalog(enCommandModule(), zhCommandModule())
}

func enCommandModule() map[string]string {
	return map[string]string{
		"slash.help":                            "Show help message",
		"slash.sessions":                        "List saved sessions",
		"slash.resume":                          "Resume a previous session",
		"slash.export":                          "Export session to markdown",
		"slash.model":                           "Switch model",
		"slash.provider":                        "Open provider manager",
		"slash.clear":                           "Clear conversation",
		"slash.mcp":                             "Show MCP servers",
		"slash.memory":                          "Manage memory",
		"slash.undo":                            "Undo last file edit",
		"slash.checkpoints":                     "List checkpoints",
		"slash.allow":                           "Always allow a tool",
		"slash.plugins":                         "List loaded plugins",
		"slash.image":                           "Attach an image",
		"slash.mode":                            "Set permission mode",
		"slash.init":                            "Generate project GGCODE.md",
		"slash.harness":                         "Run harness workflow commands",
		"slash.lang":                            "Switch interface language",
		"slash.skills":                          "Browse available skills",
		"slash.exit":                            "Exit ggcode",
		"slash.agents":                          "List sub-agents",
		"slash.agent":                           "Sub-agent details",
		"slash.compact":                         "Compress conversation history",
		"slash.todo":                            "View/manage todo list",
		"slash.bug":                             "Report a bug",
		"slash.config":                          "View/modify configuration",
		"slash.qq":                              "Manage QQ channel binding",
		"slash.tg":                              "Manage Telegram bot binding",
		"slash.telegram":                        "Manage Telegram bot binding",
		"slash.pc":                              "Manage PC channel binding",
		"slash.discord":                         "Manage Discord binding",
		"slash.feishu":                          "Manage Feishu binding",
		"slash.lark":                            "Manage Feishu binding",
		"slash.slack":                           "Manage Slack binding",
		"slash.dingtalk":                        "Manage DingTalk binding",
		"slash.ding":                            "Manage DingTalk binding",
		"slash.status":                          "Show current status",
		"slash.update":                          "Update ggcode",
		"slash.restart":                         "Restart ggcode",
		"command.unknown":                       "Unknown command: %s\n",
		"command.help_hint":                     "Type /help for available commands\n\n",
		"command.usage.allow":                   "Usage: /allow <tool-name>\n\n",
		"command.usage.resume":                  "Usage: /resume <session-id>\n\n",
		"command.usage.export":                  "Usage: /export <session-id>\n\n",
		"command.model_switched":                "Switched model to: %s (vendor: %s)\n\n",
		"command.model_failed":                  "Failed to switch model: %v\n\n",
		"command.model_current":                 "Current model: %s (vendor: %s)\nAvailable models: %s\nUse /model to open the model panel or /model <model-name> to switch directly.\n\n",
		"command.provider_unknown":              "Unknown vendor: %s (available: %v)\n\n",
		"command.provider_switched":             "Switched vendor to: %s (model: %s)\n\n",
		"command.provider_failed":               "Failed to update provider selection: %v\n\n",
		"command.provider_current":              "Current vendor: %s (endpoint: %s, model: %s)\nAvailable vendors: %s\nAvailable endpoints: %s\nUsage: /provider [vendor] [endpoint]\n\n",
		"command.allow_set":                     "✓ %s is now always allowed\n\n",
		"command.custom":                        "Custom command /%s:\n",
		"command.mention_error":                 "Mention expansion error: %v",
		"command.harness_usage":                 "Usage: /harness <init|check|queue|tasks|run|rerun|run-queued|monitor|contexts|inbox|review|promote|release|gc|doctor> ... (release supports rollouts|advance|pause|resume|abort|approve|reject)",
		"command.harness_queue_usage":           "Usage: /harness queue <goal>",
		"command.harness_run_usage":             "Usage: /harness run <goal>",
		"command.harness_rerun_usage":           "Usage: /harness rerun <task-id>",
		"command.skill_agent_only":              "Skill %s can only be invoked by the agent.",
		"command.harness_owner_promoted":        "Promoted %d harness task(s) for owner %s.",
		"command.harness_review_approved":       "Approved harness task %s.",
		"command.harness_review_rejected":       "Rejected harness task %s.",
		"command.harness_promoted_many":         "Promoted %d harness task(s).",
		"command.harness_promoted_one":          "Promoted harness task %s.",
		"command.harness_task_queued_detail":    "Queued harness task %s.\n- goal: %s",
		"command.harness_tasks_empty":           "No harness tasks recorded.",
		"command.harness_run_start":             "Starting tracked harness run...\nUse /harness monitor or the Tasks/Monitor views for live state.",
		"command.harness_rerun_start":           "Starting tracked harness rerun...\nUse /harness monitor or the Tasks/Monitor views for live state.",
		"command.harness_rerun_invalid_status":  "Harness task %s is %s; only failed tasks can be rerun.",
		"command.harness_status_starting_run":   "Starting harness run...",
		"command.harness_status_starting_rerun": "Starting harness rerun...",
		"command.harness_spinner_running":       "Running harness",
		"command.harness_cancelled":             "Harness run cancelled.",
		"init.resolve_failed":                   "Failed to resolve init target: %v\n\n",
		"init.generate_failed":                  "Failed to generate GGCODE.md content: %v\n\n",
		"init.collecting":                       "Collecting project knowledge...",
		"help.text": `Available commands:
  /help, /?          Show this help message
  /sessions          List all saved sessions
  /resume <id>       Resume a previous session
  /export <id>       Export session to markdown file
  /model [name]      Open model panel or switch directly
  /provider [vendor] Open provider manager
  /qq                Open QQ binding panel
  /tg, /telegram     Open Telegram binding panel
  /pc                Open PC channel binding panel
  /discord           Open Discord binding panel
  /feishu, /lark     Open Feishu binding panel
  /slack             Open Slack binding panel
  /dingtalk, /ding   Open DingTalk binding panel
  /lang [code]       Choose or switch interface language
  /skills            Browse available skills
  /clear             Clear conversation history
  /mcp               Show connected MCP servers and tools
  /memory            Show loaded memory files
  /memory list       List auto memory entries
  /memory clear      Clear all auto memories
  /undo              Undo the last file edit (checkpoint rollback)
  /checkpoints       List all file edit checkpoints

  /allow <tool>      Always allow a specific tool
  /plugins           List loaded plugins and their tools
  /image <path>      Attach an image file
  /mode <mode>       Set agent mode (supervised|plan|auto|bypass|autopilot)
  /init              Generate GGCODE.md from the current project
  /harness ...       Run harness control-plane commands
  /agents            List sub-agents
  /agent <id>        Show sub-agent details
  /agent cancel <id> Cancel a sub-agent

  /compact           Compress conversation history
  /todo              View todo list
  /todo clear        Clear todo list
  /bug               Report a bug with diagnostics
  /config            Show current configuration
  /config set <k> <v> Set a config value
  /status            Show current status
  /update            Update ggcode to the latest release
  /exit, /quit       Exit ggcode

Keyboard shortcuts:
  Tab                Cycle autocomplete or approval choices
  Shift+Tab          Reverse cycle autocomplete, otherwise toggle permission mode
  Enter              Send message / apply current selection
  Esc                Cancel autocomplete / exit idle shell mode
  ↑/↓                 Browse command history (or autocomplete)
  PgUp/PgDn          Scroll conversation output
  Ctrl+C             Cancel current activity, otherwise clear input then press again to exit
  Ctrl+D             Exit immediately
  $ / !              Enter shell mode

Mouse:
  Option+drag / Shift+drag  Select text to copy (bypasses app mouse capture)
  Mouse wheel               Scroll conversation output`,
	}
}

func zhCommandModule() map[string]string {
	return map[string]string{
		"slash.help":                            "显示帮助",
		"slash.sessions":                        "列出已保存会话",
		"slash.resume":                          "恢复历史会话",
		"slash.export":                          "导出会话为 Markdown",
		"slash.model":                           "切换模型",
		"slash.provider":                        "打开供应商管理界面",
		"slash.clear":                           "清空对话",
		"slash.mcp":                             "显示 MCP 服务器",
		"slash.memory":                          "管理记忆",
		"slash.undo":                            "撤销最近一次文件修改",
		"slash.checkpoints":                     "列出检查点",
		"slash.allow":                           "永久允许某个工具",
		"slash.plugins":                         "列出已加载插件",
		"slash.image":                           "附加图片",
		"slash.mode":                            "设置权限模式",
		"slash.init":                            "生成项目 GGCODE.md",
		"slash.harness":                         "运行 harness 工作流命令",
		"slash.lang":                            "切换界面语言",
		"slash.skills":                          "浏览可用 skills",
		"slash.exit":                            "退出 ggcode",
		"slash.agents":                          "列出子 Agent",
		"slash.agent":                           "查看子 Agent 详情",
		"slash.compact":                         "压缩对话历史",
		"slash.todo":                            "查看/管理 todo",
		"slash.bug":                             "报告 bug",
		"slash.config":                          "查看/修改配置",
		"slash.qq":                              "管理 QQ 渠道绑定",
		"slash.tg":                              "管理 Telegram 机器人绑定",
		"slash.telegram":                        "管理 Telegram 机器人绑定",
		"slash.pc":                              "管理 PC 渠道绑定",
		"slash.discord":                         "管理 Discord 绑定",
		"slash.feishu":                          "管理飞书绑定",
		"slash.lark":                            "管理飞书绑定",
		"slash.slack":                           "管理 Slack 绑定",
		"slash.dingtalk":                        "管理钉钉绑定",
		"slash.ding":                            "管理钉钉绑定",
		"slash.status":                          "显示当前状态",
		"slash.update":                          "升级 ggcode",
		"slash.restart":                         "重启 ggcode",
		"command.unknown":                       "未知命令：%s\n",
		"command.help_hint":                     "输入 /help 查看可用命令\n\n",
		"command.usage.allow":                   "用法：/allow <tool-name>\n\n",
		"command.usage.resume":                  "用法：/resume <session-id>\n\n",
		"command.usage.export":                  "用法：/export <session-id>\n\n",
		"command.model_switched":                "已切换模型为：%s（供应商：%s）\n\n",
		"command.model_failed":                  "切换模型失败：%v\n\n",
		"command.model_current":                 "当前模型：%s（供应商：%s）\n可用模型：%s\n使用 /model 打开模型面板，或用 /model <model-name> 直接切换。\n\n",
		"command.provider_unknown":              "未知供应商：%s（可用：%v）\n\n",
		"command.provider_switched":             "已切换供应商为：%s（模型：%s）\n\n",
		"command.provider_failed":               "更新供应商选择失败：%v\n\n",
		"command.provider_current":              "当前供应商：%s（接口：%s，模型：%s）\n可用供应商：%s\n可用接口：%s\n用法：/provider [vendor] [endpoint]\n\n",
		"command.allow_set":                     "✓ %s 已设为永久允许\n\n",
		"command.custom":                        "自定义命令 /%s：\n",
		"command.mention_error":                 "展开 @ 引用失败：%v",
		"command.harness_usage":                 "用法：/harness <init|check|queue|tasks|run|rerun|run-queued|monitor|contexts|inbox|review|promote|release|gc|doctor> ...（release 支持 rollouts|advance|pause|resume|abort|approve|reject）",
		"command.harness_queue_usage":           "用法：/harness queue <goal>",
		"command.harness_run_usage":             "用法：/harness run <goal>",
		"command.harness_rerun_usage":           "用法：/harness rerun <task-id>",
		"command.skill_agent_only":              "技能 %s 只能由 agent 调用。",
		"command.harness_owner_promoted":        "已为 owner %[2]s 推进 %[1]d 个 harness 任务。",
		"command.harness_review_approved":       "已批准 harness 任务 %s。",
		"command.harness_review_rejected":       "已拒绝 harness 任务 %s。",
		"command.harness_promoted_many":         "已推进 %d 个 harness 任务。",
		"command.harness_promoted_one":          "已推进 harness 任务 %s。",
		"command.harness_task_queued_detail":    "已加入 harness 队列：%s。\n- 目标：%s",
		"command.harness_tasks_empty":           "还没有记录任何 harness 任务。",
		"command.harness_run_start":             "正在启动跟踪型 harness 运行...\n可使用 /harness monitor 或 Tasks/Monitor 视图查看实时状态。",
		"command.harness_rerun_start":           "正在启动跟踪型 harness 重跑...\n可使用 /harness monitor 或 Tasks/Monitor 视图查看实时状态。",
		"command.harness_rerun_invalid_status":  "Harness 任务 %s 当前是 %s；只有失败任务才能重跑。",
		"command.harness_status_starting_run":   "正在启动 harness 运行...",
		"command.harness_status_starting_rerun": "正在启动 harness 重跑...",
		"command.harness_spinner_running":       "正在运行 harness",
		"command.harness_cancelled":             "Harness 运行已取消。",
		"init.resolve_failed":                   "解析初始化目标失败：%v\n\n",
		"init.generate_failed":                  "生成 GGCODE.md 内容失败：%v\n\n",
		"init.collecting":                       "正在收集项目知识...",
		"help.text": `可用命令：
  /help, /?          显示帮助
  /sessions          列出已保存会话
  /resume <id>       恢复历史会话
  /export <id>       导出会话为 Markdown 文件
  /model [name]      打开模型面板或直接切换
  /provider [vendor] 打开供应商管理界面
  /qq                打开 QQ 绑定面板
  /tg, /telegram     打开 Telegram 绑定面板
  /pc                打开 PC 渠道绑定面板
  /discord           打开 Discord 绑定面板
  /feishu, /lark     打开飞书绑定面板
  /slack             打开 Slack 绑定面板
  /dingtalk, /ding   打开钉钉绑定面板
  /lang [code]       选择或切换界面语言
  /skills            浏览可用 skills
  /clear             清空对话历史
  /mcp               显示已连接的 MCP 服务器和工具
  /memory            显示已加载记忆
  /memory list       列出自动记忆条目
  /memory clear      清空自动记忆
  /undo              撤销最近一次文件修改（回滚检查点）
  /checkpoints       列出所有文件修改检查点

  /allow <tool>      永久允许某个工具
  /plugins           列出已加载插件及其工具
  /image <path>      附加图片文件
  /mode <mode>       设置运行模式（supervised|plan|auto|bypass|autopilot）
  /init              基于当前项目生成 GGCODE.md
  /harness ...       运行 harness 控制面命令
  /agents            列出子 Agent
  /agent <id>        查看子 Agent 详情
  /agent cancel <id> 取消子 Agent

  /compact           压缩对话历史
  /todo              查看 todo 列表
  /todo clear        清空 todo 列表
  /bug               生成 bug 诊断信息
  /config            显示当前配置
  /config set <k> <v> 设置配置项
  /status            显示当前状态
  /update            升级到最新正式版本
  /exit, /quit       退出 ggcode

键盘快捷键：
  Tab                在补全或确认选项中切换
  Shift+Tab          反向切换补全，否则切换权限模式
  Enter              发送消息 / 应用当前选择
  Esc                取消补全 / 在空闲 shell 模式下退出命令模式
  ↑/↓                 浏览命令历史（或补全）
  PgUp/PgDn          滚动对话输出
  Ctrl+C             取消当前活动；空闲时先清空输入，再次按下退出
  Ctrl+D             立即退出
  $ / !              进入 shell 模式

鼠标：
  Option+拖拽 / Shift+拖拽  选择文本复制（绕过应用鼠标捕获）
  鼠标滚轮                 滚动对话输出`,
	}
}
