package tui

// inspectorTextZH returns the zh-CN message for key; unmapped keys fall back to key itself.
// Extracted from inspectorText to keep per-language lookup tables in one place.
func inspectorTextZH(key string) string {
	switch key {
	case "hint_sessions":
		return "↑/↓ 选择 • Enter 恢复 • A 全部工作区 • E 导出 • / 过滤 • Esc 关闭"
	case "session_filter":
		return "过滤: %s (Esc 清除)"
	case "hint_search":
		return "↑/↓ 选择 • Enter 恢复该会话 • Esc 关闭"
	case "hint_agents":
		return "↑/↓ 选择 • X 取消 • Esc 关闭"
	case "hint_checkpoints":
		return "↑/↓ 选择 • Enter 回退到所选检查点 • Esc 关闭"
	case "hint_memory":
		return "↑/↓ 选择 • C 清空自动记忆 • Esc 关闭"
	case "hint_todos":
		return "↑/↓ 选择 • C 清空 todo • Esc 关闭"
	case "hint_default":
		return "↑/↓ 选择 • Esc 关闭"
	case "hint_status":
		return "↑/↓ 选择 • Enter 安装缺失的 LSP • Esc 关闭"
	case "hint_lsp_install":
		return "↑/↓ 选择 • Enter 执行安装 • Esc 返回 /status"
	case "sessions_empty":
		return "暂无会话。"
	case "sessions_empty_ws":
		return "当前 workspace 暂无会话"
	case "sessions_empty_ws_hint":
		return "按 A 显示所有工作区的会话"
	case "sessions_loading":
		return "正在加载会话..."
	case "session_locked":
		return "该会话已被其他实例锁定"
	case "agents_empty":
		return "暂无子 Agent。"
	case "checkpoints_empty":
		return "暂无检查点。"
	case "memory_empty":
		return "暂无记忆内容。"
	case "todos_empty":
		return "暂无 todo。"
	case "plugins_empty":
		return "暂无插件。"
	case "panel_empty":
		return "暂无可显示内容。"
	case "revert_failed":
		return "回退失败：%v"
	case "reverted":
		return "已回退到检查点（已恢复文件：%s）"
	case "agent_cancelled":
		return "已取消子 Agent %s"
	case "agent_cancel_failed":
		return "取消子 Agent %s 失败"
	case "memory_unavailable":
		return "自动记忆不可用"
	case "memory_cleared":
		return "已清空自动记忆"
	case "todo_cleared":
		return "已清空 todo"
	case "clear_failed":
		return "清理失败：%v"
	case "untitled_session":
		return "未命名会话"
	case "current_workspace":
		return "当前工作区"
	case "session_id":
		return "会话"
	case "updated":
		return "更新"
	case "messages":
		return "消息"
	case "vendor":
		return "供应商"
	case "endpoint":
		return "接入点"
	case "model":
		return "模型"
	case "workspace":
		return "工作区"
	case "status":
		return "状态"
	case "created":
		return "创建"
	case "started":
		return "开始"
	case "ended":
		return "结束"
	case "phase":
		return "阶段"
	case "progress":
		return "进度"
	case "result":
		return "结果"
	case "error":
		return "错误"
	case "checkpoint_id":
		return "检查点"
	case "tool":
		return "工具"
	case "project_memory":
		return "项目记忆"
	case "auto_memory":
		return "自动记忆"
	case "files":
		return "文件"
	case "none":
		return "无"
	case "todos_error":
		return "读取 todo 失败"
	case "sessions_error":
		return "读取会话失败"
	case "loaded":
		return "已加载"
	case "failed":
		return "失败"
	case "tools":
		return "工具"
	case "active_selection":
		return "当前选择"
	case "runtime_limits":
		return "运行限制"
	case "language":
		return "语言"
	case "runtime":
		return "运行时"
	case "version":
		return "版本"
	case "mode":
		return "模式"
	case "agents":
		return "子 Agent"
	case "running":
		return "运行中"
	case "update":
		return "更新"
	case "mcp":
		return "MCP"
	case "session":
		return "会话"
	case "todo_id":
		return "Todo"
	case "vendors":
		return "供应商数量"
	case "mcp_servers":
		return "MCP 服务数"
	case "context_window":
		return "上下文窗口"
	case "max_tokens":
		return "最大输出"
	case "lsp":
		return "LSP"
	case "lsp_ready":
		return "可用"
	case "lsp_unavailable":
		return "不可用"
	case "lsp_binary":
		return "二进制"
	case "lsp_install":
		return "安装"
	case "lsp_detected_by":
		return "检测依据"
	case "lsp_install_options":
		return "安装选项"
	case "lsp_recommended":
		return "推荐"
	case "lsp_install_enter_hint":
		return "按 Enter 直接安装；如果有多个候选，会先让你选择。"
	case "lsp_install_unavailable":
		return "当前没有可用的安装选项"
	case "lsp_no_supported_languages":
		return "当前工作区未检测到已支持的语言"
	}
	return key
}

