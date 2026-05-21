package tui

func zhCatalog(key string) string {
	switch key {
	case "workspace.tagline":
		return "极客 AI 工作台"
	case "header.terminal_native":
		return "终端原生 AI 编码"
	case "session.ephemeral":
		return "临时会话"
	case "agents.idle":
		return "空闲"
	case "agents.running":
		return "%d 个运行中"
	case "activity.idle":
		return "空闲"
	case "panel.conversation":
		return "对话"
	case "panel.composer":
		return "输入"
	case "panel.composer_locked":
		return "输入已锁定"
	case "panel.commands":
		return "命令："
	case "panel.files":
		return "文件："
	case "panel.agent_status":
		return "Agent 状态"
	case "panel.mode_policy":
		return "模式说明"
	case "panel.context":
		return "上下文"
	case "panel.mcp":
		return "MCP"
	case "panel.mcp.install_spec_required":
		return "请先输入安装规格。"
	case "panel.mcp.installing_server":
		return "正在安装 MCP 服务..."
	case "panel.mcp.reconnect_unavailable":
		return "当前会话不支持重新连接。"
	case "panel.mcp.reconnecting":
		return "正在重新连接 %s..."
	case "panel.mcp.reconnect_failed":
		return "无法重新连接 %s。"
	case "panel.mcp.installing_browser_preset":
		return "正在安装浏览器自动化 MCP 预设..."
	case "panel.mcp.uninstalling":
		return "正在卸载 %s..."
	case "panel.startup":
		return "正在初始化"
	case "panel.approval_required":
		return "需要确认"
	case "panel.bypass_approval":
		return "Bypass 模式确认"
	case "panel.review_file_change":
		return "审阅文件修改"
	case "panel.im":
		return "IM"
	case "label.vendor":
		return "供应商"
	case "label.endpoint":
		return "接入点"
	case "label.model":
		return "模型"
	case "label.mode":
		return "模式"
	case "label.session":
		return "会话"
	case "label.agents":
		return "子 Agent"
	case "label.cwd":
		return "目录"
	case "label.branch":
		return "分支"
	case "label.context":
		return "上下文"
	case "label.skills":
		return "技能"
	case "label.activity":
		return "活动"
	case "label.window":
		return "窗口"
	case "label.usage":
		return "占用"
	case "label.compact":
		return "压缩"
	case "label.approval_policy":
		return "审批"
	case "label.tool_policy":
		return "工具"
	case "label.agent_policy":
		return "行为"
	case "label.tool":
		return "工具"
	case "label.input":
		return "输入"
	case "label.file":
		return "文件"
	case "label.directory":
		return "目录"
	case "context.unavailable":
		return "暂无上下文数据"
	case "im.none":
		return "未配置适配器"
	case "im.summary":
		return "%d 个适配器 • %d 个健康"
	case "im.more":
		return "还有 %d 个（/qq）"
	case "im.runtime.available":
		return "运行时可用"
	case "im.runtime.disabled":
		return "已禁用"
	case "im.runtime.not_started":
		return "已启用 • 重启后初始化"
	case "im.status.not_started":
		return "未启动"
	case "context.until_compact":
		return "后触发"
	case "empty.ask":
		return "你可以让我做重构、修复 bug、解释代码或补测试。"
	case "empty.tips":
		return "提示：用 @path 引用文件，/? 查看帮助，Shift+Tab 切换模式。"
	case "startup.banner":
		return "正在准备终端界面并过滤启动期的终端噪声。你现在就可以输入；一旦界面进入可交互状态，这个提示会自动消失。"
	case "harness.views":
		return "视图"
	case "harness.items":
		return "条目"
	case "harness.action":
		return "操作"
	case "harness.details":
		return "详情"
	case "harness.none":
		return "（无）"
	case "harness.unknown":
		return "未知"
	case "harness.unscoped":
		return "未归属"
	case "harness.unavailable":
		return "Harness 不可用"
	case "harness.unavailable_intro":
		return "已有项目可从这里开始："
	case "harness.unavailable_step_init":
		return "  1. 按 Enter 或 i 初始化 harness"
	case "harness.unavailable_step_refresh":
		return "  2. 初始化完成后按 r 刷新"
	case "harness.section.init":
		return "初始化"
	case "harness.section.check":
		return "检查"
	case "harness.section.doctor":
		return "诊断"
	case "harness.section.monitor":
		return "监控"
	case "harness.section.gc":
		return "清理"
	case "harness.section.contexts":
		return "上下文"
	case "harness.section.tasks":
		return "任务"
	case "harness.section.queue":
		return "排队"
	case "harness.section.run":
		return "运行"
	case "harness.section.run_queued":
		return "运行队列"
	case "harness.section.inbox":
		return "收件箱"
	case "harness.section.review":
		return "评审"
	case "harness.section.promote":
		return "晋升"
	case "harness.section.release":
		return "发布"
	case "harness.section.rollouts":
		return "发布波次"
	case "harness.hints.unavailable":
		return "Enter/i 初始化 • r 刷新 • Esc 关闭"
	case "harness.hints.move":
		return "j/k 移动"
	case "harness.hints.tab":
		return "Tab 切换"
	case "harness.hints.refresh":
		return "r 刷新"
	case "harness.hints.close":
		return "Esc 关闭"
	case "harness.hints.check":
		return "Enter 运行检查"
	case "harness.hints.monitor":
		return "Enter 刷新快照"
	case "harness.hints.gc":
		return "Enter 执行清理"
	case "harness.hints.type_goal":
		return "输入目标"
	case "harness.hints.queue":
		return "Enter 排队"
	case "harness.hints.run":
		return "Enter 运行"
	case "harness.hints.focus_input":
		return "Tab 聚焦输入"
	case "harness.hints.rerun":
		return "Enter 重跑失败任务"
	case "harness.hints.next":
		return "Enter 跑下一个"
	case "harness.hints.all":
		return "a 全部"
	case "harness.hints.retry_failed":
		return "f 重试失败"
	case "harness.hints.resume":
		return "s 恢复"
	case "harness.hints.promote_owner":
		return "p 晋升该 owner"
	case "harness.hints.retry_owner":
		return "f 重试该 owner"
	case "harness.hints.approve":
		return "Enter 通过"
	case "harness.hints.reject":
		return "x 拒绝"
	case "harness.hints.promote":
		return "Enter 晋升"
	case "harness.hints.apply_batch":
		return "Enter 应用批次"
	case "harness.hints.advance":
		return "Enter 推进"
	case "harness.hints.approve_gate":
		return "g 批准 gate"
	case "harness.hints.pause_resume":
		return "p 暂停/恢复"
	case "harness.hints.abort":
		return "x 中止"
	case "harness.hint.primary.check":
		return "按 Enter 运行检查。"
	case "harness.hint.primary.monitor":
		return "按 Enter 刷新监控快照。"
	case "harness.hint.primary.gc":
		return "按 Enter 运行垃圾清理。"
	case "harness.hint.primary.queue":
		return "输入目标后按 Enter 加入队列。"
	case "harness.hint.primary.run":
		return "输入目标后按 Enter 开始运行。"
	case "harness.hint.primary.tasks":
		return "按 Enter 重跑所选失败任务。"
	case "harness.hint.primary.run_queued":
		return "Enter 跑下一个；a 全部运行；f 重试失败；s 恢复中断。"
	case "harness.hint.primary.inbox":
		return "按 p 晋升该 owner，或按 f 重试该 owner。"
	case "harness.hint.primary.review":
		return "按 Enter 通过，或按 x 拒绝。"
	case "harness.hint.primary.promote":
		return "按 Enter 晋升所选任务。"
	case "harness.hint.primary.release":
		return "按 Enter 应用当前发布批次。"
	case "harness.hint.primary.rollouts":
		return "按 Enter 推进；g 批准 gate；p 暂停/恢复；x 中止。"
	case "harness.hint.primary.none":
		return "这个分区不需要内联输入。"
	case "harness.message.read_only":
		return "当前有其他运行进行中，Harness 面板为只读。"
	case "harness.message.monitor_refreshed":
		return "Harness 监控已刷新。"
	case "harness.message.rerun_failed_only":
		return "Harness 任务 %s 当前是 %s；只有失败任务才能重跑。"
	case "harness.message.review_approved":
		return "已通过 %s 的评审"
	case "harness.message.review_rejected":
		return "已拒绝 %s 的评审"
	case "harness.message.promoted":
		return "已晋升 %s"
	case "harness.message.no_release_tasks":
		return "没有可发布的 harness 任务。"
	case "harness.message.release_applied":
		return "已应用发布批次 %s"
	case "harness.message.no_rollouts":
		return "没有持久化的 rollout。"
	case "harness.message.rollout_advanced":
		return "已推进 rollout %s"
	case "harness.message.owner_promoted":
		return "已为 %2$s 晋升 %1$d 个任务"
	case "harness.message.owner_retried":
		return "已为 %s 重试失败任务"
	case "harness.message.gate_approved":
		return "已批准 %s 的下一个 gate"
	case "harness.message.rollout_resumed":
		return "已恢复 rollout %s"
	case "harness.message.rollout_paused":
		return "已暂停 rollout %s"
	case "harness.message.rollout_aborted":
		return "已中止 rollout %s"
	case "harness.message.check_passed":
		return "Harness 检查已通过。"
	case "harness.message.check_failed":
		return "Harness 检查发现问题。"
	case "harness.message.gc_complete":
		return "Harness 清理完成。"
	case "harness.message.queue_goal_required":
		return "请先在面板输入框里填写排队目标。"
	case "harness.message.queued":
		return "已加入 harness 队列：%s"
	case "harness.activity.status":
		return "Harness %s"
	case "harness.log.phase":
		return "阶段"
	case "harness.log.worker":
		return "Worker"
	case "harness.tool.read_file":
		return "读取文件"
	case "harness.tool.write_file":
		return "写入文件"
	case "harness.tool.browse_files":
		return "浏览文件"
	case "harness.tool.search_code":
		return "搜索代码"
	case "harness.tool.run_command":
		return "执行命令"
	case "harness.tool.fetch_web_page":
		return "抓取网页"
	case "harness.tool.run_subagent":
		return "运行子代理"
	case "harness.tool.update_task_state":
		return "更新任务状态"
	case "harness.message.run_goal_required":
		return "请先在面板输入框里填写运行目标。"
	case "harness.message.no_queued_executed":
		return "没有执行任何已排队的 harness 任务。"
	case "harness.message.queue_retried":
		return "已重试 %d 个失败的排队任务。"
	case "harness.message.queue_resumed":
		return "已恢复 %d 个中断的排队任务。"
	case "harness.message.queue_ran":
		return "已运行 %d 个排队任务。"
	case "harness.preview.not_initialized":
		return "当前项目还没有初始化 harness。\n\n按 Enter 或 i 可在当前仓库中运行 harness init。"
	case "harness.preview.check":
		return "对当前项目运行 harness 检查。\n\nEnter：执行所需的文件/内容/上下文检查，以及配置中的校验命令。"
	case "harness.preview.gc":
		return "运行 harness 垃圾清理。\n\nEnter：归档陈旧任务、放弃陈旧的 blocked/running 工作、清理旧日志，并移除孤儿 worktree。"
	case "harness.preview.queue_help":
		return "在这里输入 harness 目标，然后按 Enter 加入队列。"
	case "harness.preview.run_help":
		return "在这里输入 harness 目标，然后按 Enter 开始运行。"
	case "harness.preview.run_queued":
		return "队列状态：\nqueued=%d running=%d blocked=%d failed=%d\n\nEnter 运行下一个可执行任务。\na 运行全部可执行任务。\nf 重试失败任务。\ns 恢复中断任务。"
	case "harness.preview.no_owner":
		return "当前没有选中的 harness owner。"
	case "harness.preview.no_context":
		return "当前没有选中的 harness 上下文。"
	case "harness.preview.no_task":
		return "当前没有选中的 harness 任务。"
	case "harness.preview.project_not_initialized":
		return "当前项目还没有初始化 harness。"
	case "harness.preview.project_initialized":
		return "Harness 已初始化。"
	case "harness.preview.project_help":
		return "使用 /harness 浏览并操作控制平面。"
	case "harness.preview.no_doctor":
		return "没有 harness 诊断报告。"
	case "harness.preview.monitor_unavailable":
		return "Harness 监控不可用。"
	case "harness.label.context_title":
		return "上下文"
	case "harness.label.owner_title":
		return "Owner"
	case "harness.label.id":
		return "id"
	case "harness.label.status":
		return "状态"
	case "harness.label.goal":
		return "目标"
	case "harness.label.attempts":
		return "尝试次数"
	case "harness.label.depends_on":
		return "依赖"
	case "harness.label.context":
		return "上下文"
	case "harness.label.workspace":
		return "工作区"
	case "harness.label.branch":
		return "分支"
	case "harness.label.worker":
		return "worker"
	case "harness.label.progress":
		return "进度"
	case "harness.label.verification":
		return "验证"
	case "harness.label.changed_files":
		return "变更文件数"
	case "harness.label.delivery_report":
		return "交付报告"
	case "harness.label.delivery_report_human":
		return "交付报告"
	case "harness.label.log":
		return "日志"
	case "harness.label.review":
		return "评审"
	case "harness.label.review_notes":
		return "评审备注"
	case "harness.label.promotion":
		return "晋升"
	case "harness.label.promotion_notes":
		return "晋升备注"
	case "harness.label.release_batch":
		return "发布批次"
	case "harness.label.release_batch_human":
		return "发布批次"
	case "harness.label.release_notes":
		return "发布备注"
	case "harness.label.error":
		return "错误"
	case "harness.label.name":
		return "名称"
	case "harness.label.description":
		return "描述"
	case "harness.label.owner":
		return "Owner"
	case "harness.label.commands":
		return "命令数"
	case "harness.label.tasks":
		return "任务"
	case "harness.label.rollouts":
		return "rollout"
	case "harness.label.gates":
		return "gate"
	case "harness.label.latest":
		return "最近任务"
	case "harness.label.repo":
		return "仓库"
	case "harness.label.config":
		return "配置"
	case "harness.label.project":
		return "项目"
	case "harness.label.structure":
		return "结构"
	case "harness.label.contexts":
		return "上下文数"
	case "harness.label.workers":
		return "worker"
	case "harness.label.workflow":
		return "流程"
	case "harness.label.quality":
		return "质量"
	case "harness.label.worktrees":
		return "worktree"
	case "harness.label.snapshot":
		return "快照"
	case "harness.label.events":
		return "事件"
	case "harness.label.target":
		return "目标"
	case "harness.label.review_ready":
		return "待评审"
	case "harness.label.promotion_ready":
		return "待晋升"
	case "harness.label.retryable":
		return "可重试"
	case "harness.task_title":
		return "Harness 任务"
	case "harness.doctor_title":
		return "Harness 诊断"
	case "harness.monitor_title":
		return "Harness 监控"
	case "harness.latest_task":
		return "最近任务"
	case "harness.latest_event":
		return "最近事件"
	case "harness.focus":
		return "关注对象"
	case "harness.status.ok":
		return "正常"
	case "harness.status.needs_attention":
		return "需要关注"
	case "harness.group.review":
		return "评审"
	case "harness.group.promotion":
		return "晋升"
	case "harness.group.retry":
		return "重试"
	case "harness.review_ready_short":
		return "评审"
	case "harness.promote_ready_short":
		return "晋升"
	case "harness.tasks_count":
		return "任务"
	case "harness.input_empty":
		return "（输入框为空）"
	case "harness.no_waves":
		return "没有波次"
	case "harness.mixed":
		return "混合"
	case "hint.autocomplete":
		return "Tab/Shift+Tab 切换 • Enter 应用 • Esc 关闭"
	case "hint.mention":
		return "@ 可附加文件/目录 • Tab/Shift+Tab 切换 • Enter 应用"
	case "hint.mode":
		return "模式"
	case "mode.approval.ask":
		return "按需询问"
	case "mode.approval.none":
		return "不会停下来审批"
	case "mode.approval.critical":
		return "仅关键操作"
	case "mode.tools.rules":
		return "遵循工具规则"
	case "mode.tools.readonly":
		return "仅只读"
	case "mode.tools.safe":
		return "仅安全操作"
	case "mode.tools.open":
		return "几乎全部工具"
	case "mode.agent.waits":
		return "等待你决策"
	case "mode.agent.autocontinue":
		return "自动继续推进"
	case "hint.enter_send":
		return "Enter 发送"
	case "hint.ctrlv_image":
		return "Ctrl+V 粘贴图片"
	case "hint.ctrlr_sidebar":
		return "Ctrl+R 侧栏"
	case "hint.help":
		return "/? 帮助"
	case "hint.add_context":
		return "@ 添加上下文"
	case "hint.scroll":
		return "PgUp/PgDn 滚动"
	case "hint.shift_tab_mode":
		return "Shift+Tab 切模式"
	case "hint.ctrlc_cancel":
		return "Ctrl+C 取消"
	case "hint.ctrlc_exit":
		return "Ctrl+C 清空/退出"
	case "hint.image_attached":
		return "已附加图片"
	case "hint.follow_panel":
		return "Ctrl+N 跟随"
	case "hint.unfollow_panel":
		return "Ctrl+N 取消跟随"
	case "queued.count":
		return "%d 条排队中"
	case "queued.output":
		return "[已排队 %d 条待发送]\n\n"
	case "interrupt.delivered":
		return "[已送达当前运行，正在调整方向]\n"
	case "status.thinking":
		return "思考中..."
	case "status.writing":
		return "输出中..."
	case "status.cancelling":
		return "取消中..."
	case "status.compacting":
		return "压缩中..."
	case "status.compacted":
		return "[会话已压缩]"
	case "status.tools_used":
		return "已调用 %d 个工具"
	case "tool.done":
		return "完成"
	case "tool.failed":
		return "失败"
	case "tool.no_output":
		return "无输出"
	case "tool.output":
		return "输出"
	case "tool.content":
		return "内容"
	case "tool.match":
		return "匹配"
	case "tool.matches":
		return "匹配"
	case "tool.entry":
		return "项"
	case "tool.result":
		return "结果"
	case "approval.rejected":
		return "  已拒绝。\n"
	case "approval.allow":
		return "允许"
	case "approval.allow_always":
		return "总是允许"
	case "approval.deny":
		return "拒绝"
	case "approval.accept":
		return "接受"
	case "approval.reject":
		return "拒绝"

	case "exit.confirm":
		return "再按一次 Ctrl-C 退出。\n\n"
	case "interrupted":
		return "[已中断]\n\n"
	case "lang.current":
		return "当前语言：%s\n使用 /lang 打开选择列表，或用 /lang <en|zh-CN> 直接切换。\n%s\n\n"
	case "lang.invalid":
		return "不支持的语言：%s\n%s\n\n"
	case "lang.switch":
		return "已切换语言为：%s\n\n"
	case "lang.selection.current":
		return " 当前语言：%s"
	case "lang.selection.hint":
		return " Tab/j/k 移动 • Enter 确认 • e/z 快捷键 • Esc 取消"
	case "lang.first_use.title":
		return "选择你偏好的语言"
	case "lang.first_use.body":
		return " 请选择今后 ggcode 默认使用的界面语言。"
	case "lang.first_use.hint":
		return " Tab/j/k 移动 • Enter 确认 • e/z 快捷键"
	case "mode.current":
		return "当前模式：%s\n用法：/mode <supervised|plan|auto|bypass|autopilot>\n  supervised  未显式配置的工具会询问\n  plan        严格只读探索；拒绝写入和命令\n  auto        自动允许安全操作，拒绝危险操作\n  bypass      基本全放行，只在关键操作时停下\n  autopilot   等同 bypass，并在模型反问时自动继续\n\n"
	case "mode.persist_failed":
		return "持久化模式偏好失败：%v"
	case "input.placeholder":
		return "输入消息...（$ / ! 进入 shell 模式）"
	case "panel.model_filter.prompt":
		return "筛选> "
	case "panel.model_filter.placeholder":
		return "输入以筛选模型"
	case "panel.model_list.none":
		return "(空)"
	case "panel.model_list.no_matches":
		return "(无匹配)"
	case "panel.model_list.showing":
		return "显示 %d/%d 个模型"
	case "panel.model_list.hidden_above":
		return "上方还有 %d 个"
	case "panel.model_list.hidden_more":
		return "还有 %d 个"
	case "panel.provider.vendors":
		return "供应商"
	case "panel.provider.endpoints":
		return "端点"
	case "panel.provider.models":
		return "模型"
	case "panel.provider.active_draft":
		return "当前草稿"
	case "panel.provider.protocol":
		return "协议"
	case "panel.provider.protocol.unknown":
		return "(未知)"
	case "panel.provider.auth":
		return "认证"
	case "panel.provider.env_var":
		return "环境变量"
	case "panel.provider.api_key":
		return "API Key"
	case "panel.provider.api_key.missing":
		return "未配置"
	case "panel.provider.api_key.configured":
		return "已配置"
	case "panel.provider.auth.connected":
		return "已连接"
	case "panel.provider.auth.not_connected":
		return "未连接"
	case "panel.provider.base_url":
		return "Base URL"
	case "panel.provider.base_url.not_set":
		return "(未设置)"
	case "panel.provider.enterprise_url":
		return "企业地址"
	case "panel.provider.tags":
		return "标签"
	case "panel.provider.model.set_with_m":
		return "(按 m 设置)"
	case "panel.provider.edit":
		return "编辑"
	case "panel.provider.edit.vendor_api_key":
		return "供应商 API Key"
	case "panel.provider.edit.endpoint_api_key":
		return "端点 API Key"
	case "panel.provider.edit.endpoint_base_url":
		return "端点 Base URL"
	case "panel.provider.edit.custom_model":
		return "自定义模型"
	case "panel.provider.hint.edit":
		return "Enter 保存 • Esc 取消"
	case "panel.provider.hint.main":
		return "Tab/Shift+Tab 切换焦点 • j/k 移动 • / 聚焦筛选 • Enter 或 s 应用 • a 供应商 key • u 端点 key • b Base URL • m 自定义模型 • Esc 关闭"
	case "panel.provider.hint.copilot":
		return "GitHub Copilot：l 登录 • x 登出 • b 编辑企业域名"
	case "panel.provider.saved":
		return "已保存。"
	case "panel.provider.saved_activated":
		return "已保存并激活。"
	case "panel.provider.login.starting":
		return "正在启动 GitHub Copilot 登录..."
	case "panel.provider.login.instructions":
		return "打开 %s 并输入代码 %s，正在等待授权..."
	case "panel.provider.login.copied":
		return "已将 device code 复制到剪贴板。"
	case "panel.provider.login.copy_failed":
		return "复制 device code 失败：%s"
	case "panel.provider.login.browser_opened":
		return "已在浏览器中打开验证页面。"
	case "panel.provider.login.browser_failed":
		return "打开验证页面失败：%s"
	case "panel.provider.login.success":
		return "GitHub Copilot 已连接。"
	case "panel.provider.login.failed":
		return "GitHub Copilot 登录失败：%s"
	case "panel.provider.logout.success":
		return "GitHub Copilot 已断开。"
	case "panel.provider.refreshing_vendor":
		return "正在刷新 %s 的模型..."
	case "panel.provider.refresh.save_failed":
		return "模型已刷新，但保存配置失败：%s"
	case "panel.provider.refresh.partial":
		return "已刷新 %d 个端点，发现 %d 个模型。部分端点失败：%v"
	case "panel.provider.refresh.success":
		return "已刷新 %d 个端点，发现 %d 个模型。"
	case "panel.provider.refresh.failed":
		return "模型刷新失败：%s"
	case "panel.provider.refresh.none":
		return "这个供应商没有可刷新的端点。"
	case "panel.model.models":
		return "模型"
	case "panel.model.vendor":
		return "供应商"
	case "panel.model.endpoint":
		return "端点"
	case "panel.model.protocol":
		return "协议"
	case "panel.model.source":
		return "来源"
	case "panel.model.source.builtin":
		return "内置"
	case "panel.model.source.remote":
		return "远端"
	case "panel.model.refreshing":
		return "正在刷新最新模型..."
	case "panel.model.hint.main":
		return "j/k 移动 • Enter 或 s 应用 • r 刷新 • / 聚焦筛选 • Esc 关闭 • /model <name> 直接切换"
	case "panel.model.saved_runtime_inactive":
		return "配置已保存，但当前运行时仍未激活：%s"
	case "panel.model.switched":
		return "已切换模型为 %s。"
	case "panel.model.refresh.save_failed":
		return "模型已刷新，但保存配置失败：%s"
	case "panel.model.refresh.builtin_reason":
		return "使用内置模型：%s"
	case "panel.model.refresh.remote_loaded":
		return "已加载 %d 个远端模型。"
	case "panel.model.refresh.builtin_loaded":
		return "已加载内置模型。"
	case "command.unknown":
		return "未知命令：%s\n"
	case "command.help_hint":
		return "输入 /help 查看可用命令\n\n"
	case "command.usage.allow":
		return "用法：/allow <tool-name>\n\n"
	case "command.usage.resume":
		return "用法：/resume <session-id>\n\n"
	case "command.usage.export":
		return "用法：/export <session-id>\n\n"
	case "init.resolve_failed":
		return "解析初始化目标失败：%v\n\n"
	case "init.generate_failed":
		return "生成 GGCODE.md 内容失败：%v\n\n"
	case "init.collecting":
		return "正在收集项目知识..."
	case "command.model_switched":
		return "已切换模型为：%s（供应商：%s）\n\n"
	case "command.model_failed":
		return "切换模型失败：%v\n\n"
	case "command.model_current":
		return "当前模型：%s（供应商：%s）\n可用模型：%s\n使用 /model 打开模型面板，或用 /model <model-name> 直接切换。\n\n"
	case "command.provider_unknown":
		return "未知供应商：%s（可用：%v）\n\n"
	case "command.provider_switched":
		return "已切换供应商为：%s（模型：%s）\n\n"
	case "command.provider_failed":
		return "更新供应商选择失败：%v\n\n"
	case "command.provider_current":
		return "当前供应商：%s（接口：%s，模型：%s）\n可用供应商：%s\n可用接口：%s\n用法：/provider [vendor] [endpoint]\n\n"
	case "command.allow_set":
		return "✓ %s 已设为永久允许\n\n"
	case "command.custom":
		return "自定义命令 /%s：\n"
	case "command.mention_error":
		return "展开 @ 引用失败：%v"
	case "session.list_failed":
		return "列出会话失败：%v\n\n"
	case "session.untitled":
		return "未命名"
	case "session.store_missing":
		return "未配置会话存储。\n\n"
	case "session.none":
		return "没有找到会话。\n\n"
	case "session.list.title":
		return "会话列表：\n\n"
	case "session.list.item":
		return "  %d. %s  %s  （%s）\n"
	case "session.list.hint":
		return "\n使用 /resume <id> 继续某个会话\n\n"
	case "session.new":
		return "新会话：%s\n\n"
	case "session.resume":
		return "已恢复会话：%s — %s（%d 条消息）\n\n"
	case "session.resume_failed":
		return "恢复会话 %s 失败：%v\n\n"
	case "session.resume_fallback":
		return "将改为创建新会话。\n\n"
	case "session.export_failed":
		return "导出会话失败：%v\n\n"
	case "session.write_failed":
		return "写入文件失败：%v\n\n"
	case "session.exported":
		return "已导出会话 %s 到 %s\n\n"
	case "checkpoint.disabled":
		return "未启用检查点。\n\n"
	case "checkpoint.undo_failed":
		return "撤销失败：%v\n\n"
	case "checkpoint.undid":
		return "已撤销 %s 对 %s 的修改（检查点 %s）\n"
	case "checkpoint.none":
		return "没有检查点。\n\n"
	case "checkpoint.list.title":
		return "检查点（%d）：\n\n"
	case "checkpoint.list.item":
		return "  %d. %s  %s  %s  %s\n"
	case "checkpoint.list.hint":
		return "\n使用 /undo 回滚最近一次修改。\n\n"
	case "memory.auto_unavailable":
		return "自动记忆未初始化。\n\n"
	case "memory.list_failed":
		return "列出记忆失败：%v\n\n"
	case "memory.none":
		return "没有自动记忆。\n\n"
	case "memory.auto_title":
		return "自动记忆：\n"
	case "memory.clear_failed":
		return "清空记忆失败：%v\n\n"
	case "memory.cleared":
		return "已清空所有自动记忆。\n\n"
	case "memory.title":
		return "记忆：\n"
	case "memory.project":
		return "项目记忆：\n"
	case "memory.project_none":
		return "  未加载项目记忆文件。\n"
	case "memory.auto":
		return "自动记忆：\n"
	case "memory.auto_none":
		return "  未加载自动记忆。\n"
	case "memory.usage":
		return "\n用法：/memory [list|clear]\n\n"
	case "compact.unavailable":
		return "上下文管理器不可用。\n\n"
	case "compact.failed":
		return "压缩失败：%v\n\n"
	case "compact.done":
		return "已压缩对话历史。\n\n"
	case "compact.done_with_stats":
		return "已压缩对话历史（%d → %d tokens）。\n\n"
	case "todo.cleared":
		return "已清空 todo 列表。\n\n"
	case "todo.clear_failed":
		return "清空 todo 失败：%v\n\n"
	case "todo.none":
		return "没有找到 todo 列表。请使用 todo_write 工具创建。\n\n"
	case "todo.read_failed":
		return "读取 todo 失败：%v\n\n"
	case "todo.parse_failed":
		return "解析 todo 失败：%v\n\n"
	case "todo.title":
		return "Todo 列表：\n%s\n\n"
	case "bug.title":
		return "=== Bug 报告诊断信息 ===\n\n"
	case "bug.version":
		return "版本：%s\n"
	case "bug.os":
		return "系统：%s %s\n"
	case "bug.go":
		return "Go：%s\n"
	case "bug.provider":
		return "供应商：%s\n"
	case "bug.model":
		return "模型：%s\n"
	case "bug.session":
		return "会话：%s（%d 条消息）\n"
	case "bug.mcp":
		return "MCP 服务器：%d\n"
	case "bug.last_error":
		return "最近错误：%s\n"
	case "bug.hint":
		return "\n报告 bug 时请附带这些信息。\n\n"
	case "config.usage":
		return "用法：/config set <key> <value>\n\n可设置：model, vendor, endpoint, language, apikey [--vendor]\n\n端点管理：/config add-endpoint <名称> <base_url> [--protocol openai] [--apikey sk-xxx]\n          /config remove-endpoint <名称>\n\n"
	case "config.not_loaded":
		return "配置未加载。\n\n"
	case "config.model_set":
		return "配置：model = %s\n\n"
	case "config.provider_set":
		return "配置：provider = %s\n\n"
	case "config.language_set":
		return "配置：language = %s\n\n"
	case "config.unknown_key":
		return "未知配置项：%s\n支持：model, provider, language\n\n"
	case "config.title":
		return "当前配置：\n"
	case "status.title":
		return "状态：\n"
	case "panel.update":
		return "更新"
	case "label.version":
		return "版本"
	case "label.latest":
		return "最新"
	case "update.sidebar_hint":
		return "发现新版本，可使用 /update 升级。"
	case "update.up_to_date":
		return "当前已是最新版本。"
	case "update.available":
		return "可升级到：%s"
	case "update.current":
		return "当前：%s（最新：%s）"
	case "update.unknown":
		return "尚未检查"
	case "update.check_failed":
		return "检查失败：%s"
	case "update.unavailable":
		return "当前会话无法升级。\n\n"
	case "update.preparing":
		return "正在准备升级"
	case "update.failed":
		return "升级失败：%v\n\n"
	case "update.restart_failed":
		return "升级已准备完成，但重启失败：%v\n\n"
	case "plugins.unavailable":
		return "插件管理器不可用。\n\n"
	case "plugins.none":
		return "没有加载插件。\n\n"
	case "plugins.title":
		return "插件：\n"
	case "mcp.none":
		return "没有配置 MCP 服务器。\n\n"
	case "mcp.title":
		return "MCP 服务器：\n"
	case "mcp.active_tools":
		return "活动工具"
	case "mcp.more":
		return "… 还有 %d 个，使用 /mcp 查看"
	case "image.usage":
		return "用法：/image <path/to/file.png>\n"
	case "image.formats":
		return "支持格式：PNG、JPEG、GIF、WebP（最大 20MB）\n\n"
	case "image.attached":
		return "已附加图片：%s\n"
	case "image.attached_hint":
		return "发送一条消息即可携带这张图片，或继续用 /image 再附加一张。\n\n"
	case "image.clipboard_failed":
		return "从剪贴板粘贴图片失败：%v"
	case "cron.firing":
		return "⏰ 定时任务触发"
	case "agents.none":
		return "还没有创建子 Agent。\n用法：LLM 可以使用 spawn_agent 工具创建子 Agent。\n\n"
	case "agents.title":
		return "%d 个子 Agent：\n"
	case "agents.item":
		return "  %s [%s]%s - %s\n"
	case "agents.hint":
		return "\n使用 /agent <id> 查看详情，/agent cancel <id> 取消。\n\n"
	case "agent.usage":
		return "用法：/agent <id> 或 /agent cancel <id>\n\n"
	case "agent.cancelled":
		return "已取消子 Agent %s\n\n"
	case "agent.cancel_failed":
		return "无法取消 %s（未找到或未在运行）\n\n"
	case "agent.not_found":
		return "未找到子 Agent %s\n\n"
	case "agent.title":
		return "Agent：%s\n状态：%s\n任务：%s\n"
	case "agent.result":
		return "结果：%s\n"
	case "agent.error":
		return "错误：%v\n"
	case "slash.help":
		return "显示帮助"
	case "slash.sessions":
		return "列出已保存会话"
	case "slash.resume":
		return "恢复历史会话"
	case "slash.export":
		return "导出会话为 Markdown"
	case "slash.model":
		return "切换模型"
	case "slash.provider":
		return "打开供应商管理界面"
	case "slash.clear":
		return "清空对话"
	case "slash.mcp":
		return "显示 MCP 服务器"
	case "slash.memory":
		return "管理记忆"
	case "slash.undo":
		return "撤销最近一次文件修改"
	case "slash.checkpoints":
		return "列出检查点"
	case "slash.allow":
		return "永久允许某个工具"
	case "slash.plugins":
		return "列出已加载插件"
	case "slash.image":
		return "附加图片"
	case "slash.init":
		return "生成项目 GGCODE.md"
	case "slash.harness":
		return "运行 harness 工作流命令"
	case "slash.lang":
		return "切换界面语言"
	case "slash.skills":
		return "浏览可用 skills"
	case "slash.exit":
		return "退出 ggcode"
	case "slash.compact":
		return "压缩对话历史"
	case "slash.todo":
		return "查看/管理 todo"
	case "slash.bug":
		return "报告 bug"
	case "slash.config":
		return "查看/修改配置"
	case "slash.qq":
		return "管理 QQ 渠道绑定"
	case "slash.telegram":
		return "管理 Telegram 渠道绑定"
	case "slash.pc":
		return "管理 PC 渠道绑定"
	case "slash.discord":
		return "管理 Discord 渠道绑定"
	case "slash.feishu":
		return "管理飞书渠道绑定"
	case "slash.slack":
		return "管理 Slack 渠道绑定"
	case "slash.dingtalk":
		return "管理钉钉渠道绑定"
	case "slash.im":
		return "打开统一 IM 渠道面板"
	case "panel.qq.directory":
		return "目录"
	case "panel.qq.runtime":
		return "运行时"
	case "panel.qq.bots":
		return "QQ 机器人"
	case "panel.qq.created":
		return "已创建：%d"
	case "panel.qq.bound":
		return "已绑定：%d"
	case "panel.qq.available":
		return "可用：%d"
	case "panel.qq.current_binding":
		return "当前绑定"
	case "panel.qq.none":
		return "(无)"
	case "panel.qq.default":
		return "(默认)"
	case "panel.qq.adapter":
		return "机器人：%s"
	case "panel.qq.target":
		return "目标：%s"
	case "panel.qq.channel":
		return "渠道：%s"
	case "panel.qq.bot_list":
		return "QQ 机器人列表"
	case "panel.qq.no_bots":
		return "没有配置 QQ 机器人。"
	case "panel.qq.entry.available":
		return "可用"
	case "panel.qq.entry.bound":
		return "已绑定"
	case "panel.qq.entry.active":
		return "活跃"
	case "panel.qq.entry.bound_other":
		return "已绑定: %s"
	case "panel.qq.entry.muted":
		return "已静音"
	case "panel.qq.details":
		return "详情"
	case "panel.qq.status":
		return "状态：%s"
	case "panel.qq.transport":
		return "传输：%s"
	case "panel.qq.bound_directory":
		return "绑定目录：%s"
	case "panel.qq.current_directory_target":
		return "当前目录目标：%s"
	case "panel.qq.current_directory_channel":
		return "当前目录渠道：%s"
	case "panel.qq.waiting_for_pairing":
		return "(等待配对)"
	case "panel.qq.last_error":
		return "最近错误：%s"
	case "panel.qq.occupied_by":
		return "已被占用：%s"
	case "panel.qq.create":
		return "创建"
	case "panel.qq.bot_input":
		return "QQ 机器人：%s"
	case "panel.qq.create_format":
		return "格式：<bot-id> <appid> <appsecret>"
	case "panel.qq.create_example":
		return "示例：qq-main 123456 secret-value"
	case "panel.qq.create_hint":
		return "Enter 创建机器人 • Esc 取消"
	case "panel.qq.actions_hint":
		return "j/k 移动 • Enter 或 b 绑定机器人 • c 绑定渠道 • x 解绑渠道 • u 解绑机器人 • i 创建机器人 • Esc 关闭"
	case "panel.qq.bind_channel":
		return "绑定渠道"
	case "panel.qq.scan_hint":
		return "扫描二维码添加机器人，然后发送一条消息开始配对。"
	case "panel.qq.qr_code":
		return "二维码："
	case "panel.qq.share_link":
		return "分享链接："
	case "panel.qq.message.no_bot":
		return "当前没有可用的 QQ 机器人。"
	case "panel.qq.message.bound_success":
		return "QQ 机器人已绑定到当前工作区。按 c 生成渠道绑定二维码。"
	case "panel.qq.message.share_generated":
		return "已生成 QQ 分享链接。扫描二维码添加机器人，然后发送一条消息开始配对。"
	case "panel.qq.message.unbound":
		return "已解绑 QQ 渠道。"
	case "panel.qq.message.cleared":
		return "已清除当前工作区的 QQ 渠道授权。"
	case "panel.qq.message.added_bot":
		return "已添加 QQ 机器人 %s。"
	case "panel.qq.error.config_unavailable":
		return "配置不可用"
	case "panel.qq.error.config_format":
		return "QQ 机器人配置格式必须为：<bot-id> <appid> <appsecret>"
	case "panel.qq.error.adapter_required":
		return "QQ 适配器名称不能为空"
	case "panel.qq.error.not_configured":
		return "QQ 机器人 %q 未配置"
	case "panel.qq.error.disabled":
		return "QQ 机器人 %q 已禁用"
	case "panel.qq.error.not_qq_adapter":
		return "适配器 %q 不是 QQ 机器人"
	case "panel.qq.error.not_online":
		return "QQ 机器人 %q 未在线"
	case "panel.qq.error.not_online_detail":
		return "QQ 机器人 %q 未在线：%s"
	case "panel.qq.runtime.available":
		return "可用"
	case "panel.qq.runtime.disabled":
		return "已禁用（设置 im.enabled: true 并重启 ggcode）"
	case "panel.qq.runtime.not_started":
		return "未启动（重启 ggcode 以初始化 IM 运行时）"
	case "panel.qq.status.not_started":
		return "未启动"
	case "panel.qq.status.unknown":
		return "未知"
	case "slash.status":
		return "显示当前状态"
	case "slash.update":
		return "升级 ggcode"
	case "help.text":
		return `可用命令：
  /help, /?          显示帮助
  /sessions          列出已保存会话
  /resume <id>       恢复历史会话
  /export <id>       导出会话为 Markdown 文件
  /model [name]      打开模型面板或直接切换
  /provider [vendor] 打开供应商管理界面
  /qq                打开 QQ 绑定面板
  /telegram          打开 Telegram 绑定面板
  /pc                打开 PC 绑定面板
  /discord           打开 Discord 绑定面板
  /feishu            打开飞书绑定面板
  /slack             打开 Slack 绑定面板
  /dingtalk          打开钉钉绑定面板
  /im                打开统一 IM 渠道面板
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
  /restart           重启 ggcode（使用最新二进制）
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
  鼠标滚轮                 滚动对话输出`
	case "command.harness_usage":
		return "用法：/harness <init|check|queue|tasks|run|rerun|run-queued|monitor|contexts|inbox|review|promote|release|gc|doctor> ...（release 支持 rollouts|advance|pause|resume|abort|approve|reject）"
	case "command.harness_queue_usage":
		return "用法：/harness queue <goal>"
	case "command.harness_run_usage":
		return "用法：/harness run <goal>"
	case "command.harness_rerun_usage":
		return "用法：/harness rerun <task-id>"
	case "command.skill_agent_only":
		return "技能 %s 只能由 agent 调用。"
	case "command.harness_owner_promoted":
		return "已为 owner %[2]s 推进 %[1]d 个 harness 任务。"
	case "command.harness_review_approved":
		return "已批准 harness 任务 %s。"
	case "command.harness_review_rejected":
		return "已拒绝 harness 任务 %s。"
	case "command.harness_promoted_many":
		return "已推进 %d 个 harness 任务。"
	case "command.harness_promoted_one":
		return "已推进 harness 任务 %s。"
	case "command.harness_task_queued_detail":
		return "已加入 harness 队列：%s。\n- 目标：%s"
	case "command.harness_tasks_empty":
		return "还没有记录任何 harness 任务。"
	case "command.harness_run_start":
		return "正在启动跟踪型 harness 运行...\n可使用 /harness monitor 或 Tasks/Monitor 视图查看实时状态。"
	case "command.harness_rerun_start":
		return "正在启动跟踪型 harness 重跑...\n可使用 /harness monitor 或 Tasks/Monitor 视图查看实时状态。"
	case "command.harness_rerun_invalid_status":
		return "Harness 任务 %s 当前是 %s；只有失败任务才能重跑。"
	case "command.harness_status_starting_run":
		return "正在启动 harness 运行..."
	case "command.harness_status_starting_rerun":
		return "正在启动 harness 重跑..."
	case "command.harness_spinner_running":
		return "正在运行 harness"
	case "command.harness_cancelled":
		return "Harness 运行已取消。"
	default:
		if v, ok := lookupModuleCatalog(LangZhCN, key); ok {
			return v
		}
		return key
	}
}
