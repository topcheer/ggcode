import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers/session_provider.dart';
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
    final allMessages = ref.watch(chatProvider);
    final approval = ref.watch(approvalProvider);
    final info = ref.watch(sessionInfoProvider);
    final agents = ref.watch(subagentProvider);

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
    ref.listen<List<ChatMessage>>(chatProvider, (prev, next) {
      _scrollToBottom();
    });

    final showTabs = _tabIds.length > 1 && _tabController != null;

    return Scaffold(
      appBar: AppBar(
        title: Row(
          children: [
            Expanded(
              child: Text(
                info?.workspace?.split('/').last ?? 'GGCode',
                style: const TextStyle(fontSize: 16),
              ),
            ),
            Text(
              info?.model ?? '',
              style: TextStyle(fontSize: 12, color: Colors.white.withOpacity(0.5)),
            ),
          ],
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.link_off, size: 20),
            onPressed: () {
              ref.read(connectionProvider.notifier).disconnect();
            },
            tooltip: 'Disconnect',
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
                              child: Icon(Icons.close, size: 14, color: Colors.white38),
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
    ref.read(subagentProvider.notifier).state = agents;

    final msgs = ref.read(chatProvider);
    ref.read(chatProvider.notifier).state =
        msgs.where((m) => m.sourceId != agentId).toList();
  }

  Widget _buildToolMessage(ChatMessage msg) {
    return Container(
      margin: const EdgeInsets.symmetric(vertical: 2),
      padding: const EdgeInsets.all(8),
      decoration: BoxDecoration(
        color: const Color(0xFF1A1A2E),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: Colors.white.withOpacity(0.1)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.build, size: 14, color: Colors.blueAccent.withOpacity(0.7)),
              const SizedBox(width: 4),
              Text(
                msg.toolName ?? 'tool',
                style: TextStyle(
                  color: Colors.blueAccent.withOpacity(0.9),
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
              if (msg.toolDetail != null) ...[
                const SizedBox(width: 4),
                Expanded(
                  child: Text(
                    msg.toolDetail!,
                    style: TextStyle(color: Colors.white.withOpacity(0.5), fontSize: 11),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ],
          ),
          if (msg.toolResult != null) ...[
            const SizedBox(height: 4),
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(6),
              decoration: BoxDecoration(
                color: msg.isToolError
                    ? Colors.red.withOpacity(0.1)
                    : Colors.green.withOpacity(0.05),
                borderRadius: BorderRadius.circular(4),
              ),
              child: Text(
                msg.toolResult!.length > 200
                    ? '${msg.toolResult!.substring(0, 200)}...'
                    : msg.toolResult!,
                style: TextStyle(
                  color: msg.isToolError ? Colors.redAccent : Colors.white70,
                  fontSize: 11,
                  fontFamily: 'monospace',
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }

  Color _parseColor(String hex) {
    try {
      return Color(int.parse(hex.replaceFirst('#', '0xFF')));
    } catch (_) {
      return Colors.green;
    }
  }
}
