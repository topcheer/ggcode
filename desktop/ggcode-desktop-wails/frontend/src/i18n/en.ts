const en = {
  // NavRail
  'nav.chat': 'Chat',
  'nav.files': 'Files',
  'nav.im': 'IM Adapters',
  'nav.mcp': 'MCP Servers',
  'nav.settings': 'Settings',
  'nav.debug': 'Debug Console',
  'nav.cron': 'Scheduled Tasks',
  'cron.title': 'Scheduled Tasks',
  'cron.newTask': 'New Task',
  'cron.refresh': 'Refresh',
  'cron.loading': 'Loading...',
  'cron.empty': 'No scheduled tasks. Click "New Task" to create one.',
  'cron.jobDeleted': 'Job deleted',
  'cron.confirmDelete': 'Are you sure you want to delete this scheduled task?',
  'cron.deleteFailed': 'Delete failed: {0}',
  'cron.jobCreated': 'Job created',
  'cron.jobUpdated': 'Job updated',
  'cron.saveFailed': 'Save failed: {0}',
  'cron.failedLoad': 'Failed to load: {0}',
  'cron.failed': 'Failed: {0}',
  'cron.edit': 'Edit',
  'cron.delete': 'Delete',
  'cron.pause': 'Pause',
  'cron.resume': 'Resume',
  'cron.active': 'Active',
  'cron.paused': 'Paused',
  'cron.oneShot': 'One-shot',
  'cron.prompt': 'Prompt',
  'cron.cronExpr': 'Cron Expression',
  'cron.cronHint': 'Format: minute hour day month weekday. Examples: */5 * * * * (every 5min), 0 9 * * 1-5 (9am weekdays)',
  'cron.recurring': 'Recurring',
  'cron.queueIfBusy': 'Queue if agent is busy',
  'cron.editTask': 'Edit Task',
  'cron.newTaskTitle': 'New Task',
  'cron.cancel': 'Cancel',
  'cron.create': 'Create',
  'cron.update': 'Update',
  'cron.generateWithAI': 'Generate with AI',
  'cron.aiDescription': 'Describe what this task should do in natural language, and AI will generate a prompt for you.',
  'cron.aiPlaceholder': 'e.g. Check for new GitHub issues and summarize them',
  'cron.generate': 'Generate',
  'cron.generating': 'Generating...',
  'cron.aiGenFailed': 'AI generation failed: {0}',
  'cron.cronExprRequired': 'Cron expression is required',
  'cron.promptRequired': 'Prompt is required',
  'cron.descRequired': 'Please enter a description',
  'cron.promptPlaceholder': 'The prompt that will be sent to the agent when this task fires...',
  'cron.nextFire': 'Next fire',
  'cron.createdAt': 'Created',
  'cron.queueLabel': 'Queue if busy',
  'cron.id': 'ID',
  'cron.yes': 'Yes',
  'cron.no': 'No',

  // Sidebar
  'sidebar.sessions': 'Sessions',
  'sidebar.search': 'Search sessions...',
  'sidebar.newSession': 'New Session',
  'sidebar.untitled': 'Untitled',
  'sidebar.delete': 'Delete',
  'sidebar.group.pinned': 'Pinned',
  'sidebar.group.today': 'Today',
  'sidebar.group.yesterday': 'Yesterday',
  'sidebar.group.thisWeek': 'This Week',
  'sidebar.group.older': 'Older',
  'sidebar.time.justNow': 'just now',
  'sidebar.time.minutesAgo': '{n}m ago',
  'sidebar.time.hoursAgo': '{n}h ago',
  'sidebar.time.yesterday': 'yesterday',

  // StatusBar
  'status.ready': 'Ready',
  'status.thinking': 'Thinking...',
  'status.offline': 'Offline',
  'status.zoomIn': 'Zoom In (Cmd+)',
  'status.zoomOut': 'Zoom Out (Cmd-)',
  'status.zoomReset': 'Reset Zoom (Cmd+0)',

  // ChatView
  'chat.placeholder': 'Message ggcode...',
  'chat.agentWorking': 'Agent is working...',
  'chat.welcome.title': 'How can I help you today?',
  'chat.welcome.subtitle': 'Ask me anything about your code, or try one of these:',
  'chat.welcome.prompt.explain': 'Explain this codebase',
  'chat.welcome.prompt.review': 'Review recent changes',
  'chat.welcome.prompt.test': 'Write a test',
  'chat.welcome.prompt.debug': 'Help me debug an issue',
  'chat.inputHint': 'Enter to send • Shift+Enter for new line',

  // CommandPalette
  'cmd.recent': 'Recent',
  'cmd.placeholder': 'Type a command...',
  'cmd.noResults': 'No matching commands',
  'cmd.newSession': 'New Session',
  'cmd.searchSessions': 'Search Sessions',
  'cmd.shareSession': 'Share Session',
  'cmd.exportMarkdown': 'Export Session as Markdown',
  'cmd.exportJSON': 'Export Session as JSON',
  'cmd.copyMarkdown': 'Copy Session as Markdown',
  'cmd.toggleContext': 'Toggle Context Panel',
  'cmd.toggleTheme': 'Toggle Theme',
  'cmd.openSettings': 'Open Settings',
  'cmd.switchModel': 'Switch Model',
  'cmd.toggleSidebar': 'Toggle Sidebar',
  'cmd.prevSession': 'Previous Session',
  'cmd.nextSession': 'Next Session',
  'cmd.cat.session': 'Session',
  'cmd.cat.chat': 'Chat',
  'cmd.cat.settings': 'Settings',
  'cmd.cat.navigation': 'Navigation',
  'cmd.cat.view': 'Views',
  'cmd.goChat': 'Go to Chat',
  'cmd.goFiles': 'Go to Files',
  'cmd.goIM': 'Go to IM Management',
  'cmd.goMCP': 'Go to MCP Servers',
  'cmd.goLanChat': 'Go to LAN Chat',
  'cmd.goDebug': 'Go to Debug Console',
  'cmd.showShortcuts': 'Show Keyboard Shortcuts',
  'cmd.zoomIn': 'Zoom In',
  'cmd.zoomOut': 'Zoom Out',
  'cmd.zoomReset': 'Reset Zoom',
  'cmd.hintNavigate': 'navigate',
  'cmd.hintSelect': 'select',
  'cmd.hintClose': 'esc close',
  'cmd.results': '{count} results',

  // KeyboardShortcuts
  'kb.title': 'Keyboard Shortcuts',
  'kb.footer': 'Tip: Press ⌘K anytime to open the command palette',
  'kb.global': 'Global',
  'kb.chat': 'Chat',
  'kb.navigation': 'Navigation',
  'kb.views': 'Views',
  'kb.cmdPalette': 'Open Command Palette',
  'kb.toggleSidebar': 'Toggle Sidebar',
  'kb.toggleContext': 'Toggle Context Panel',
  'kb.toggleTheme': 'Toggle Dark/Light Theme',
  'kb.searchSessions': 'Search Sessions',
  'kb.prevSession': 'Previous Session',
  'kb.nextSession': 'Next Session',
  'kb.openSettings': 'Open Settings',
  'kb.globalHotkey': 'Global hotkey (show/hide app)',
  'kb.showShortcuts': 'Show This Help',
  'kb.closeDialog': 'Close Dialog',
  'kb.newSession': 'New Session',
  'kb.shareSession': 'Share Session',
  'kb.toggleIdentity': 'Toggle Agent Identity',
  'kb.searchInChat': 'Search in Conversation',
  'kb.sendMessage': 'Send Message',
  'kb.newline': 'New Line (in Input)',
  'kb.cancelGen': 'Cancel Generation',
  'kb.inputHistory': 'Recall Previous Input',
  'kb.navigateList': 'Navigate List',
  'kb.vimNav': 'Vim-style Navigation',
  'kb.selectItem': 'Select Item',
  'kb.goChat': 'Go to Chat',
  'kb.goFiles': 'Go to Files',
  'kb.goIM': 'Go to IM Adapters',
  'kb.goMCP': 'Go to MCP Servers',
  'kb.goSettings': 'Go to Settings',
  'kb.goDebug': 'Go to Debug Console',
  'kb.goLanChat': 'Go to LAN Chat',
  'kb.zoomIn': 'Zoom In',
  'kb.zoomOut': 'Zoom Out',
  'kb.zoomReset': 'Reset Zoom',

  // ContextPanel
  'context.title': 'Context',
  'context.usage': 'Context Usage',
  'context.input': 'Input',
  'context.output': 'Output',
  'context.cacheRead': 'Cache Read',
  'context.cacheWrite': 'Cache Write',
  'context.cacheHit': 'Cache Hit',
  'context.contextLabel': '{n}K context',
  'context.warning': 'Context window is {pct}% full. Consider starting a new session.',
  'context.critical': 'Context window is {pct}% full. Older messages may be dropped.',
  'context.estimatedCost': 'Est. Cost',
  'context.costDisclaimer': 'Estimated based on token usage and model pricing. Actual cost may vary.',
  'context.planIncluded': "Included in {plan}",

  // SettingsPage
  'settings.title': 'Settings',
  'settings.vendor': 'Vendor',
  'settings.endpoint': 'Endpoint',
  'settings.baseUrl': 'Base URL',
  'settings.apiKey': 'API Key',
  'settings.apiKeyPlaceholder': 'Enter your API key',
  'settings.model': 'Model',
  'settings.permissionMode': 'Permission Mode',
  'settings.language': 'Language',
  'settings.identity': 'Identity',
  'settings.save': 'Save',
  'settings.saved': 'Saved!',
  'settings.saving': 'Saving...',
  'settings.impersonate': 'Impersonate',
  'settings.impersonateHint': 'Test as a different persona',
  'settings.impersonateVersion': 'Version',
  'settings.impersonateApply': 'Apply',

  // Onboarding
  'onboarding.next': 'Next',
  'onboarding.back': 'Back',
  'onboarding.getStarted': 'Get Started',
  'onboarding.vendorHint': 'Select the AI provider you want to use',
  'onboarding.configHint': 'Adjust these settings or keep the defaults',
  'onboarding.welcome': 'Welcome to GGCode',
  'onboarding.selectDirHint': 'Select a project directory to get started',
  'onboarding.chooseDir': 'Choose Directory',
  'onboarding.modeSupervised': 'Supervised',
  'onboarding.modeSupervisedDesc': 'Review and approve every tool call before execution',
  'onboarding.modeBypass': 'Bypass',
  'onboarding.modeBypassDesc': 'Auto-approve all tools, no confirmation needed',
  'onboarding.fetchingModels': 'Fetching...',
  'onboarding.fetchModels': 'Fetch Models',
  'onboarding.noModelsFound': 'No models found. Enter a model name manually.',
  'onboarding.fetchModelsFailed': 'Failed to fetch models. You can enter the model name manually.',
  'onboarding.fillAllCustom': 'Please fill in all custom provider fields',
  'onboarding.fillAllFields': 'Please fill in all fields',
  'onboarding.saveConfigFailed': 'Failed to save config',
  'onboarding.modelPlaceholder': 'gpt-4o (enter manually or fetch)',
  'onboarding.configureLLM': 'Configure LLM',

  // ApprovalDialog
  'approval.title': 'Tool Approval Required',
  'approval.allow': 'Allow',
  'approval.deny': 'Deny',
  'approval.alwaysAllow': 'Always Allow',

  // AskUserDialog
  'askUser.title': 'Question',
  'askUser.submit': 'Submit',

  // Dialogs (About)
  'about.title': 'About GGCode',
  'about.description': 'AI coding agent for the terminal',

  // PairingCodeDialog
  'pairing.title': 'Pair Device',
  'pairing.close': 'Close',

  // ShareDialog
  'share.title': 'Share Session',
  'share.copy': 'Copy',
  'share.copied': 'Copied!',
  'share.stopSharing': 'Stop Sharing',
  'share.generating': 'Generating...',

  // IMManagement
  'im.title': 'IM Adapters',
  'im.addAdapter': 'Add Adapter',
  'im.noAdapters': 'No IM adapters configured. Click "Add Adapter" to get started.',
  'im.currentWorkspace': 'Current Workspace',
  'im.otherWorkspaces': 'Other Workspaces',
  'im.unbound': 'Unbound',
  'im.online': 'Online',
  'im.offline': 'Offline',
  'im.mute': 'Mute',
  'im.unmute': 'Unmute',
  'im.platform': 'Platform',
  'im.name': 'Name',
  'im.save': 'Save',
  'im.cancel': 'Cancel',
  'im.selectPlatform': 'Select platform...',

  // MCPServers
  'mcp.title': 'MCP Servers',
  'mcp.add': 'Add',
  'mcp.name': 'Name',
  'mcp.cancel': 'Cancel',

  // FileBrowser
  'files.fileTooLarge': 'File too large to preview (>2MB)',
  'files.binaryFile': 'Binary file',

  // DebugConsole
  'debug.title': 'Debug Console',
  'debug.start': 'Start',
  'debug.stop': 'Stop',
  'debug.clear': 'Clear',
  'debug.lines': 'lines',
  'debug.live': '● LIVE',
  'debug.allCategories': 'All categories',
  'debug.filterPlaceholder': 'Filter...',
  'debug.empty': 'Click "Start" to begin capturing debug logs',

  // TeamBoard
  'team.teams': 'teams',
  'team.teammates': 'teammates',
  'team.tasks': 'tasks',
  'team.startPrompt': 'Start or spawn a team to see teammates and tasks here.',
  'team.noTeammates': 'No teammates yet',
  'team.pending': 'Pending',
  'team.inProgress': 'In Progress',
  'team.done': 'Done',
  'team.noPending': 'No pending tasks',
  'team.noInProgress': 'Nobody is blocked here',
  'team.noDone': 'Nothing completed yet',

  // --- v1.3.143+ i18n gap fill ---

  // SettingsPage sections
  'settings.agentSafety': 'Agent & Safety',
  'settings.appearance': 'Appearance',
  'settings.integrations': 'Integrations',
  'settings.diagnostics': 'Diagnostics',
  'settings.hooksTitle': 'Lifecycle Hooks',
  'settings.hooksAdd': '+ Add',
  'settings.hooksEdit': 'edit',
  'settings.hooksDelete': 'del',
  'settings.hooksInject': 'inject',
  'settings.hooksNone': '(none)',
  'settings.hooksMatch': 'Match (* for all)',
  'settings.hooksType': 'Type (command/http)',
  'settings.hooksCommand': 'Command',
  'settings.hooksURL': 'URL',
  'settings.hooksSecret': 'Secret',
  'settings.hooksInjectLabel': 'Inject output into tool result',
  'settings.hooksSaved': 'Hooks saved',
  'settings.hooksErrorLoad': 'Error loading hooks: {0}',
  'settings.hooksErrorSave': 'Error saving: {0}',
  'settings.hooksOnUserMessage': 'On User Message',
  'settings.hooksPreToolUse': 'Pre Tool Use',
  'settings.hooksPostToolUse': 'Post Tool Use',
  'settings.hooksOnAgentStop': 'On Agent Stop',
  'settings.hooksOnStreamStop': 'On Stream Stop',
  'settings.hooksAddBtn': 'Add',
  'settings.hooksEditBtn': 'Edit',
  'settings.hooksCancelBtn': 'Cancel',
  'settings.hooksSaveBtn': 'Save',
  'settings.hooksMatchMode': 'Match Mode',
  'settings.hooksTesterTitle': 'Regex Tester',
  'settings.hooksTesterPlaceholder': 'e.g. run_command git push',
  'settings.hooksTestMatched': 'Matched!',
  'settings.hooksTestNotMatched': 'Not matched',
  'settings.hooksTestError': 'Error: {0}',
  'settings.addCustomEndpoint': 'Add Custom Endpoint',
  'settings.configured': 'Configured',
  'settings.saveLimits': 'Save limits',
  'settings.refreshModels': 'Refresh models from API',
  'settings.versionPlaceholder': 'Version string...',
  'settings.endpointNamePlaceholder': 'My Endpoint',

  // SettingsPage integrations checkboxes
  'settings.fileBrowser': 'File Browser',
  'settings.contextUsage': 'Context Usage',
  'settings.mcpServers': 'MCP Servers',
  'settings.imAdapters': 'IM Adapters',
  'settings.mobileShare': 'Mobile Share',
  'settings.contextPanel': 'Context Panel',
  'settings.debugConsole': 'Debug Console',
  'settings.aboutUpdates': 'About & Updates',
  'settings.lspServers': 'Language Servers',
  'settings.diagnosticsHint': 'Tools for troubleshooting and support. These open existing desktop panels and dialogs.',
  'settings.openConsole': 'Open Console',
  'settings.openAbout': 'Open About',
  'settings.debugConsoleDesc': 'View runtime logs and recent desktop/backend diagnostic events.',
  'settings.aboutDesc': 'Check the app version, update status, release notes, and support links.',
  'settings.anthropicOAuth': 'Anthropic OAuth',
  'settings.contextWindowMaxTokens': 'Context Window & Max Tokens',
  'settings.contextWindow': 'Context Window',
  'settings.maxTokens': 'Max Output Tokens',
  'settings.auto': 'auto',
  'settings.sessionLimits': 'Session Limits',
  'settings.sessionLimitsHint': 'Override for this session only. 0 = auto (use endpoint/model config).',
  'settings.sessionLimitsActive': 'Session-level overrides active',
  'settings.clickToEdit': 'Click to edit session limits',
  'settings.invalidTokenFormat': 'Invalid format. Use numbers like 256000, 256k, or 1m.',
  'settings.workspaceFileAccess': 'Workspace & file access',
  'settings.theme': 'Theme',
  'settings.themeLight': 'Light',
  'settings.themeDark': 'Dark',
  'settings.lspHint': 'Language Server Protocol (LSP) servers provide the agent with code intelligence: go-to-definition, find references, hover info, diagnostics, and more.',
  'settings.lspNoServers': 'No language servers detected in this workspace.',
  'settings.lspConfigured': '(configured)',
  'settings.lspNotAvailable': 'Not found',
  'settings.lspInstall': 'Install:',
  'settings.extraHeaders': 'Extra Headers',
  'settings.someProvidersHint': 'some providers require specific headers.',
  'settings.saveAgentSettings': 'Save agent settings',
  'settings.name': 'Name',
  'settings.protocol': 'Protocol',
  'settings.addEndpoint': 'Add Endpoint',
  'settings.testConnection': 'Test',
  'settings.adding': 'Adding...',
  'settings.lspServersShort': 'Language Servers',

  // Sidebar aria labels
  'sidebar.searchAria': 'Search sessions',
  'sidebar.clearAria': 'Clear',
  'sidebar.deleteSessionAria': 'Delete session',

  // ChatView tooltip extras
  'chat.editNickRoleTeam': 'Click to edit your nickname, role, and team',
  'chat.regenerate': 'Regenerate response',
  'chat.copyResult': 'Copy result',
  'chat.copyError': 'Copy error',

  // Permission mode dropdown options

  // Common dropdown placeholders
  'common.chooseModel': 'Choose model...',

  // ChatView activity descriptions
  'chat.activity.reading': 'Reading files...',
  'chat.activity.editing': 'Editing code...',
  'chat.activity.writing': 'Writing files...',
  'chat.activity.running': 'Running commands...',
  'chat.activity.browsing': 'Browsing files...',
  'chat.activity.git': 'Git operations...',
  'chat.activity.analyzing': 'Analyzing code...',
  'chat.activity.searching': 'Searching the web...',
  'chat.activity.managing': 'Managing tasks...',
  'chat.activity.subagent': 'Running sub-agent...',
  'chat.activity.waiting': 'Waiting for input...',
  'chat.activity.saving': 'Saving memory...',
  'chat.activity.planMode': 'Entering plan mode...',

  // ChatView UI labels
  'chat.findPlaceholder': 'Find in conversation...',
  'chat.sending': 'Sending...',
  'chat.failedToSend': 'Failed to send',
  'chat.nickname': 'Nickname',
  'chat.role': 'Role',
  'chat.team': 'Team',
  'chat.cancel': 'Cancel',
  'chat.save': 'Save',

  // ChatView button titles
  'chat.title.newSession': 'New session',
  'chat.title.shareSession': 'Share session (Cmd+Shift+S)',
  'chat.title.search': 'Search in conversation (Cmd+F)',
  'chat.title.copyConversation': 'Copy conversation to clipboard',
  'chat.title.download': 'Download as markdown file',
  'chat.title.scrollBottom': 'Scroll to bottom',
  'chat.title.removeImage': 'Remove image',
  'chat.title.attachImage': 'Attach image',
  'chat.title.paste': 'Paste from clipboard',
  'chat.title.cancel': 'Cancel',
  'chat.title.send': 'Send message (Enter)',
  'chat.title.copyReasoning': 'Copy reasoning',
  'chat.title.editResend': 'Edit & resend',
  'chat.title.copyMessage': 'Copy message',
  'chat.title.teamBoard': 'Open team board',

  // MCPServers
  'mcp.transport': 'Transport',
  'mcp.transportStdio': 'Local process via stdin/stdout',
  'mcp.transportWs': 'Remote server via WebSocket',
  'mcp.commandLabel': 'Command',
  'mcp.argsOptional': 'Arguments (optional, space-separated)',
  'mcp.headersOptional': 'Headers (optional, one per line: Key: Value)',
  'mcp.envVars': 'Environment Variables (optional, one per line: KEY=VALUE)',
  'mcp.login': 'Login',
  'mcp.reconnect': 'Reconnect',
  'mcp.resetOAuth': 'Reset OAuth (switch account)',
  'mcp.remove': 'Remove',

  // Onboarding
  'onboarding.chooseVendor': 'Choose vendor...',
  'onboarding.providerName': 'Provider Name',
  'onboarding.protocol': 'Protocol',
  'onboarding.providerPlaceholder': 'My Provider...',
  'onboarding.chooseModel': 'Choose model...',
  'onboarding.chooseEndpoint': 'Choose endpoint...',

  // LanChatView
  'lanchat.groupChat': 'Group Chat',
  'lanchat.chat': 'Chat',
  'lanchat.agentSuffix': ' (agent)',
  'lanchat.noMessagesBroadcast': 'No messages yet. Start a conversation with everyone on your network.',
  'lanchat.noMessagesDM': 'No direct messages yet.',
  'lanchat.broadcastPlaceholder': 'Broadcast to all...',
  'lanchat.dmPlaceholder': 'Message {name}...',

  // ApprovalDialog hints

  // AskUserDialog
  'askUser.typeAnswer': 'Type your answer...',

  // IMManagement
  'im.editAdapter': 'Edit: {name}',

  // FileBrowser
  'files.searchPlaceholder': 'Search files...',
  'files.officeDocument': 'Office document',
  'files.openExternal': 'Open with external application to view',
  'files.htmlPreview': 'HTML Preview',

  // Dialogs / About

  // TopDragBar (window controls)

  // StatusBar
  'status.toggleContext': 'Toggle context panel',

  // TeamBoard
  'team.boardTitle': 'Team Board',

  // Common shared
  'common.dismiss': 'Dismiss notification',
  'common.cancel': 'Cancel',
  'common.save': 'Save',
  'common.copyToClipboard': 'Copy to clipboard',

  // FeatureCard descriptions and actions
  'settings.fileBrowserDesc': 'Browse files in the current workspace using the existing desktop file browser.',
  'settings.openFiles': 'Open Files',
  'settings.contextUsageDesc': 'Inspect active context usage and token/cache totals before running long tasks.',
  'settings.openContext': 'Open Context',
  'settings.mcpDesc': 'Configure Model Context Protocol servers and tools.',
  'settings.manageMCP': 'Manage MCP',
  'settings.imDesc': 'Connect Telegram, Slack, Feishu, DingTalk and other chat adapters.',
  'settings.manageIM': 'Manage IM',
  'settings.mobileShareDesc': 'Share the active desktop session with mobile clients through the relay.',
  'settings.openShare': 'Open Share',
  'settings.contextPanelDesc': 'Inspect context usage, token totals, and cache statistics for the active session.',
  'settings.lspDesc': 'View detected LSP servers for Go, Rust, TypeScript, Python and more.',
  'settings.viewStatus': 'View Status',
  'settings.integrationsDesc': 'Manage existing desktop features from one place. These shortcuts open the current dedicated screens without changing any backend configuration semantics.',
  'settings.vendorGemini': 'Google Gemini',

  // Dialog aria labels
  'common.toolApproval': 'Tool approval',
  'common.closeDialog': 'Close dialog',
  'common.cancelBtn': 'Cancel',
  'common.searchPrev': 'Previous (Shift+Enter)',
  'common.searchNext': 'Next (Enter)',
  'common.searchClose': 'Close (Esc)',
  'common.filterByCategory': 'Filter by category',
  'common.addIMAdapter': 'Add IM adapter',
  'common.lanChatApproval': 'LAN Chat approval',
  'common.lanChatMessages': 'LAN Chat messages',
  'common.mobilePairing': 'Mobile pairing',
  'common.shareSession': 'Share session',
  'sidebar.shareWithMobile': 'Share with mobile',
  'status.mobileConnected': 'Mobile connected',
  'status.mobileConnectedAria': 'Mobile device connected',
  'team.closeBoardAria': 'Close team board',
  'common.closeWindow': 'Close window',
  'common.minimizeWindow': 'Minimize window',
  'common.maximizeWindow': 'Maximize window',
  'common.restoreWindow': 'Restore window',
  'common.pinWindow': 'Pin window on top',
  'common.unpinWindow': 'Unpin window',

  // SettingsPage option labels
  'settings.chooseVendor': 'Choose vendor...',
  'settings.chooseEndpoint': 'Choose endpoint...',
  'settings.modeSupervised': 'Supervised (confirm each tool)',
  'settings.modeBypass': 'Bypass (auto-approve safe tools)',
  'settings.modeBypassAll': 'Bypass (auto-approve most tools)',
  'settings.saveModeHint': 'Save to persist this default mode. Runtime mode changes still follow the app\'s normal mode handling.',

  // Dialogs
  'dialogs.newVersion': 'New version available: v',
  'dialogs.download': 'Download',
  'dialogs.upToDate': 'You\'re up to date (v',
  'dialogs.productName': 'GGCode Desktop',

  // TeamBoard
  // (team.boardTitle already exists above)

  // FileBrowser
  'files.renderingDiagram': 'Rendering diagram...',
  // (files.officeDocument, files.openExternal already exist above)

  // ApprovalDialog keyboard hints
  'approval.always': 'Always',

  // IMManagement
  // (im.editAdapter already exists above)

  // MCPServers
  'mcp.oauthRequired': 'OAuth login required',
  'mcp.oauthHint': 'Sign in with your browser to authorize this MCP server.',

  // Onboarding
  'onboarding.customProvider': '+ Custom Provider',
  'onboarding.a2a': 'A2A Service',
  'onboarding.a2aDesc': 'Enable agent-to-agent communication (recommended)',
  'onboarding.languageTitle': 'Choose Your Language',
  'onboarding.languageHint': 'You can change this later in Settings',

  // SettingsPage mode descriptions (FeatureCard)
  'settings.agentSafetyHint': 'Review how much autonomy the desktop agent currently has. This summarizes existing behavior without changing advanced tool policies.',
  'settings.workspaceAccessHint': 'File tools operate within the current workspace and configured allowed directories. Advanced allowed-directory editing is intentionally not exposed here yet to avoid changing safety boundaries accidentally.',
  'settings.appearanceHint': 'Customize the visual theme. The app follows your system preference by default.',
  'settings.llmProvider': 'LLM Provider',
  'settings.themeAuto': 'Auto (System)',
  'settings.themeShortcut': 'Shortcut: Cmd+Shift+T to toggle between light and dark. "Auto" follows your OS setting.',
  'settings.globalHotkey': 'Global Hotkey',
  'settings.globalHotkeyHint': 'Show or hide GGCode from anywhere with Option+Cmd+G (macOS).',
  'settings.enabled': 'Enabled',
  'settings.disabled': 'Disabled',
  'settings.modeTitlePlan': 'Read-only planning mode',
  'settings.modeTitleAuto': 'Auto mode',
  'settings.modeTitleBypass': 'Bypass mode',
  'settings.modeTitleAutopilot': 'Autopilot mode',
  'settings.modeTitleSupervised': 'Supervised mode',
  'settings.modeDescFullPlan': 'The agent can inspect files and search the workspace, but write actions and commands are blocked. This is the safest mode for exploration.',
  'settings.modeDescFullAuto': 'Safe operations can proceed automatically while dangerous actions are denied or require escalation according to policy.',
  'settings.modeDescFullBypass': 'Most operations are allowed with fewer prompts. Use this only in trusted workspaces where you are comfortable with faster execution.',
  'settings.modeDescFullAutopilot': 'The agent has high autonomy and can continue through many steps automatically. Review workspace state before using it on important code.',
  'settings.modeDescFullSupervised': 'The agent asks before unspecified tool actions. This is a balanced default for normal desktop use.',

  // Toast messages
  'toast.settingsSaved': 'Settings saved',
  'toast.settingsLoadFailed': 'Failed to load settings: {error}',
  'toast.endpointsLoadFailed': 'Failed to load endpoints: {error}',
  'toast.endpointDetailsLoadFailed': 'Failed to load endpoint details: {error}',
  'toast.staticModelsLoadFailed': 'Failed to load static models: {error}',
  'toast.modelsFetchFailed': 'Failed to fetch models: {error}',
  'toast.settingsSaveFailed': 'Failed to save settings: {error}',
  'toast.limitsMustBeNumbers': 'Context window and max tokens must be numbers',
  'toast.invalidTokenFormat': 'Invalid format. Use numbers like 256000, 256k, or 1m.',
  'toast.endpointLimitsSaved': 'Endpoint limits saved',
  'toast.endpointLimitsSaveFailed': 'Failed to save limits: {error}',
  'toast.oauthLoginSuccess': 'Anthropic OAuth login successful',
  'toast.oauthLoginFailed': 'OAuth login failed: {error}',
  'toast.oauthLogoutSuccess': 'Anthropic OAuth logged out',
  'toast.oauthLogoutFailed': 'OAuth logout failed: {error}',
  'toast.impersonationApplied': 'Impersonation settings applied',
  'toast.impersonationFailed': 'Failed to apply impersonation: {error}',
  'toast.endpointTestFailed': 'Endpoint test failed: {error}',
  'toast.endpointAdded': 'Endpoint added',
  'toast.endpointAddFailed': 'Failed to add endpoint: {error}',
  'toast.copyConversationFailed': 'Failed to copy conversation',
  'toast.imageProcessFailed': 'Failed to process attached image',
  'toast.imageDropFailed': 'Failed to process dropped image',
  'toast.imagePasteFailed': 'Failed to paste image',
  'toast.clipboardEmpty': 'Clipboard is empty or contains unsupported content',
  'toast.clipboardPasteFailed': 'Failed to paste from clipboard',
  'toast.reasoningNotSupported': 'Reasoning effort is not supported by the current model',
  'toast.reasoningSwitchFailed': 'Failed to switch reasoning effort: {error}',
  'toast.sessionDeleted': 'Session deleted',
  'toast.sessionLocked': 'This session is locked by another instance',
  'toast.sessionDeleteFailed': 'Failed to delete session: {error}',
  'toast.sessionCreateFailed': 'Failed to create session: {error}',
  'toast.sessionOpenFailed': 'Failed to open session: {error}',
  'toast.copiedMessages': 'Copied {count} messages to clipboard',

  // FileBrowser

  // ApprovalDialog risk levels
  'approval.riskHigh': 'High Risk',
  'approval.riskMedium': 'Medium Risk',
  'approval.riskLow': 'Low Risk',

  // DebugConsole

  // Sidebar context menu

  // ChatView context menu & run stats

  // Dialogs links

  // MCPServers transports
  'mcp.transportHttp': 'HTTP / SSE',
  'mcp.descStdio': 'Local process via stdin/stdout',
  'mcp.descHttp': 'Remote server via HTTP (Streamable HTTP / SSE)',
  'mcp.descWs': 'Remote server via WebSocket',

  // IMManagement

  // PairingCodeDialog

  // LanChatView

  // ContextPanel

  // SettingsPage
  'settings.oauthConnected': 'Connected',
  'settings.oauthNotConnected': 'Not connected',
  'settings.oauthLoggingIn': 'Logging in...',
  'settings.login': 'Login',
  'settings.failedFetchModels': 'Failed to fetch models',

  // Sidebar context menu
  'sidebar.menuOpen': 'Open',
  'sidebar.menuRename': 'Rename',
  'sidebar.menuPin': 'Pin',
  'sidebar.menuUnpin': 'Unpin',
  'sidebar.menuCopyId': 'Copy ID',
  'sidebar.menuCopyMarkdown': 'Copy as Markdown',
  'sidebar.menuExportMarkdown': 'Export as Markdown',
  'sidebar.menuExportJSON': 'Export as JSON',
  'sidebar.exportSaved': 'Session exported',
  'sidebar.copiedMarkdown': 'Markdown copied to clipboard',
  'sidebar.deleteConfirm': 'Delete this session? This cannot be undone.',
  'sidebar.renamedToast': 'Session renamed',
  'sidebar.copyFailedToast': 'Failed to copy',

  // ChatView context menu
  'chat.menuCopy': 'Copy',
  'chat.menuEdit': 'Edit',
  'chat.menuRegenerate': 'Regenerate',

  // ChatView run stats
  'chat.runStatDuration': 'Duration',
  'chat.runStatTools': 'Tools',
  'chat.runStatFiles': 'Files',
  'chat.runStatErrors': 'Errors',
  'chat.runStatTokens': 'Tokens',

  // Dialogs links
  'dialogs.github': 'GitHub',
  'dialogs.releases': 'Releases',
  'dialogs.issues': 'Issues',
  'dialogs.discord': 'Discord',
  'dialogs.checkUpdate': 'Check for Updates',
  'dialogs.checking': 'Checking...',

  // MCPServers oauth
  'mcp.oauthOpening': 'Opening browser for MCP login...',
  'mcp.oauthCompleteBrowser': 'Complete login in your browser, then return here.',
  'mcp.oauthReconnecting': 'Login complete. Reconnecting MCP server...',

  // AskUser
  'askUser.notesPlaceholder': 'Additional notes (optional)',
} as const

export type TranslationKey = keyof typeof en
export default en
