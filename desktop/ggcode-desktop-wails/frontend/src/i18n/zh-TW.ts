import type { TranslationKey } from './en'

const zh: Record<TranslationKey, string> = {
  // NavRail
  'nav.chat': '聊天',
  'nav.files': '文件',
  'nav.im': 'IM 适配器',
  'nav.mcp': 'MCP 服務器',
  'nav.settings': '設置',
  'nav.debug': '調試控制台',
  'nav.cron': '定時任務',
  'cron.title': '定時任務',
  'cron.newTask': '新增任務',
  'cron.refresh': '重新整理',
  'cron.loading': '載入中...',
  'cron.empty': '暫無定時任務。點擊"新增任務"建立。',
  'cron.jobDeleted': '任務已刪除',
  'cron.confirmDelete': '確定要刪除這個定時任務嗎？',
  'cron.deleteFailed': '刪除失敗：{0}',
  'cron.jobCreated': '任務已建立',
  'cron.jobUpdated': '任務已更新',
  'cron.saveFailed': '儲存失敗：{0}',
  'cron.failedLoad': '載入失敗：{0}',
  'cron.failed': '操作失敗：{0}',
  'cron.edit': '編輯',
  'cron.delete': '刪除',
  'cron.pause': '暫停',
  'cron.resume': '恢復',
  'cron.active': '活躍',
  'cron.paused': '已暫停',
  'cron.oneShot': '一次性',
  'cron.prompt': '提示詞',
  'cron.cronExpr': 'Cron 運算式',
  'cron.cronHint': '格式：分 時 日 月 週。例：*/5 * * * *（每5分鐘）、0 9 * * 1-5（工作日9點）',
  'cron.recurring': '循環執行',
  'cron.queueIfBusy': 'Agent 忙時排隊',
  'cron.editTask': '編輯任務',
  'cron.newTaskTitle': '新增任務',
  'cron.cancel': '取消',
  'cron.create': '建立',
  'cron.update': '更新',
  'cron.generateWithAI': 'AI 生成',
  'cron.aiDescription': '用自然語言描述這個任務應該做什麼，AI 會幫你生成提示詞。',
  'cron.aiPlaceholder': '例如：檢查新的 GitHub issue 並總結',
  'cron.generate': '生成',
  'cron.generating': '生成中...',
  'cron.aiGenFailed': 'AI 生成失敗：{0}',
  'cron.cronExprRequired': 'Cron 運算式不能為空',
  'cron.promptRequired': '提示詞不能為空',
  'cron.descRequired': '請輸入描述',
  'cron.promptPlaceholder': '任務觸發時傳送給 agent 的提示詞...',
  'cron.nextFire': '下次觸發',
  'cron.createdAt': '建立時間',
  'cron.queueLabel': '忙時排隊',
  'cron.id': 'ID',
  'cron.yes': '是',
  'cron.no': '否',

  // Sidebar
  'sidebar.sessions': '會話',
  'sidebar.search': '搜索會話...',
  'sidebar.newSession': '新建會話',
  'sidebar.untitled': '未命名',
  'sidebar.delete': '刪除',
  'sidebar.group.pinned': '已置頂',
  'sidebar.group.today': '今天',
  'sidebar.group.yesterday': '昨天',
  'sidebar.group.thisWeek': '本週',
  'sidebar.group.older': '更早',
  'sidebar.time.justNow': '刚刚',
  'sidebar.time.minutesAgo': '{n}分鐘前',
  'sidebar.time.hoursAgo': '{n}小時前',
  'sidebar.time.yesterday': '昨天',

  // StatusBar
  'status.ready': '就绪',
  'status.thinking': '思考中...',
  'status.offline': '離線',
  'status.zoomIn': '放大 (Cmd+)',
  'status.zoomOut': '縮小 (Cmd-)',
  'status.zoomReset': '重置縮放 (Cmd+0)',

  // ChatView
  'chat.placeholder': '给 ggcode 發消息...',
  'chat.agentWorking': 'Agent 工作中...',
  'chat.welcome.title': '今天我能幫你做什麼？',
  'chat.welcome.subtitle': '問我任何關於代碼的問題，或者試試：',
  'chat.welcome.prompt.explain': '解釋這個代碼庫',
  'chat.welcome.prompt.review': '審查最近的改動',
  'chat.welcome.prompt.test': '寫一個測試',
  'chat.welcome.prompt.debug': '幫我調試一個問題',
  'chat.inputHint': 'Enter 發送 • Shift+Enter 換行',

  // CommandPalette
  'cmd.recent': '最近使用',
  'cmd.placeholder': '輸入命令...',
  'cmd.noResults': '没有匹配的命令',
  'cmd.newSession': '新建會話',
  'cmd.exportMarkdown': '匯出會話為 Markdown',
  'cmd.exportJSON': '匯出會話為 JSON',
  'cmd.copyMarkdown': '復制會話為 Markdown',
  'cmd.searchSessions': '搜索會話',
  'cmd.shareSession': '分享會話',
  'cmd.toggleContext': '切換上下文面板',
  'cmd.toggleTheme': '切換主題',
  'cmd.openSettings': '打開設置',
  'cmd.switchModel': '切換模型',
  'cmd.toggleSidebar': '切換側邊欄',
  'cmd.prevSession': '上一個會話',
  'cmd.nextSession': '下一個會話',
  'cmd.cat.session': '會話',
  'cmd.cat.chat': '聊天',
  'cmd.cat.settings': '設置',
  'cmd.cat.navigation': '導航',
  'cmd.cat.view': '視圖',
  'cmd.goChat': '聊天',
  'cmd.goFiles': '文件瀏覽',
  'cmd.goIM': 'IM 管理',
  'cmd.goMCP': 'MCP 服務器',
  'cmd.goLanChat': '局域網聊天',
  'cmd.goDebug': '調試控制台',
  'cmd.showShortcuts': '顯示快捷鍵',
  'cmd.zoomIn': '放大',
  'cmd.zoomOut': '縮小',
  'cmd.zoomReset': '重置縮放',
  'cmd.hintNavigate': '導航',
  'cmd.hintSelect': '選擇',
  'cmd.hintClose': 'esc 關閉',
  'cmd.results': '{count} 個結果',

  // KeyboardShortcuts
  'kb.title': '快捷鍵',
  'kb.footer': '提示：随時按 ⌘K 打開命令面板',
  'kb.global': '全局',
  'kb.chat': '聊天',
  'kb.navigation': '導航',
  'kb.views': '視圖',
  'kb.cmdPalette': '打開命令面板',
  'kb.toggleSidebar': '切換側邊欄',
  'kb.toggleContext': '切換上下文面板',
  'kb.toggleTheme': '切換深色/浅色主題',
  'kb.searchSessions': '搜索會話',
  'kb.prevSession': '上一個工作階段',
  'kb.nextSession': '下一個工作階段',
  'kb.openSettings': '打開設置',
  'kb.globalHotkey': '全域快捷鍵 (顯示/隱藏應用)',
  'kb.showShortcuts': '顯示此幫助',
  'kb.closeDialog': '關閉對話框',
  'kb.newSession': '新建會話',
  'kb.shareSession': '分享會話',
  'kb.toggleIdentity': '切換 Agent 身份',
  'kb.searchInChat': '在對話中搜索',
  'kb.sendMessage': '發送消息',
  'kb.newline': '換行（輸入框內）',
  'kb.cancelGen': '取消生成',
  'kb.inputHistory': '調用歷史輸入',
  'kb.navigateList': '導航列錶',
  'kb.vimNav': 'Vim 風格導航',
  'kb.selectItem': '選擇項目',
  'kb.goChat': '跳到聊天',
  'kb.goFiles': '跳到文件',
  'kb.goIM': '跳到 IM 适配器',
  'kb.goMCP': '跳到 MCP 服務器',
  'kb.goSettings': '跳到設置',
  'kb.goDebug': '跳到調試控制台',
  'kb.goLanChat': '跳到局域網聊天',
  'kb.zoomIn': '放大',
  'kb.zoomOut': '縮小',
  'kb.zoomReset': '重置縮放',

  // ContextPanel
  'context.title': '上下文',
  'context.usage': '上下文使用量',
  'context.input': '輸入',
  'context.output': '輸出',
  'context.cacheRead': '緩存讀取',
  'context.cacheWrite': '緩存寫入',
  'context.cacheHit': '緩存命中',
  'context.contextLabel': '{n}K 上下文',
  'context.warning': '上下文窗口已使用 {pct}%，建議開啟新會話。',
  'context.critical': '上下文窗口已使用 {pct}%，较早的消息可能被丟弃。',
  'context.estimatedCost': '預估费用',
  'context.costDisclaimer': '基於 Token 使用量和模型定價的估算，實际费用可能不同。',

  // SettingsPage
  'settings.title': '設置',
  'settings.vendor': '服務商',
  'settings.endpoint': '端點',
  'settings.baseUrl': '基础 URL',
  'settings.apiKey': 'API 密钥',
  'settings.apiKeyPlaceholder': '请輸入 API 密钥',
  'settings.model': '模型',
  'settings.permissionMode': '權限模式',
  'settings.language': '語言',
  'settings.identity': '身份',
  'settings.save': '保存',
  'settings.saved': '已保存!',
  'settings.saving': '保存中...',
  'settings.impersonate': '模擬身份',
  'settings.impersonateHint': '以不同身份測試',
  'settings.impersonateVersion': '版本',
  'settings.impersonateApply': '應用',

  // Onboarding
  'onboarding.next': '下一步',
  'onboarding.back': '上一步',
  'onboarding.getStarted': '開始使用',
  'onboarding.vendorHint': '選擇你要使用的 AI 服務商',
  'onboarding.configHint': '調整這些設置，或保持默認值',
  'onboarding.welcome': '歡迎使用 GGCode',
  'onboarding.selectDirHint': '選擇一個項目目錄開始使用',
  'onboarding.chooseDir': '選擇目錄',
  'onboarding.modeSupervised': '監督模式',
  'onboarding.modeSupervisedDesc': '執行前審查并批準每個工具調用',
  'onboarding.modeBypass': '繞過模式',
  'onboarding.modeBypassDesc': '自動批準所有工具，无需確認',
  'onboarding.fetchingModels': '獲取中...',
  'onboarding.fetchModels': '獲取模型',
  'onboarding.noModelsFound': '未找到模型。请手動輸入模型名稱。',
  'onboarding.fetchModelsFailed': '獲取模型失敗。你可以手動輸入模型名稱。',
  'onboarding.fillAllCustom': '请填寫所有自定義服務商字段',
  'onboarding.fillAllFields': '请填寫所有字段',
  'onboarding.saveConfigFailed': '保存配置失敗',
  'onboarding.modelPlaceholder': 'gpt-4o (手動輸入或獲取)',
  'onboarding.configureLLM': '配置 LLM',

  // ApprovalDialog
  'approval.title': '需要工具審批',
  'approval.allow': '允許',
  'approval.deny': '拒絕',
  'approval.alwaysAllow': '始終允許',

  // AskUserDialog
  'askUser.title': '問題',
  'askUser.submit': '提交',

  // Dialogs (About)
  'about.title': '關於 GGCode',
  'about.description': '終端 AI 編程助手',

  // PairingCodeDialog
  'pairing.title': '配對設備',
  'pairing.close': '關閉',

  // ShareDialog
  'share.title': '分享會話',
  'share.copy': '復制',
  'share.copied': '已復制!',
  'share.stopSharing': '停止分享',
  'share.generating': '生成中...',

  // IMManagement
  'im.title': 'IM 适配器',
  'im.addAdapter': '添加适配器',
  'im.noAdapters': '未配置 IM 适配器。點擊"添加适配器"開始。',
  'im.currentWorkspace': '当前工作區',
  'im.otherWorkspaces': '其他工作區',
  'im.unbound': '未綁定',
  'im.online': '在線',
  'im.offline': '離線',
  'im.mute': '静音',
  'im.unmute': '取消静音',
  'im.platform': '平台',
  'im.name': '名稱',
  'im.save': '保存',
  'im.cancel': '取消',
  'im.selectPlatform': '選擇平台...',

  // MCPServers
  'mcp.title': 'MCP 服務器',
  'mcp.add': '添加',
  'mcp.name': '名稱',
  'mcp.cancel': '取消',

  // FileBrowser
  'files.fileTooLarge': '文件過大无法預覽 (>2MB)',
  'files.binaryFile': '二進制文件',

  // DebugConsole
  'debug.title': '調試控制台',
  'debug.start': '開始',
  'debug.stop': '停止',
  'debug.clear': '清空',
  'debug.lines': '行',
  'debug.live': '● 實時',
  'debug.allCategories': '全部分類',
  'debug.filterPlaceholder': '過濾...',
  'debug.empty': '點擊“開始”捕獲調試日志',

  // TeamBoard
  'team.teams': '個團隊',
  'team.teammates': '個隊友',
  'team.tasks': '個任務',
  'team.startPrompt': '創建或啟動一個團隊以查看隊友和任務。',
  'team.noTeammates': '暂无隊友',
  'team.pending': '待處理',
  'team.inProgress': '進行中',
  'team.done': '已完成',
  'team.noPending': '没有待處理任務',
  'team.noInProgress': '没有被阻塞的任務',
  'team.noDone': '暂无已完成任務',

  // --- v1.3.143+ i18n gap fill ---

  // SettingsPage sections
  'settings.agentSafety': 'Agent 與安全',
  'settings.appearance': '外觀',
  'settings.integrations': '集成',
  'settings.diagnostics': '診斷',
  'settings.hooksTitle': '生命週期鉤子',
  'settings.hooksAdd': '+ 新增',
  'settings.hooksEdit': '編輯',
  'settings.hooksDelete': '刪除',
  'settings.hooksInject': '注入',
  'settings.hooksNone': '(無)',
  'settings.hooksMatch': '匹配 (* 符合全部)',
  'settings.hooksType': '類型 (command/http)',
  'settings.hooksCommand': '命令',
  'settings.hooksURL': 'URL',
  'settings.hooksSecret': '金鑰',
  'settings.hooksInjectLabel': '將輸出注入工具結果',
  'settings.hooksSaved': '鉤子已儲存',
  'settings.hooksErrorLoad': '載入鉤子錯誤: {0}',
  'settings.hooksErrorSave': '儲存錯誤: {0}',
  'settings.hooksOnUserMessage': '使用者訊息時',
  'settings.hooksPreToolUse': '工具使用前',
  'settings.hooksPostToolUse': '工具使用後',
  'settings.hooksOnAgentStop': 'Agent 停止時',
  'settings.hooksOnStreamStop': '串流停止時',
  'settings.hooksAddBtn': '新增',
  'settings.hooksEditBtn': '編輯',
  'settings.hooksCancelBtn': '取消',
  'settings.hooksSaveBtn': '儲存',
  'settings.hooksMatchMode': '匹配模式',
  'settings.hooksTesterTitle': '正則測試器',
  'settings.hooksTesterPlaceholder': '例如 run_command git push',
  'settings.hooksTestMatched': '匹配成功！',
  'settings.hooksTestNotMatched': '不匹配',
  'settings.hooksTestError': '錯誤: {0}',
  'settings.addCustomEndpoint': '添加自定義端點',
  'settings.configured': '已配置',
  'settings.saveLimits': '保存限制',
  'settings.refreshModels': '從 API 刷新模型',
  'settings.versionPlaceholder': '版本號...',
  'settings.endpointNamePlaceholder': '我的端點',

  // SettingsPage integrations checkboxes
  'settings.fileBrowser': '文件瀏覽器',
  'settings.contextUsage': '上下文使用量',
  'settings.mcpServers': 'MCP 服務器',
  'settings.imAdapters': 'IM 适配器',
  'settings.mobileShare': '移動端分享',
  'settings.contextPanel': '上下文面板',
  'settings.debugConsole': '調試控制台',
  'settings.aboutUpdates': '關於與更新',
  'settings.lspServers': '語言服務器',
  'settings.diagnosticsHint': '用於故障排除和支持的工具。這些會打開現有的桌面面板和對話框。',
  'settings.openConsole': '打開控制台',
  'settings.openAbout': '打開關於',
  'settings.debugConsoleDesc': '查看運行時日志和最近的桌面/后端診斷事件。',
  'settings.aboutDesc': '檢查應用版本、更新狀態、發布說明和支持鏈接。',
  'settings.anthropicOAuth': 'Anthropic OAuth',
  'settings.contextWindowMaxTokens': '上下文窗口與最大 Token',
  'settings.contextWindow': '上下文窗口',
  'settings.maxTokens': '最大輸出 Token',
  'settings.auto': '自動',
  'settings.sessionLimits': '會話級限制',
  'settings.sessionLimitsHint': '僅對當前會話生效。0 = 自動（使用端點/模型配置）。',
  'settings.sessionLimitsActive': '會話級覆蓋已生效',
  'settings.clickToEdit': '點擊編輯會話限制',
  'settings.invalidTokenFormat': '格式無效。請使用數字如 256000、256k 或 1m。',
  'settings.workspaceFileAccess': '工作區與文件访問',
  'settings.theme': '主題',
  'settings.themeLight': '浅色',
  'settings.themeDark': '深色',
  'settings.lspHint': '語言服務器協議（LSP）為 Agent 提供代碼智能功能：轉到定義、查找引用、懸停信息、診斷等。',
  'settings.lspNoServers': '此工作區未檢測到語言服務器。',
  'settings.lspConfigured': '(已配置)',
  'settings.lspNotAvailable': '未找到',
  'settings.lspInstall': '安裝:',
  'settings.extraHeaders': '額外请求頭',
  'settings.someProvidersHint': '某些提供商需要特定的请求頭。',
  'settings.saveAgentSettings': '保存 Agent 設置',
  'settings.name': '名稱',
  'settings.protocol': '協議',
  'settings.addEndpoint': '添加端點',
  'settings.testConnection': '測試',
  'settings.adding': '添加中...',
  'settings.lspServersShort': '語言服務器',

  // Sidebar aria labels
  'sidebar.searchAria': '搜索會話',
  'sidebar.clearAria': '清除',
  'sidebar.deleteSessionAria': '刪除會話',

  // ChatView tooltip extras
  'chat.editNickRoleTeam': '點擊編輯暱稱、角色和團隊',
  'chat.regenerate': '重新生成回復',
  'chat.copyResult': '復制結果',
  'chat.copyError': '復制錯誤',

  // Permission mode dropdown options

  // Common dropdown placeholders
  'common.chooseModel': '選擇模型...',

  // ChatView activity descriptions
  'chat.activity.reading': '讀取文件中...',
  'chat.activity.editing': '編輯代碼中...',
  'chat.activity.writing': '寫入文件中...',
  'chat.activity.running': '執行命令中...',
  'chat.activity.browsing': '瀏覽文件中...',
  'chat.activity.git': 'Git 操作中...',
  'chat.activity.analyzing': '分析代碼中...',
  'chat.activity.searching': '搜索網頁中...',
  'chat.activity.managing': '管理任務中...',
  'chat.activity.subagent': '運行子 Agent...',
  'chat.activity.waiting': '等待輸入...',
  'chat.activity.saving': '保存記憶中...',
  'chat.activity.planMode': '進入計劃模式...',

  // ChatView UI labels
  'chat.findPlaceholder': '在對話中查找...',
  'chat.sending': '發送中...',
  'chat.failedToSend': '發送失敗',
  'chat.nickname': '暱稱',
  'chat.role': '角色',
  'chat.team': '團隊',
  'chat.cancel': '取消',
  'chat.save': '保存',

  // ChatView button titles
  'chat.title.newSession': '新建會話',
  'chat.title.shareSession': '分享會話 (Cmd+Shift+S)',
  'chat.title.search': '在對話中搜索 (Cmd+F)',
  'chat.title.copyConversation': '復制對話到剪貼板',
  'chat.title.download': '下載為 Markdown 文件',
  'chat.title.scrollBottom': '滾動到底部',
  'chat.title.removeImage': '移除圖片',
  'chat.title.attachImage': '附加圖片',
  'chat.title.paste': '從剪貼板粘貼',
  'chat.title.cancel': '取消',
  'chat.title.send': '發送消息 (Enter)',
  'chat.title.copyReasoning': '復制推理過程',
  'chat.title.editResend': '編輯并重發',
  'chat.title.copyMessage': '復制消息',
  'chat.title.teamBoard': '打開任務面板',

  // MCPServers
  'mcp.transport': '傳輸方式',
  'mcp.transportStdio': '通過 stdin/stdout 的本地進程',
  'mcp.transportWs': '通過 WebSocket 的遠程服務器',
  'mcp.commandLabel': '命令',
  'mcp.argsOptional': '參數（可選，空格分隔）',
  'mcp.headersOptional': '请求頭（可選，每行一個: Key: Value）',
  'mcp.envVars': '環境變量（可選，每行一個: KEY=VALUE）',
  'mcp.login': '登錄',
  'mcp.reconnect': '重新連接',
  'mcp.resetOAuth': '重置 OAuth (切換賬號)',
  'mcp.remove': '移除',

  // Onboarding
  'onboarding.chooseVendor': '選擇服務商...',
  'onboarding.providerName': '服務商名稱',
  'onboarding.protocol': '協議',
  'onboarding.providerPlaceholder': '我的服務商...',
  'onboarding.chooseModel': '選擇模型...',
  'onboarding.chooseEndpoint': '選擇端點...',

  // LanChatView
  'lanchat.groupChat': '群聊',
  'lanchat.chat': '聊天',
  'lanchat.agentSuffix': ' (agent)',
  'lanchat.noMessagesBroadcast': '暂无消息。與局域網上的所有人開始對話吧。',
  'lanchat.noMessagesDM': '暂无私信。',
  'lanchat.broadcastPlaceholder': '廣播给所有人...',
  'lanchat.dmPlaceholder': '给 {name} 發消息...',

  // ApprovalDialog hints

  // AskUserDialog
  'askUser.typeAnswer': '輸入你的回答...',

  // IMManagement
  'im.editAdapter': '編輯: {name}',

  // FileBrowser
  'files.searchPlaceholder': '搜索文件...',
  'files.officeDocument': 'Office 文檔',
  'files.openExternal': '使用外部應用打開查看',
  'files.htmlPreview': 'HTML 預覽',

  // Dialogs / About

  // TopDragBar (window controls)

  // StatusBar
  'status.toggleContext': '切換上下文面板',

  // TeamBoard
  'team.boardTitle': '任務面板',

  // Common shared
  'common.dismiss': '關閉通知',
  'common.cancel': '取消',
  'common.save': '保存',
  'common.copyToClipboard': '復制到剪貼板',

  // FeatureCard descriptions and actions
  'settings.fileBrowserDesc': '在当前工作區中瀏覽文件。',
  'settings.openFiles': '打開文件',
  'settings.contextUsageDesc': '在運行長任務前檢查上下文使用情況和 Token/緩存總量。',
  'settings.openContext': '打開上下文',
  'settings.mcpDesc': '配置 Model Context Protocol 服務器和工具。',
  'settings.manageMCP': '管理 MCP',
  'settings.imDesc': '連接 Telegram、Slack、飛書、钉钉等聊天适配器。',
  'settings.manageIM': '管理 IM',
  'settings.mobileShareDesc': '通過中继共享活跃的桌面會話到移動端。',
  'settings.openShare': '打開共享',
  'settings.contextPanelDesc': '檢查活跃會話的上下文使用、Token 總量和緩存統計。',
  'settings.lspDesc': '查看檢測到的 Go、Rust、TypeScript、Python 等語言服務器。',
  'settings.viewStatus': '查看狀態',
  'settings.integrationsDesc': '從一個地方管理現有桌面功能。這些快捷方式會打開当前專用界面，不會更改任何后端配置。',
  'settings.vendorGemini': 'Google Gemini',

  // Dialog aria labels
  'common.toolApproval': '工具審批',
  'common.closeDialog': '關閉對話框',
  'common.cancelBtn': '取消',
  'common.searchPrev': '上一個 (Shift+Enter)',
  'common.searchNext': '下一個 (Enter)',
  'common.searchClose': '關閉 (Esc)',
  'common.filterByCategory': '按類別篩選',
  'common.addIMAdapter': '添加 IM 适配器',
  'common.lanChatApproval': 'LAN 聊天審批',
  'common.lanChatMessages': 'LAN 聊天消息',
  'common.mobilePairing': '移動端配對',
  'common.shareSession': '共享會話',
  'sidebar.shareWithMobile': '與移動端共享',
  'status.mobileConnected': '移動端已連接',
  'status.mobileConnectedAria': '移動設備已連接',
  'team.closeBoardAria': '關閉任務面板',
  'common.closeWindow': '關閉窗口',
  'common.minimizeWindow': '最小化窗口',
  'common.maximizeWindow': '最大化窗口',
  'common.restoreWindow': '恢復窗口',
  'common.pinWindow': '視窗置頂',
  'common.unpinWindow': '取消置頂',

  // SettingsPage option labels
  'settings.chooseVendor': '選擇供應商...',
  'settings.chooseEndpoint': '選擇端點...',
  'settings.modeSupervised': '受控模式（逐個確認工具）',
  'settings.modeBypass': '繞過模式（自動批準安全工具）',
  'settings.modeBypassAll': '繞過模式（自動批準大部分工具）',
  'settings.saveModeHint': '保存以持久化此默認模式。運行時模式更改仍遵循應用程序的正常模式處理。',

  // Dialogs
  'dialogs.newVersion': '發現新版本：v',
  'dialogs.download': '下載',
  'dialogs.upToDate': '已是最新版本 (v',
  'dialogs.productName': 'GGCode 桌面版',

  // TeamBoard
  // (team.boardTitle already exists above)

  // FileBrowser
  'files.renderingDiagram': '正在渲染圖錶...',
  // (files.officeDocument, files.openExternal already exist above)

  // ApprovalDialog keyboard hints
  'approval.always': '始終',

  // IMManagement
  // (im.editAdapter already exists above)

  // MCPServers
  'mcp.oauthRequired': '需要 OAuth 登錄',
  'mcp.oauthHint': '使用瀏覽器登錄以授權此 MCP 服務器。',

  // Onboarding
  'onboarding.customProvider': '+ 自定義供應商',
  'onboarding.a2a': 'A2A 服務',
  'onboarding.a2aDesc': '啟用代理間通訊（推薦）',
  'onboarding.languageTitle': '選擇您的語言',
  'onboarding.languageHint': '稍後可以在設定中更改',

  // SettingsPage mode descriptions (FeatureCard)
  'settings.agentSafetyHint': '查看桌面端 agent 当前的自主權限。此處僅匯總現有行為，不更改高級工具策略。',
  'settings.workspaceAccessHint': '文件工具在当前工作區和配置的允許目錄內操作。高級目錄編輯目前不在此處開放，以避免意外更改安全邊界。',
  'settings.appearanceHint': '自定義視觉主題。應用默認跟随系統偏好設置。',
  'settings.llmProvider': 'LLM 服務商',
  'settings.themeAuto': '自動（跟随系統）',
  'settings.themeShortcut': '快捷鍵 Cmd+Shift+T 可在浅色和深色之间切換。“自動”跟随你的系統設置。',
  'settings.globalHotkey': '全域快捷鍵',
  'settings.globalHotkeyHint': '使用 Option+Cmd+G 可從任何應用中顯示或隱藏 GGCode (macOS)。',
  'settings.enabled': '已啟用',
  'settings.disabled': '已禁用',
  'settings.modeTitlePlan': '只讀計劃模式',
  'settings.modeTitleAuto': '自動模式',
  'settings.modeTitleBypass': '繞過模式',
  'settings.modeTitleAutopilot': '自動驾驶模式',
  'settings.modeTitleSupervised': '監督模式',
  'settings.modeDescFullPlan': 'Agent 可以檢查文件和搜索工作區，但寫操作和命令被阻止。這是最安全的探索模式。',
  'settings.modeDescFullAuto': '安全操作自動執行，危險操作根據策略拒絕或升級處理。',
  'settings.modeDescFullBypass': '大部分操作允許執行，提示更少。僅在信任的工作區中使用此模式。',
  'settings.modeDescFullAutopilot': 'Agent 拥有高自主性，可自動執行多個步骤。在重要代碼上使用前请先查看工作區狀態。',
  'settings.modeDescFullSupervised': 'Agent 在執行未指定的工具操作前會先詢問。這是桌面端正常使用的平衡默認值。',

  // Toast messages
  'toast.settingsSaved': '設置已保存',
  'toast.settingsLoadFailed': '加載設置失敗：{error}',
  'toast.endpointsLoadFailed': '加載端點失敗：{error}',
  'toast.endpointDetailsLoadFailed': '加載端點詳情失敗：{error}',
  'toast.staticModelsLoadFailed': '加載静態模型失敗：{error}',
  'toast.modelsFetchFailed': '獲取模型失敗：{error}',
  'toast.settingsSaveFailed': '保存設置失敗：{error}',
  'toast.limitsMustBeNumbers': '上下文窗口和最大令牌數必须是數字',
  'toast.invalidTokenFormat': '格式無效。請使用數字如 256000、256k 或 1m。',
  'toast.endpointLimitsSaved': '端點限制已保存',
  'toast.endpointLimitsSaveFailed': '保存限制失敗：{error}',
  'toast.oauthLoginSuccess': 'Anthropic OAuth 登錄成功',
  'toast.oauthLoginFailed': 'OAuth 登錄失敗：{error}',
  'toast.oauthLogoutSuccess': 'Anthropic OAuth 已登出',
  'toast.oauthLogoutFailed': 'OAuth 登出失敗：{error}',
  'toast.impersonationApplied': '模擬設置已應用',
  'toast.impersonationFailed': '應用模擬失敗：{error}',
  'toast.endpointTestFailed': '端點測試失敗：{error}',
  'toast.endpointAdded': '端點已添加',
  'toast.endpointAddFailed': '添加端點失敗：{error}',
  'toast.copyConversationFailed': '復制對話失敗',
  'toast.imageProcessFailed': '處理附加圖片失敗',
  'toast.imageDropFailed': '處理拖放圖片失敗',
  'toast.imagePasteFailed': '粘貼圖片失敗',
  'toast.clipboardEmpty': '剪貼板為空或包含不支持的內容',
  'toast.clipboardPasteFailed': '從剪貼板粘貼失敗',
  'toast.reasoningNotSupported': '当前模型不支持推理強度設置',
  'toast.reasoningSwitchFailed': '切換推理強度失敗：{error}',
  'toast.sessionDeleted': '會話已刪除',
  'toast.sessionLocked': '此會話被其他實例鎖定',
  'toast.sessionDeleteFailed': '刪除會話失敗：{error}',
  'toast.sessionCreateFailed': '創建會話失敗：{error}',
  'toast.sessionOpenFailed': '打開會話失敗：{error}',
  'toast.copiedMessages': '已復制 {count} 條消息到剪貼板',

  // FileBrowser

  // ApprovalDialog risk levels
  'approval.riskHigh': '高風險',
  'approval.riskMedium': '中風險',
  'approval.riskLow': '低風險',

  // DebugConsole

  // Sidebar context menu

  // ChatView context menu & run stats

  // Dialogs links

  // MCPServers transports
  'mcp.transportHttp': 'HTTP / SSE',
  'mcp.descStdio': '通過 stdin/stdout 的本地進程',
  'mcp.descHttp': '通過 HTTP 的遠程服務器（Streamable HTTP / SSE）',
  'mcp.descWs': '通過 WebSocket 的遠程服務器',

  // IMManagement

  // PairingCodeDialog

  // LanChatView

  // ContextPanel

  // SettingsPage
  'settings.oauthConnected': '已連接',
  'settings.oauthNotConnected': '未連接',
  'settings.oauthLoggingIn': '正在登錄...',
  'settings.login': '登錄',
  'settings.failedFetchModels': '獲取模型失敗',

  // Sidebar context menu
  'sidebar.menuOpen': '打開',
  'sidebar.menuRename': '重命名',
  'sidebar.menuPin': '置頂',
  'sidebar.menuUnpin': '取消置頂',
  'sidebar.menuCopyId': '復制 ID',
  'sidebar.menuCopyMarkdown': '復制為 Markdown',
  'sidebar.menuExportMarkdown': '匯出為 Markdown',
  'sidebar.menuExportJSON': '匯出為 JSON',
  'sidebar.exportSaved': '工作階段已匯出',
  'sidebar.copiedMarkdown': 'Markdown 已復製到剪貼簿',
  'sidebar.deleteConfirm': '刪除此會話？此操作不可撤銷。',
  'sidebar.renamedToast': '會話已重命名',
  'sidebar.copyFailedToast': '復制失敗',

  // ChatView context menu
  'chat.menuCopy': '復制',
  'chat.menuEdit': '編輯',
  'chat.menuRegenerate': '重新生成',

  // ChatView run stats
  'chat.runStatDuration': '耗時',
  'chat.runStatTools': '工具',
  'chat.runStatFiles': '文件',
  'chat.runStatErrors': '錯誤',
  'chat.runStatTokens': '令牌',

  // Dialogs links
  'dialogs.github': 'GitHub',
  'dialogs.releases': '發布',
  'dialogs.issues': '問題',
  'dialogs.discord': 'Discord',
  'dialogs.checkUpdate': '檢查更新',
  'dialogs.checking': '檢查中...',

  // MCPServers oauth
  'mcp.oauthOpening': '正在打開瀏覽器進行 MCP 登錄...',
  'mcp.oauthCompleteBrowser': '在瀏覽器中完成登錄后返回此處。',
  'mcp.oauthReconnecting': '登錄完成。正在重新連接 MCP 服務器...',

  // AskUser
  'askUser.notesPlaceholder': '附加備注（可選）',
}

export default zh

