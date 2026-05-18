import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers/session_provider.dart';
import 'message_bubble.dart';
import 'approval_sheet.dart';
import 'input_bar.dart';
import '../status/status_bar.dart';
import 'subagent_panel.dart';

class ChatScreen extends ConsumerStatefulWidget {
  const ChatScreen({super.key});

  @override
  ConsumerState<ChatScreen> createState() => _ChatScreenState();
}

class _ChatScreenState extends ConsumerState<ChatScreen> {
  final _scrollController = ScrollController();
  final _inputController = TextEditingController();

  @override
  void dispose() {
    _scrollController.dispose();
    _inputController.dispose();
    super.dispose();
  }

  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scrollController.hasClients) {
        _scrollController.jumpTo(_scrollController.position.maxScrollExtent);
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final messages = ref.watch(chatProvider);
    final approval = ref.watch(approvalProvider);
    final info = ref.watch(sessionInfoProvider);

    // Auto-scroll on new messages or content changes
    ref.listen<List<ChatMessage>>(chatProvider, (prev, next) {
      _scrollToBottom();
    });

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
      ),
      body: Stack(
        children: [
          Column(
            children: [
              const StatusBar(),
              // Messages
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
              // Approval sheet
              if (approval != null)
                ApprovalSheet(approval: approval),
              // Input
              InputBar(controller: _inputController),
            ],
          ),
          // Floating sub-agent panel
          const SubagentPanel(),
        ],
      ),
    );
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
}