// inspectorTextEN returns the English message for key; unmapped keys fall back to key itself.
func inspectorTextEN(key string) string {
	switch key {
	case "hint_sessions":
		return "↑/↓ select • Enter resume • E export • / filter • Esc close"
	case "session_filter":
		return "Filter: %s (Esc to clear)"
	case "search_no_query":
		return "用法：/search <关键词> — 在所有会话的消息内容中搜索"
	case "search_no_results":
		return "未找到匹配的会话。"
	case "search_results":
		return "找到 %d 条结果（来自 %d 个会话）"
	case "hint_search":
		return "↑/↓ select • Enter resume session • Esc close"
	case "hint_agents":
		return "↑/↓ select • X cancel • Esc close"
	case "hint_checkpoints":
		return "↑/↓ select • Enter revert selected checkpoint • Esc close"
	case "hint_memory":
		return "↑/↓ select • C clear auto memory • Esc close"
	case "hint_todos":
		return "↑/↓ select • C clear todos • Esc close"
	case "hint_default":
		return "↑/↓ select • Esc close"
	case "hint_status":
		return "↑/↓ select • Enter install missing LSP • Esc close"
	case "hint_lsp_install":
		return "↑/↓ select • Enter run installer • Esc back to /status"
	case "sessions_empty":
		return "No sessions saved."
	case "sessions_empty_ws":
		return "No sessions in the current workspace"
	case "sessions_empty_ws_hint":
		return "Press A to show sessions from every workspace"
	case "sessions_loading":
		return "Loading sessions..."
	case "session_locked":
		return "This session is locked by another instance"
	case "agents_empty":
		return "No sub-agents recorded."
	case "checkpoints_empty":
		return "No checkpoints recorded."
	case "memory_empty":
		return "No memory sources available."
	case "todos_empty":
		return "No todos recorded."
	case "plugins_empty":
		return "No plugins loaded."
	case "panel_empty":
		return "Nothing to show."
	case "revert_failed":
		return "Revert failed: %v"
	case "reverted":
		return "Reverted checkpoint (restored files: %s)"
	case "agent_cancelled":
		return "Cancelled sub-agent %s"
	case "agent_cancel_failed":
		return "Failed to cancel sub-agent %s"
	case "memory_unavailable":
		return "Auto memory is unavailable"
	case "memory_cleared":
		return "Auto memory cleared"
	case "todo_cleared":
		return "Todos cleared"
	case "clear_failed":
		return "Clear failed: %v"
	case "untitled_session":
		return "Untitled session"
	case "current_workspace":
		return "current workspace"
	case "session_id":
		return "Session"
	case "updated":
		return "Updated"
	case "messages":
		return "Messages"
	case "vendor":
		return "Vendor"
	case "endpoint":
		return "Endpoint"
	case "model":
		return "Model"
	case "workspace":
		return "Workspace"
	case "status":
		return "Status"
	case "created":
		return "Created"
	case "started":
		return "Started"
	case "ended":
		return "Ended"
	case "phase":
		return "Phase"
	case "progress":
		return "Progress"
	case "result":
		return "Result"
	case "error":
		return "Error"
	case "checkpoint_id":
		return "Checkpoint"
	case "tool":
		return "Tool"
	case "project_memory":
		return "Project memory"
	case "auto_memory":
		return "Auto memory"
	case "files":
		return "files"
	case "none":
		return "None"
	case "todos_error":
		return "Failed to read todos"
	case "sessions_error":
		return "Failed to read sessions"
	case "loaded":
		return "loaded"
	case "failed":
		return "failed"
	case "tools":
		return "Tools"
	case "active_selection":
		return "Active selection"
	case "runtime_limits":
		return "Runtime limits"
	case "language":
		return "Language"
	case "runtime":
		return "Runtime"
	case "version":
		return "Version"
	case "mode":
		return "Mode"
	case "agents":
		return "Agents"
	case "running":
		return "running"
	case "update":
		return "Update"
	case "mcp":
		return "MCP"
	case "session":
		return "Session"
	case "todo_id":
		return "Todo"
	case "vendors":
		return "Vendors"
	case "mcp_servers":
		return "MCP servers"
	case "context_window":
		return "Context window"
	case "max_tokens":
		return "Max tokens"
	case "lsp":
		return "LSP"
	case "lsp_ready":
		return "ready"
	case "lsp_unavailable":
		return "unavailable"
	case "lsp_binary":
		return "Binary"
	case "lsp_install":
		return "Install"
	case "lsp_detected_by":
		return "Detected by"
	case "lsp_install_options":
		return "Install options"
	case "lsp_recommended":
		return "recommended"
	case "lsp_install_enter_hint":
		return "Press Enter to install. Languages with multiple candidates will open a chooser first."
	case "lsp_install_unavailable":
		return "No install options available"
	case "lsp_no_supported_languages":
		return "No supported workspace languages detected"
	}
	return key
}
