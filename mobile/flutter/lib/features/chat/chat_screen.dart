import 'package:flutter/material.dart';
import 'package:flutter_markdown_plus/flutter_markdown_plus.dart';
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

  void _dismissComposerFocus() {
    FocusManager.instance.primaryFocus?.unfocus();
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
    ref.listen<ApprovalInfo?>(approvalProvider, (prev, next) {
      if (next != null && prev == null) {
        FocusManager.instance.primaryFocus?.unfocus();
      }
    });
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
                labelColor: AppColors.textPrimary,
                unselectedLabelColor: AppColors.textMuted,
                indicatorColor: AppColors.accent,
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
                            color: isCompleted
                                ? AppColors.textMuted
                                : AppColors.textPrimary,
                          ),
                        ),
                        if (isCompleted && id != 'main')
                          GestureDetector(
                            onTap: () => _closeTab(id),
                            child: Padding(
                              padding: EdgeInsets.only(left: 4),
                              child: Icon(
                                Icons.close,
                                size: 14,
                                color: AppColors.textMuted,
                              ),
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
          if (connState.relaySync != null)
            _RelaySyncBanner(sync: connState.relaySync!),
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
            child: Listener(
              behavior: HitTestBehavior.translucent,
              onPointerDown: (_) => _dismissComposerFocus(),
              child: ListView.builder(
                controller: _scrollController,
                keyboardDismissBehavior:
                    ScrollViewKeyboardDismissBehavior.onDrag,
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
    return _ToolMessageCard(message: msg, title: title);
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

class _ToolMessageCard extends StatefulWidget {
  final ChatMessage message;
  final String title;

  const _ToolMessageCard({
    required this.message,
    required this.title,
  });

  @override
  State<_ToolMessageCard> createState() => _ToolMessageCardState();
}

class _ToolMessageCardState extends State<_ToolMessageCard>
    with SingleTickerProviderStateMixin {
  late final AnimationController _pulse = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 1400),
  );

  bool get _isRunning => !widget.message.toolCompleted;

  @override
  void initState() {
    super.initState();
    _syncAnimation();
  }

  @override
  void didUpdateWidget(covariant _ToolMessageCard oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.message.toolCompleted != widget.message.toolCompleted) {
      _syncAnimation();
    }
  }

  void _syncAnimation() {
    if (_isRunning) {
      if (!_pulse.isAnimating) {
        _pulse.repeat(reverse: true);
      }
      return;
    }
    if (_pulse.isAnimating || _pulse.value != 0) {
      _pulse.stop();
      _pulse.value = 0;
    }
  }

  @override
  void dispose() {
    _pulse.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final detail = widget.message.toolDetail ?? '';
    final hasResultBody = widget.message.toolResult != null &&
        widget.message.toolResult!.isNotEmpty;

    return AnimatedBuilder(
      animation: _pulse,
      builder: (context, child) {
        final glowT =
            _isRunning ? Curves.easeInOut.transform(_pulse.value) : 0.0;
        final borderColor = _isRunning
            ? Color.lerp(
                AppColors.accent.withValues(alpha: 0.38),
                AppColors.accentSoft.withValues(alpha: 0.92),
                glowT,
              )!
            : widget.message.isToolError
                ? AppColors.danger.withValues(alpha: 0.34)
                : AppColors.border;
        final glowColor = Color.lerp(
          AppColors.accent.withValues(alpha: 0.10),
          AppColors.accentSoft.withValues(alpha: 0.26),
          glowT,
        )!;

        return Container(
          margin: const EdgeInsets.symmetric(vertical: 5),
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          decoration: BoxDecoration(
            color: _isRunning
                ? Color.lerp(AppColors.surface, AppColors.surfaceElevated, 0.42)
                : AppColors.surface,
            borderRadius: BorderRadius.circular(AppRadii.md),
            border: Border.all(color: borderColor, width: _isRunning ? 1.2 : 1),
            boxShadow: [
              ...AppShadows.panel,
              if (_isRunning)
                BoxShadow(
                  color: glowColor,
                  blurRadius: 16 + (glowT * 18),
                  spreadRadius: glowT * 1.5,
                ),
            ],
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Icon(
                    Icons.build,
                    size: 13,
                    color: (_isRunning
                            ? AppColors.accent
                            : (widget.message.isToolError
                                ? AppColors.danger
                                : AppColors.accent))
                        .withValues(alpha: 0.9),
                  ),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text.rich(
                      TextSpan(
                        children: [
                          TextSpan(
                            text: widget.title,
                            style: TextStyle(
                              color: AppColors.accent.withValues(alpha: 0.97),
                              fontSize: 12,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                          if (detail.isNotEmpty)
                            TextSpan(
                              text: '  $detail',
                              style: TextStyle(
                                color: AppColors.textMuted,
                                fontSize: 11,
                                fontWeight: FontWeight.w400,
                              ),
                            ),
                        ],
                      ),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  const SizedBox(width: 8),
                  _ToolStatusBadge(
                    message: widget.message,
                    isRunning: _isRunning,
                  ),
                ],
              ),
              if (hasResultBody)
                _ToolResultCard(
                  result: widget.message.toolResult!,
                  payload: widget.message.toolPayload,
                  payloadMode: widget.message.toolPayloadMode,
                  toolName: widget.message.toolName,
                  isError: widget.message.isToolError,
                ),
            ],
          ),
        );
      },
    );
  }
}

class _ToolStatusBadge extends StatelessWidget {
  final ChatMessage message;
  final bool isRunning;

  const _ToolStatusBadge({
    required this.message,
    required this.isRunning,
  });

  @override
  Widget build(BuildContext context) {
    if (isRunning) {
      return Container(
        key: Key('toolStatusWorking-${message.id}'),
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
        decoration: BoxDecoration(
          color: AppColors.accent.withValues(alpha: 0.12),
          borderRadius: BorderRadius.circular(999),
          border: Border.all(color: AppColors.accent.withValues(alpha: 0.28)),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            SizedBox(
              width: 10,
              height: 10,
              child: CircularProgressIndicator(
                strokeWidth: 1.6,
                color: AppColors.accent,
              ),
            ),
            const SizedBox(width: 6),
            Text(
              'Working',
              style: TextStyle(
                color: AppColors.accent.withValues(alpha: 0.95),
                fontSize: 10.5,
                fontWeight: FontWeight.w700,
              ),
            ),
          ],
        ),
      );
    }

    if (message.isToolError) {
      return Icon(
        Icons.error_outline,
        key: Key('toolStatusError-${message.id}'),
        size: 14,
        color: AppColors.danger.withValues(alpha: 0.88),
      );
    }

    return Icon(
      Icons.check_circle_outline,
      key: Key('toolStatusDone-${message.id}'),
      size: 14,
      color: AppColors.success.withValues(alpha: 0.8),
    );
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

class _RelaySyncBanner extends StatelessWidget {
  final RelaySyncState sync;

  const _RelaySyncBanner({required this.sync});

  @override
  Widget build(BuildContext context) {
    final title = _title();
    final detail = _detail();
    final isWarning = sync.stalled;

    return Container(
      key: const Key('relay-sync-banner'),
      width: double.infinity,
      margin: const EdgeInsets.fromLTRB(12, 10, 12, 0),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: isWarning
            ? AppColors.warning.withValues(alpha: 0.12)
            : AppColors.surfaceElevated,
        borderRadius: BorderRadius.circular(AppRadii.md),
        border: Border.all(
          color: isWarning
              ? AppColors.warning.withValues(alpha: 0.45)
              : AppColors.border,
        ),
      ),
      child: Row(
        children: [
          SizedBox(
            width: 16,
            height: 16,
            child: isWarning
                ? Icon(
                    Icons.sync_problem,
                    size: 16,
                    color: AppColors.warning,
                  )
                : const CircularProgressIndicator(strokeWidth: 2),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  title,
                  style: TextStyle(
                    color: AppColors.textPrimary,
                    fontSize: 13,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                if (detail.isNotEmpty) ...[
                  const SizedBox(height: 2),
                  Text(
                    detail,
                    style: TextStyle(
                      color: AppColors.textSecondary,
                      fontSize: 12,
                    ),
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }

  String _title() {
    if (sync.stalled) {
      return t('relay_sync.stalled_title');
    }
    switch (sync.phase) {
      case RelaySyncPhase.waiting:
        return t('relay_sync.waiting_title');
      case RelaySyncPhase.replaying:
        return t(sync.resumeMode == 'full_history'
            ? 'relay_sync.full_history_title'
            : 'relay_sync.replaying_title');
      case RelaySyncPhase.snapshot:
        return t('relay_sync.snapshot_title');
    }
  }

  String _detail() {
    if (sync.stalled) {
      return t(
        'relay_sync.stalled_detail',
        args: {
          'count': (sync.remainingReplayCount ?? 0).toString(),
        },
      );
    }
    switch (sync.phase) {
      case RelaySyncPhase.waiting:
        return t('relay_sync.waiting_detail');
      case RelaySyncPhase.replaying:
        return t(
          'relay_sync.replaying_detail',
          args: {
            'count': (sync.remainingReplayCount ?? 0).toString(),
          },
        );
      case RelaySyncPhase.snapshot:
        return t('relay_sync.snapshot_detail');
    }
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
  final _manualUrlController = TextEditingController();

  void _submitManualUrl() {
    final raw = _manualUrlController.text.trim();
    if (_handled || raw.isEmpty) return;
    _handled = true;
    Navigator.of(context).pop(raw);
  }

  @override
  void dispose() {
    _manualUrlController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final manualUrl = _manualUrlController.text.trim();
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
              padding: EdgeInsets.fromLTRB(
                16,
                16,
                16,
                16 + MediaQuery.of(context).viewInsets.bottom,
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    t('workspace.scan_hint'),
                    style:
                        TextStyle(color: AppColors.textSecondary, fontSize: 13),
                    textAlign: TextAlign.center,
                  ),
                  SizedBox(height: 16),
                  Text(
                    t('workspace.manual_hint'),
                    style: TextStyle(
                      color: AppColors.textMuted,
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  SizedBox(height: 8),
                  TextField(
                    key: const Key('workspaceScannerManualUrlField'),
                    controller: _manualUrlController,
                    style:
                        TextStyle(color: AppColors.textPrimary, fontSize: 14),
                    decoration: InputDecoration(
                      hintText: t('workspace.url_placeholder'),
                      hintStyle: TextStyle(color: AppColors.textMuted),
                      filled: true,
                      fillColor: AppColors.surface,
                      border: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(AppRadii.md),
                        borderSide: BorderSide.none,
                      ),
                      prefixIcon:
                          Icon(Icons.link, color: AppColors.textSecondary),
                      suffixIcon: manualUrl.isNotEmpty
                          ? IconButton(
                              icon: Icon(Icons.clear,
                                  color: AppColors.textSecondary),
                              onPressed: () {
                                _manualUrlController.clear();
                                setState(() {});
                              },
                            )
                          : null,
                    ),
                    onChanged: (_) => setState(() {}),
                    onSubmitted: (_) => _submitManualUrl(),
                  ),
                  SizedBox(height: 12),
                  FilledButton(
                    key: const Key('workspaceScannerManualConnectButton'),
                    onPressed: manualUrl.isEmpty ? null : _submitManualUrl,
                    style: FilledButton.styleFrom(
                      backgroundColor: AppColors.accent,
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(AppRadii.md),
                      ),
                    ),
                    child: Text(t('workspace.connect_direct')),
                  ),
                ],
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
  final String? payload;
  final String? payloadMode;
  final String? toolName;
  final bool isError;
  const _ToolResultCard({
    required this.result,
    this.payload,
    this.payloadMode,
    this.toolName,
    this.isError = false,
  });

  @override
  State<_ToolResultCard> createState() => _ToolResultCardState();
}

class _ToolResultCardState extends State<_ToolResultCard> {
  bool _expanded = false;

  bool get _rendersMarkdown =>
      !widget.isError && widget.toolName == 'swarm_task_create';

  bool get _usesPayload =>
      (widget.payload?.isNotEmpty ?? false) && widget.payload != widget.result;

  bool get _payloadIsText => widget.payloadMode == 'text' || widget.isError;

  bool get _structuredPayload =>
      (widget.payloadMode?.isNotEmpty ?? false) && !_payloadIsText;

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
            if (_rendersMarkdown)
              _buildMarkdownResult()
            else
              Text(
                _expanded && _usesPayload ? widget.payload! : preview,
                style: TextStyle(
                  color: widget.isError
                      ? AppColors.danger
                      : AppColors.textSecondary,
                  fontSize: 11,
                  fontFamily: _structuredPayload ? null : 'monospace',
                ),
                maxLines: _expanded ? null : 2,
                overflow: _expanded ? null : TextOverflow.ellipsis,
              ),
          ],
        ),
      ),
    );
  }

  Widget _buildMarkdownResult() {
    final body = MarkdownBody(
      data: widget.result,
      selectable: true,
      styleSheet: MarkdownStyleSheet(
        p: TextStyle(
          color: AppColors.textSecondary,
          fontSize: 11,
          height: 1.45,
        ),
        listBullet: TextStyle(
          color: AppColors.textSecondary,
          fontSize: 11,
        ),
        strong: TextStyle(
          color: AppColors.textPrimary,
          fontWeight: FontWeight.w700,
        ),
        em: TextStyle(
          color: AppColors.textSecondary,
          fontStyle: FontStyle.italic,
        ),
        code: TextStyle(
          color: AppColors.accent.withValues(alpha: 0.95),
          fontSize: 10.5,
          backgroundColor: AppColors.backgroundElevated,
        ),
        codeblockDecoration: BoxDecoration(
          color: AppColors.backgroundElevated,
          borderRadius: BorderRadius.circular(AppRadii.xs),
          border: Border.all(color: AppColors.border),
        ),
        codeblockPadding: const EdgeInsets.all(6),
      ),
    );
    if (_expanded) {
      return body;
    }
    return ClipRect(
      child: SizedBox(
        height: 48,
        child: body,
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
