import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

import '../../core/providers/session_provider.dart';
import '../../core/l10n/app_localizations.dart';
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
                borderRadius: BorderRadius.circular(8),
                child: Padding(
                  padding: const EdgeInsets.symmetric(vertical: 6),
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
                          const SizedBox(width: 4),
                          Icon(Icons.expand_more,
                              size: 16,
                              color: Colors.white.withValues(alpha: 0.7)),
                        ],
                      ),
                      if (currentSession != null)
                        Text(
                          currentSession.title,
                          style: TextStyle(
                            fontSize: 11,
                            color: Colors.white.withValues(alpha: 0.45),
                          ),
                          overflow: TextOverflow.ellipsis,
                        ),
                    ],
                  ),
                ),
              ),
            ),
            Text(
              info?.model ?? '',
              style: TextStyle(
                  fontSize: 12, color: Colors.white.withValues(alpha: 0.5)),
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
                style: const TextStyle(fontSize: 13, fontWeight: FontWeight.bold),
              ),
              tooltip: t('settings.language'),
              onPressed: () {
                final newLang = lang == 'zh-CN' ? 'en' : 'zh-CN';
                ref.read(languageProvider.notifier).setLanguage(newLang);
                loadTranslations(newLang);
                // Notify desktop
                ref.read(connectionProvider.notifier).service?.sendLanguageChange(newLang);
                setState(() {});
              },
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
                  backgroundColor: const Color(0xFF1A1A2E),
                  title: Text(
                    isDisconnected ? '返回连接页' : '断开连接',
                    style: const TextStyle(color: Colors.white),
                  ),
                  content: Text(
                    isDisconnected
                        ? '当前连接已经断开。返回后会回到扫码 / 连接界面。'
                        : '确定要断开与服务端的连接吗？',
                    style: TextStyle(color: Colors.white70),
                  ),
                  actions: [
                    TextButton(
                      onPressed: () => Navigator.of(ctx).pop(),
                      child: Text(t('chat.cancel'),
                          style: const TextStyle(color: Colors.white54)),
                    ),
                    TextButton(
                      onPressed: () async {
                        Navigator.of(ctx).pop();
                        await ref
                            .read(connectionProvider.notifier)
                            .leaveSession();
                      },
                      child: Text(
                        isDisconnected ? '返回' : '断开',
                        style: const TextStyle(color: Colors.redAccent),
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
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
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
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
            children: [
              Text(
                t('workspace.switcher_title'),
                style: TextStyle(
                  color: Colors.white.withValues(alpha: 0.9),
                  fontSize: 14,
                  fontWeight: FontWeight.w700,
                ),
              ),
              const SizedBox(height: 8),
              for (final workspace in workspaces)
                ListTile(
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(
                    workspace.key == cache.liveWorkspaceKey
                        ? Icons.radio_button_checked
                        : Icons.folder_open,
                    color: workspace.key == cache.selectedWorkspaceKey
                        ? Colors.blueAccent
                        : Colors.white54,
                  ),
                  title: Text(
                    workspace.displayName,
                    style: const TextStyle(color: Colors.white),
                  ),
                  subtitle: workspace.lastSessionId.isNotEmpty
                      ? Text(
                          'Session ${workspace.lastSessionId.substring(0, workspace.lastSessionId.length > 8 ? 8 : workspace.lastSessionId.length)}',
                          style: TextStyle(
                              color: Colors.white.withValues(alpha: 0.45)),
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
                const SizedBox(height: 16),
                Text(
                  'Sessions',
                  style: TextStyle(
                    color: Colors.white.withValues(alpha: 0.9),
                    fontSize: 14,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(height: 8),
                for (final session in sessionList)
                  ListTile(
                    contentPadding: EdgeInsets.zero,
                    leading: Icon(
                      session.sessionId == cache.liveSessionId
                          ? Icons.bolt
                          : Icons.history,
                      color: session.sessionId == cache.selectedSessionId
                          ? Colors.blueAccent
                          : Colors.white54,
                    ),
                    title: Text(
                      session.title,
                      style: const TextStyle(color: Colors.white),
                    ),
                    subtitle: Text(
                      session.model.isNotEmpty
                          ? session.model
                          : session.provider,
                      style: TextStyle(
                          color: Colors.white.withValues(alpha: 0.45)),
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
      margin: const EdgeInsets.symmetric(vertical: 2),
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        color: const Color(0xFF1A1A2E),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: Colors.white.withValues(alpha: 0.08)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Header line: icon + tool name + detail
          Row(
            children: [
              Icon(Icons.build,
                  size: 13, color: Colors.blueAccent.withValues(alpha: 0.7)),
              const SizedBox(width: 4),
              Text(
                title,
                style: TextStyle(
                  color: Colors.blueAccent.withValues(alpha: 0.9),
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
              if (msg.toolDetail != null && msg.toolDetail!.isNotEmpty) ...[
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    msg.toolDetail!,
                    style: TextStyle(
                        color: Colors.white.withValues(alpha: 0.4),
                        fontSize: 11),
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
                      : Colors.green.withValues(alpha: 0.6),
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
      margin: const EdgeInsets.fromLTRB(8, 6, 8, 0),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: Colors.amber.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: Colors.amber.withValues(alpha: 0.2)),
      ),
      child: Row(
        children: [
          const Icon(Icons.history_toggle_off, color: Colors.amber, size: 18),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              '当前查看的是缓存的历史 session，输入已禁用。',
              style: TextStyle(
                color: Colors.amber.shade100,
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
      backgroundColor: const Color(0xFF0D0D14),
      body: SafeArea(
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.all(8),
              child: Row(
                children: [
                  IconButton(
                    icon: const Icon(Icons.close, color: Colors.white),
                    onPressed: () => Navigator.of(context).pop(),
                  ),
                  Text(
                    t('workspace.scan_new'),
                    style: const TextStyle(
                        color: Colors.white,
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
              padding: const EdgeInsets.all(16),
              child: Text(
                '扫描 GGCode 桌面端展示的二维码，立即切换到对应 workspace。',
                style: TextStyle(
                    color: Colors.white.withValues(alpha: 0.5), fontSize: 13),
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
        margin: const EdgeInsets.only(top: 4),
        padding: const EdgeInsets.all(6),
        decoration: BoxDecoration(
          color: widget.isError
              ? Colors.red.withValues(alpha: 0.08)
              : Colors.white.withValues(alpha: 0.03),
          borderRadius: BorderRadius.circular(4),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(
                  _expanded ? Icons.expand_less : Icons.expand_more,
                  size: 14,
                  color: Colors.white38,
                ),
                const SizedBox(width: 2),
                Text(
                  widget.isError ? t('tool.error') : t('tool.result'),
                  style: TextStyle(
                    color: widget.isError
                        ? Colors.redAccent.withValues(alpha: 0.8)
                        : Colors.white38,
                    fontSize: 10,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 2),
            Text(
              _expanded ? widget.result : preview,
              style: TextStyle(
                color: widget.isError ? Colors.redAccent : Colors.white60,
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
          padding: const EdgeInsets.symmetric(horizontal: 12),
          child: Icon(Icons.cloud_done, size: 18, color: Colors.greenAccent),
        );
      case ConnectionStatus.connecting:
        return Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12),
          child: SizedBox(
            width: 18,
            height: 18,
            child: CircularProgressIndicator(
              strokeWidth: 2,
              color: Colors.orangeAccent,
            ),
          ),
        );
      case ConnectionStatus.disconnected:
        return IconButton(
          icon: const Icon(Icons.cloud_off, size: 20, color: Colors.redAccent),
          onPressed: onDisconnectTap,
          tooltip: t('connect.status_disconnected'),
        );
    }
  }
}
