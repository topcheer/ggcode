import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

import '../../core/providers/session_provider.dart';
import '../../core/l10n/app_localizations.dart';
import '../../core/theme/app_theme.dart';
import 'message_bubble.dart';
import 'approval_sheet.dart';
import 'input_bar.dart';
import '../status/status_bar.dart';

class ChatScreen extends ConsumerStatefulWidget {
  const ChatScreen({super.key});

  @override
  ConsumerState<ChatScreen> createState() => _ChatScreenState();
}

class _ChatScreenState extends ConsumerState<ChatScreen>
    with TickerProviderStateMixin {
  final _scrollController = ScrollController();
  final _inputController = TextEditingController();
  TabController? _tabController;
  List<String> _tabIds = [];
  List<String> _tabNames = [];
  int _currentTab = 0;
  bool _disposed = false;

  @override
  void dispose() {
    _disposed = true;
    _scrollController.dispose();
    _inputController.dispose();
    _tabController?.removeListener(_onTabChange);
    _tabController?.dispose();
    super.dispose();
  }

  void _onTabChange() {
    if (_disposed) return;
    final controller = _tabController;
    if (controller == null) return;
    if (!controller.indexIsChanging) {
      setState(() {
        _currentTab = controller.index;
      });
    }
  }

  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scrollController.hasClients && !_disposed) {
        _scrollController.jumpTo(_scrollController.position.maxScrollExtent);
      }
    });
  }

  void _updateTabs(List<String> newIds, List<String> newNames) {
    // Only rebuild if tab list actually changed
    if (_tabIds.length == newIds.length) {
      bool changed = false;
      for (int i = 0; i < _tabIds.length; i++) {
        if (_tabIds[i] != newIds[i]) {
          changed = true;
          break;
        }
      }
      if (!changed) return;
    }

    _tabController?.removeListener(_onTabChange);
    _tabController?.dispose();

    _tabIds = newIds;
    _tabNames = newNames;
    _tabController = TabController(length: _tabIds.length, vsync: this);
    _tabController!.addListener(_onTabChange);

    if (_currentTab >= _tabIds.length) {
      _currentTab = _tabIds.length - 1;
    }
    if (_currentTab < 0) _currentTab = 0;
  }

  @override
  Widget build(BuildContext context) {
    final allMessages = ref.watch(displayedMessagesProvider);
    final approval = ref.watch(approvalProvider);
    final info = ref.watch(displayedSessionInfoProvider);
    final agents = ref.watch(displayedSubagentProvider);
    final connState = ref.watch(connectionProvider);
    final cache = ref.watch(workspaceCacheProvider);
    final cacheNotifier = ref.read(workspaceCacheProvider.notifier);
    final isHistorical = ref.watch(isHistoricalViewProvider);
    final currentWorkspace = cache.selectedWorkspaceKey != null &&
            cache.selectedWorkspaceKey!.isNotEmpty
        ? cache.workspaces[cache.selectedWorkspaceKey!]
        : null;
    final currentSessions = currentWorkspace == null
        ? const <CachedSessionRecord>[]
        : cacheNotifier.sessionsForWorkspace(currentWorkspace.key);
    CachedSessionRecord? currentSession;
    if (cache.selectedSessionId != null &&
        cache.selectedSessionId!.isNotEmpty) {
      for (final session in currentSessions) {
        if (session.sessionId == cache.selectedSessionId) {
          currentSession = session;
          break;
        }
      }
    }

    // Build tab list: main + all agents (active first, then completed)
    final tabIds = <String>['main'];
    final tabNames = <String>['Chat'];
    for (final agent in agents.values) {
      if (!agent.completed) {
        tabIds.add(agent.agentId);
        tabNames.add(agent.name);
      }
    }
    for (final agent in agents.values) {
      if (agent.completed && !tabIds.contains(agent.agentId)) {
        tabIds.add(agent.agentId);
        tabNames.add(agent.name);
      }
    }

    _updateTabs(tabIds, tabNames);

    // Filter messages for current tab
    final currentSourceId =
        _currentTab < _tabIds.length ? _tabIds[_currentTab] : 'main';
    final messages = currentSourceId == 'main'
        ? allMessages.where((m) => m.sourceId == null).toList()
        : allMessages.where((m) => m.sourceId == currentSourceId).toList();

    // Auto-scroll on message changes
    ref.listen<List<ChatMessage>>(displayedMessagesProvider, (prev, next) {
      _scrollToBottom();
    });

    final showTabs = _tabIds.length > 1 && _tabController != null;

    return Scaffold(
      appBar: AppBar(
        leading: IconButton(
          icon: const Icon(Icons.qr_code_scanner),
          tooltip: 'Scan workspace',
          onPressed: _openWorkspaceScanner,
        ),
        title: Row(
          children: [
            Expanded(
              child: InkWell(
                onTap: _openWorkspaceSwitcher,
                borderRadius: BorderRadius.circular(16),
                child: Ink(
                  decoration: BoxDecoration(
                    color: AppColors.surface,
                    borderRadius: BorderRadius.circular(16),
                    border: Border.all(color: AppColors.border),
                  ),
                  child: Padding(
                    padding: const EdgeInsets.fromLTRB(12, 8, 10, 8),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Row(
                          children: [
                            Flexible(
                              child: Text(
                                currentWorkspace?.displayName ??
                                    info?.workspace.split('/').last ??
                                    'GGCode',
                                style: const TextStyle(fontSize: 16),
                                overflow: TextOverflow.ellipsis,
                              ),
                            ),
                            SizedBox(width: 4),
                            Icon(Icons.expand_more,
                                size: 16, color: AppColors.textSecondary),
                          ],
                        ),
                        if (currentSession != null)
                          Text(
                            currentSession.title,
                            style: TextStyle(
                              fontSize: 11,
                              color: AppColors.textMuted,
                            ),
                            overflow: TextOverflow.ellipsis,
                          ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
            SizedBox(width: 8),
            Text(
              info?.model ?? '',
              style: TextStyle(fontSize: 12, color: AppColors.textMuted),
            ),
          ],
        ),
        actions: [
          // Language toggle
          Consumer(builder: (context, ref, _) {
            final lang = ref.watch(languageProvider);
            return IconButton(
              icon: Text(
                lang == 'zh-CN' ? 'EN' : '中',
                style:
                    const TextStyle(fontSize: 13, fontWeight: FontWeight.bold),
              ),
              tooltip: t('settings.language'),
              onPressed: () {
                final newLang = lang == 'zh-CN' ? 'en' : 'zh-CN';
                ref.read(languageProvider.notifier).setLanguage(newLang);
                loadTranslations(newLang);
                // Notify desktop
                ref
                    .read(connectionProvider.notifier)
                    .service
                    ?.sendLanguageChange(newLang);
                setState(() {});
              },
            );
          }),
          // Theme toggle
          Consumer(builder: (context, ref, _) {
            final current = ref.watch(themeProvider);
            return PopupMenuButton<String>(
              tooltip: 'Theme: ${displayThemeName(current)}',
              icon: const Icon(Icons.palette_outlined, size: 20),
              initialValue: current,
              onSelected: (next) {
                ref.read(themeProvider.notifier).setTheme(next);
                ref
                    .read(connectionProvider.notifier)
                    .service
                    ?.sendThemeChange(next);
              },
              itemBuilder: (context) => [
                for (final theme in availableThemes)
                  PopupMenuItem<String>(
                    value: theme,
                    child: Row(
                      children: [
                        if (theme == current)
                          Icon(Icons.check, size: 16, color: AppColors.accent)
                        else
                          const SizedBox(width: 16),
                        const SizedBox(width: 8),
                        Text(displayThemeName(theme)),
                      ],
                    ),
                  ),
              ],
            );
          }),
          _ConnectionStatusIcon(
            status: connState.status,
            onDisconnectTap: () {
              final isDisconnected =
                  connState.status == ConnectionStatus.disconnected;
              showDialog(
                context: context,
                builder: (ctx) => AlertDialog(
                  backgroundColor: AppColors.surface,
                  title: Text(
                    isDisconnected
                        ? t('chat.back_to_connect')
                        : t('chat.disconnect_confirm'),
                    style: TextStyle(color: AppColors.textPrimary),
                  ),
                  content: Text(
                    isDisconnected
                        ? t('chat.disconnected_message')
                        : t('chat.disconnect_message'),
                    style: TextStyle(color: AppColors.textSecondary),
                  ),
                  actions: [
                    TextButton(
                      onPressed: () => Navigator.of(ctx).pop(),
                      child: Text(t('chat.cancel'),
                          style: TextStyle(color: AppColors.textSecondary)),
                    ),
                    TextButton(
                      onPressed: () async {
                        Navigator.of(ctx).pop();
                        await ref
                            .read(connectionProvider.notifier)
                            .leaveSession();
                      },
                      child: Text(
                        isDisconnected
                            ? t('chat.back_button')
                            : t('chat.disconnect_button'),
                        style: TextStyle(color: AppColors.danger),
                      ),
                    ),
                  ],
                ),
              );
            },
          ),
        ],
        bottom: showTabs
            ? TabBar(
                controller: _tabController,
                isScrollable: true,
                tabAlignment: TabAlignment.start,
                labelColor: Colors.white,
                unselectedLabelColor: Colors.white54,
                indicatorColor: Colors.blueAccent,
                labelStyle: const TextStyle(fontSize: 13),
                tabs: List.generate(_tabIds.length, (i) {
                  final id = _tabIds[i];
                  final name = _tabNames[i];
                  final agent = agents[id];
                  final isCompleted = agent?.completed ?? false;
                  final isRunning =
                      agent?.status == 'running' || agent?.status == 'thinking';

                  return Tab(
                    height: 36,
                    child: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        if (isRunning)
                          SizedBox(
                            width: 10,
                            height: 10,
                            child: CircularProgressIndicator(
                              strokeWidth: 1.5,
                              color: _parseColor(agent?.color ?? '#4CAF50'),
                            ),
                          )
                        else if (isCompleted)
                          Icon(
                            agent?.success ?? false
                                ? Icons.check_circle
                                : Icons.error,
                            size: 12,
                            color: agent?.success ?? false
                                ? Colors.green
                                : Colors.red,
                          ),
                        if (isRunning || isCompleted) const SizedBox(width: 4),
                        Text(
                          name,
                          style: TextStyle(
                            color: isCompleted ? Colors.white38 : Colors.white,
                          ),
                        ),
                        if (isCompleted && id != 'main')
                          GestureDetector(
                            onTap: () => _closeTab(id),
                            child: const Padding(
                              padding: EdgeInsets.only(left: 4),
                              child: Icon(Icons.close,
                                  size: 14, color: Colors.white38),
                            ),
                          ),
                      ],
                    ),
                  );
                }),
              )
            : null,
      ),
      body: Column(
        children: [
          const StatusBar(),
          if (isHistorical)
            _HistoricalSessionBanner(
              onReturnToLive: () async {
                final liveWorkspaceKey = cache.liveWorkspaceKey;
                final liveSessionId = cache.liveSessionId;
                if (liveWorkspaceKey == null ||
                    liveWorkspaceKey.isEmpty ||
                    liveSessionId == null ||
                    liveSessionId.isEmpty) {
                  return;
                }
                await ref
                    .read(workspaceCacheProvider.notifier)
                    .selectSession(liveWorkspaceKey, liveSessionId);
              },
            ),
          Expanded(
            child: ListView.builder(
              controller: _scrollController,
              padding: const EdgeInsets.fromLTRB(12, 10, 12, 6),
              itemCount: messages.length,
              itemBuilder: (context, index) {
                final msg = messages[index];
                if (msg.toolName != null) {
                  return _buildToolMessage(msg);
                }
                return MessageBubble(message: msg);
              },
            ),
          ),
          if (approval != null) ApprovalSheet(approval: approval),
          InputBar(controller: _inputController),
        ],
      ),
    );
  }

  void _closeTab(String agentId) {
    final agents = Map<String, SubagentInfo>.from(ref.read(subagentProvider));
    agents.remove(agentId);
    ref.read(subagentProvider.notifier).set(agents);

    final msgs = ref.read(chatProvider);
    ref
        .read(chatProvider.notifier)
        .set(msgs.where((m) => m.sourceId != agentId).toList());
  }

  Future<void> _openWorkspaceScanner() async {
    final scanned = await Navigator.of(context).push<String>(
      MaterialPageRoute(builder: (_) => const _WorkspaceScannerScreen()),
    );
    if (scanned == null || scanned.isEmpty) return;
    await ref.read(connectionProvider.notifier).connectScannedCode(scanned);
  }

  Future<void> _openWorkspaceSwitcher() async {
    final cache = ref.read(workspaceCacheProvider);
    final notifier = ref.read(workspaceCacheProvider.notifier);
    final workspaces = notifier.sortedWorkspaces();
    final selectedWorkspaceKey = cache.selectedWorkspaceKey;
    final sessionList =
        selectedWorkspaceKey == null || selectedWorkspaceKey.isEmpty
            ? const <CachedSessionRecord>[]
            : notifier.sessionsForWorkspace(selectedWorkspaceKey);
    await showModalBottomSheet<void>(
      context: context,
      backgroundColor: const Color(0xFF141421),
      showDragHandle: true,
      builder: (ctx) {
        return SafeArea(
          child: ListView(
            shrinkWrap: true,
            padding: EdgeInsets.fromLTRB(16, 8, 16, 24),
            children: [
              Text(
                t('workspace.switcher_title'),
                style: TextStyle(
                  color: AppColors.textPrimary.withValues(alpha: 0.95),
                  fontSize: 14,
                  fontWeight: FontWeight.w700,
                ),
              ),
              SizedBox(height: 8),
              for (final workspace in workspaces)
                ListTile(
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(14),
                  ),
                  tileColor:
                      AppColors.backgroundElevated.withValues(alpha: 0.5),
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(
                    workspace.key == cache.liveWorkspaceKey
                        ? Icons.radio_button_checked
                        : Icons.folder_open,
                    color: workspace.key == cache.selectedWorkspaceKey
                        ? Colors.blueAccent
                        : AppColors.textSecondary,
                  ),
                  title: Text(
                    workspace.displayName,
                    style: TextStyle(color: AppColors.textPrimary),
                  ),
                  subtitle: workspace.lastSessionId.isNotEmpty
                      ? Text(
                          'Session ${workspace.lastSessionId.substring(0, workspace.lastSessionId.length > 8 ? 8 : workspace.lastSessionId.length)}',
                          style: TextStyle(color: AppColors.textMuted),
                        )
                      : null,
                  trailing: workspace.key == cache.selectedWorkspaceKey
                      ? const Icon(Icons.check, color: Colors.blueAccent)
                      : null,
                  onTap: () async {
                    Navigator.of(ctx).pop();
                    await ref
                        .read(connectionProvider.notifier)
                        .connectWorkspace(workspace.key);
                  },
                ),
              if (selectedWorkspaceKey != null &&
                  selectedWorkspaceKey.isNotEmpty &&
                  sessionList.isNotEmpty) ...[
                SizedBox(height: 16),
                Text(
                  'Sessions',
                  style: TextStyle(
                    color: AppColors.textPrimary.withValues(alpha: 0.95),
                    fontSize: 14,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                SizedBox(height: 8),
                for (final session in sessionList)
                  ListTile(
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(14),
                    ),
                    tileColor:
                        AppColors.backgroundElevated.withValues(alpha: 0.5),
                    contentPadding: EdgeInsets.zero,
                    leading: Icon(
                      session.sessionId == cache.liveSessionId
                          ? Icons.bolt
                          : Icons.history,
                      color: session.sessionId == cache.selectedSessionId
                          ? Colors.blueAccent
                          : AppColors.textSecondary,
                    ),
                    title: Text(
                      session.title,
                      style: TextStyle(color: AppColors.textPrimary),
                    ),
                    subtitle: Text(
                      session.model.isNotEmpty
                          ? session.model
                          : session.provider,
                      style: TextStyle(color: AppColors.textMuted),
                    ),
                    trailing: session.sessionId == cache.selectedSessionId
                        ? const Icon(Icons.check, color: Colors.blueAccent)
                        : null,
                    onTap: () async {
                      Navigator.of(ctx).pop();
                      await ref
                          .read(workspaceCacheProvider.notifier)
                          .selectSession(
                              selectedWorkspaceKey, session.sessionId);
                    },
                  ),
              ],
            ],
          ),
        );
      },
    );
  }

  Widget _buildToolMessage(ChatMessage msg) {
    final prettyName = _prettyToolName(msg.toolName ?? 'tool');
    final title =
        (msg.toolDisplayName != null && msg.toolDisplayName!.isNotEmpty)
            ? msg.toolDisplayName!
            : prettyName;
    final hasResult = msg.toolResult != null && msg.toolResult!.isNotEmpty;

    return Container(
      margin: EdgeInsets.symmetric(vertical: 5),
      padding: EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: AppColors.surface,
        borderRadius: BorderRadius.circular(AppRadii.md),
        border: Border.all(color: AppColors.border),
        boxShadow: AppShadows.panel,
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Header line: icon + tool name + detail
          Row(
            children: [
              Icon(Icons.build,
                  size: 13, color: AppColors.accent.withValues(alpha: 0.85)),
              SizedBox(width: 4),
              Text(
                title,
                style: TextStyle(
                  color: AppColors.accent.withValues(alpha: 0.95),
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
              if (msg.toolDetail != null && msg.toolDetail!.isNotEmpty) ...[
                SizedBox(width: 6),
                Expanded(
                  child: Text(
                    msg.toolDetail!,
                    style: TextStyle(color: AppColors.textMuted, fontSize: 11),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
              if (hasResult)
                Icon(
                  msg.isToolError
                      ? Icons.error_outline
                      : Icons.check_circle_outline,
                  size: 13,
                  color: msg.isToolError
                      ? Colors.redAccent.withValues(alpha: 0.7)
                      : AppColors.success.withValues(alpha: 0.75),
                ),
            ],
          ),
          // Result: collapsed by default, tap to expand
          if (hasResult)
            _ToolResultCard(
              result: msg.toolResult!,
              isError: msg.isToolError,
            ),
        ],
      ),
    );
  }

  /// read_file → Read File, search_files → Search Files
  static String _prettyToolName(String name) {
    return name
        .split('_')
        .map((w) => w.isEmpty ? '' : '${w[0].toUpperCase()}${w.substring(1)}')
        .join(' ');
  }

  Color _parseColor(String hex) {
    try {
      return Color(int.parse(hex.replaceFirst('#', '0xFF')));
    } catch (_) {
      return Colors.green;
    }
  }
}

class _HistoricalSessionBanner extends StatelessWidget {
  final Future<void> Function() onReturnToLive;

  const _HistoricalSessionBanner({required this.onReturnToLive});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      margin: EdgeInsets.fromLTRB(8, 6, 8, 0),
      padding: EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: Colors.amber.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(AppRadii.sm),
        border: Border.all(color: AppColors.warning.withValues(alpha: 0.28)),
      ),
      child: Row(
        children: [
          Icon(Icons.history_toggle_off, color: AppColors.warning, size: 18),
          SizedBox(width: 8),
          Expanded(
            child: Text(
              t('session.cached_input_disabled'),
              style: TextStyle(
                color: AppColors.warning.withValues(alpha: 0.95),
                fontSize: 12,
              ),
            ),
          ),
          TextButton(
            onPressed: onReturnToLive,
            child: Text(t('chat.back_to_current')),
          ),
        ],
      ),
    );
  }
}

class _WorkspaceScannerScreen extends StatefulWidget {
  const _WorkspaceScannerScreen();

  @override
  State<_WorkspaceScannerScreen> createState() =>
      _WorkspaceScannerScreenState();
}

class _WorkspaceScannerScreenState extends State<_WorkspaceScannerScreen> {
  bool _handled = false;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.background,
      body: SafeArea(
        child: Column(
          children: [
            Padding(
              padding: EdgeInsets.fromLTRB(8, 8, 8, 4),
              child: Row(
                children: [
                  IconButton(
                    icon: Icon(Icons.close, color: AppColors.textPrimary),
                    onPressed: () => Navigator.of(context).pop(),
                  ),
                  Text(
                    t('workspace.scan_new'),
                    style: TextStyle(
                        color: AppColors.textPrimary,
                        fontSize: 18,
                        fontWeight: FontWeight.w600),
                  ),
                ],
              ),
            ),
            Expanded(
              child: MobileScanner(
                onDetect: (capture) {
                  if (_handled) return;
                  if (capture.barcodes.isEmpty) return;
                  final barcode = capture.barcodes.first;
                  final raw = barcode.rawValue?.trim() ?? '';
                  if (raw.isEmpty) return;
                  _handled = true;
                  Navigator.of(context).pop(raw);
                },
              ),
            ),
            Padding(
              padding: EdgeInsets.all(16),
              child: Text(
                t('workspace.scan_hint'),
                style: TextStyle(color: AppColors.textSecondary, fontSize: 13),
                textAlign: TextAlign.center,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// Collapsible tool result card. Default collapsed, tap to expand.
class _ToolResultCard extends StatefulWidget {
  final String result;
  final bool isError;
  const _ToolResultCard({required this.result, this.isError = false});

  @override
  State<_ToolResultCard> createState() => _ToolResultCardState();
}

class _ToolResultCardState extends State<_ToolResultCard> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final preview = widget.result.length > 120
        ? '${widget.result.substring(0, 120)}...'
        : widget.result;

    return GestureDetector(
      onTap: () => setState(() => _expanded = !_expanded),
      child: Container(
        width: double.infinity,
        margin: EdgeInsets.only(top: 4),
        padding: EdgeInsets.all(8),
        decoration: BoxDecoration(
          color: widget.isError
              ? AppColors.danger.withValues(alpha: 0.10)
              : AppColors.backgroundElevated,
          borderRadius: BorderRadius.circular(AppRadii.sm),
          border: Border.all(
            color: widget.isError
                ? AppColors.danger.withValues(alpha: 0.20)
                : AppColors.border,
          ),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(
                  _expanded ? Icons.expand_less : Icons.expand_more,
                  size: 14,
                  color: AppColors.textMuted,
                ),
                SizedBox(width: 2),
                Text(
                  widget.isError ? t('tool.error') : t('tool.result'),
                  style: TextStyle(
                    color: widget.isError
                        ? AppColors.danger.withValues(alpha: 0.9)
                        : AppColors.textMuted,
                    fontSize: 10,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ],
            ),
            SizedBox(height: 2),
            Text(
              _expanded ? widget.result : preview,
              style: TextStyle(
                color:
                    widget.isError ? AppColors.danger : AppColors.textSecondary,
                fontSize: 11,
                fontFamily: 'monospace',
              ),
              maxLines: _expanded ? null : 2,
              overflow: _expanded ? null : TextOverflow.ellipsis,
            ),
          ],
        ),
      ),
    );
  }
}

/// Connection status indicator shown in the AppBar.
/// - connected: green dot
/// - connecting: yellow spinner
/// - disconnected: red broken link icon (tappable to disconnect)
class _ConnectionStatusIcon extends StatelessWidget {
  final ConnectionStatus status;
  final VoidCallback onDisconnectTap;

  const _ConnectionStatusIcon({
    required this.status,
    required this.onDisconnectTap,
  });

  @override
  Widget build(BuildContext context) {
    switch (status) {
      case ConnectionStatus.connected:
        return Padding(
          padding: EdgeInsets.symmetric(horizontal: 12),
          child: Icon(Icons.cloud_done, size: 18, color: AppColors.success),
        );
      case ConnectionStatus.connecting:
        return Padding(
          padding: EdgeInsets.symmetric(horizontal: 12),
          child: SizedBox(
            width: 18,
            height: 18,
            child: CircularProgressIndicator(
              strokeWidth: 2,
              color: AppColors.warning,
            ),
          ),
        );
      case ConnectionStatus.disconnected:
        return IconButton(
          icon: Icon(Icons.cloud_off, size: 20, color: AppColors.danger),
          onPressed: onDisconnectTap,
          tooltip: t('connect.status_disconnected'),
        );
    }
  }
}
