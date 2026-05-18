import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers/session_provider.dart';
import '../../core/models/protocol.dart';
import 'message_bubble.dart';
import 'tool_card.dart';
import 'approval_sheet.dart';
import 'input_bar.dart';
import '../status/status_bar.dart';

class ChatScreen extends ConsumerStatefulWidget {
  const ChatScreen({super.key});

  @override
  ConsumerState<ChatScreen> createState() => _ChatScreenState();
}

class _ChatScreenState extends ConsumerState<ChatScreen> {
  final _scrollController = ScrollController();

  @override
  void initState() {
    super.initState();
    // Listen for approval requests to show bottom sheet
    ref.listenManual(pendingApprovalProvider, (prev, next) {
      if (next != null && prev == null) {
        showModalBottomSheet(
          context: context,
          isDismissible: false,
          enableDrag: false,
          isScrollControlled: true,
          builder: (_) => const ApprovalSheet(),
        );
      }
    });
  }

  @override
  void dispose() {
    _scrollController.dispose();
    super.dispose();
  }

  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scrollController.hasClients) {
        _scrollController.animateTo(
          _scrollController.position.maxScrollExtent,
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
        );
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final chatState = ref.watch(chatProvider);
    final session = ref.watch(sessionInfoProvider);
    final theme = Theme.of(context);

    // Auto-scroll on new messages
    _scrollToBottom();

    return Scaffold(
      appBar: AppBar(
        title: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    session?.workspace ?? 'GGCode',
                    style: theme.textTheme.titleSmall?.copyWith(
                      fontWeight: FontWeight.bold,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  Text(
                    session?.model ?? 'Connecting...',
                    style: theme.textTheme.labelSmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ),
            ),
            _StatusDot(),
          ],
        ),
        actions: [
          // Mode selector
          PopupMenuButton<String>(
            icon: const Icon(Icons.tune),
            tooltip: 'Change mode',
            onSelected: (mode) {
              ref.read(currentModeProvider.notifier).state = mode;
              ref.read(connectionProvider).sendModeChange(mode);
            },
            itemBuilder: (context) => [
              const PopupMenuItem(value: 'supervised', child: Text('Supervised')),
              const PopupMenuItem(value: 'auto', child: Text('Auto')),
              const PopupMenuItem(value: 'bypass', child: Text('Bypass')),
              const PopupMenuItem(value: 'autopilot', child: Text('Autopilot')),
            ],
          ),
          // Disconnect
          IconButton(
            icon: const Icon(Icons.logout),
            tooltip: 'Disconnect',
            onPressed: () {
              ref.read(connectionProvider).disconnect();
            },
          ),
        ],
      ),
      body: Column(
        children: [
          // Messages
          Expanded(
            child: ListView.builder(
              controller: _scrollController,
              padding: const EdgeInsets.symmetric(vertical: 8),
              itemCount: chatState.messages.length +
                  chatState.streamingBuffers.length,
              itemBuilder: (context, index) {
                // Check if this is a streaming message
                final bufferKeys = chatState.streamingBuffers.keys.toList();
                if (index >= chatState.messages.length) {
                  final bufferIndex = index - chatState.messages.length;
                  if (bufferIndex < bufferKeys.length) {
                    final bufferId = bufferKeys[bufferIndex];
                    final streamingText =
                        chatState.streamingBuffers[bufferId] ?? '';
                    return _buildStreamingBubble(streamingText, theme);
                  }
                }

                final msg = chatState.messages[index];
                return _buildMessage(msg, chatState, theme);
              },
            ),
          ),

          // Status bar
          const StatusBar(),

          // Input bar
          const InputBar(),
        ],
      ),
    );
  }

  Widget _buildMessage(ChatMessage msg, ChatState chatState, ThemeData theme) {
    switch (msg.type) {
      case MessageType.user:
      case MessageType.agent:
        return MessageBubble(message: msg);
      case MessageType.toolCall:
        return ToolCard(toolData: msg.toolCall!);
      case MessageType.toolResult:
        return ToolCard(toolData: msg.toolResult!);
    }
  }

  Widget _buildStreamingBubble(String text, ThemeData theme) {
    if (text.isEmpty) {
      return Align(
        alignment: Alignment.centerLeft,
        child: Container(
          margin: const EdgeInsets.symmetric(vertical: 4, horizontal: 12),
          padding: const EdgeInsets.all(16),
          decoration: BoxDecoration(
            color: theme.colorScheme.surfaceContainerHigh,
            borderRadius: BorderRadius.circular(16),
          ),
          child: SizedBox(
            width: 20,
            height: 20,
            child: CircularProgressIndicator(
              strokeWidth: 2,
              color: theme.colorScheme.primary,
            ),
          ),
        ),
      );
    }

    return MessageBubble(
      message: ChatMessage(
        id: '__streaming__',
        type: MessageType.agent,
        text: text,
      ),
    );
  }
}

class _StatusDot extends ConsumerWidget {
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final status = ref.watch(agentStatusProvider);
    final color = _colorForStatus(status.status);
    return Tooltip(
      message: status.status,
      child: Container(
        width: 10,
        height: 10,
        decoration: BoxDecoration(
          color: color,
          shape: BoxShape.circle,
          boxShadow: [
            BoxShadow(color: color.withOpacity(0.4), blurRadius: 4),
          ],
        ),
      ),
    );
  }

  Color _colorForStatus(String status) {
    switch (status) {
      case 'thinking':
        return Colors.blue;
      case 'running':
        return Colors.orange;
      case 'waiting':
        return Colors.amber;
      case 'idle':
      default:
        return Colors.green;
    }
  }
}
