import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers/session_provider.dart';

class InputBar extends ConsumerStatefulWidget {
  final TextEditingController controller;
  const InputBar({super.key, required this.controller});

  @override
  ConsumerState<InputBar> createState() => _InputBarState();
}

class _InputBarState extends ConsumerState<InputBar> {
  @override
  void dispose() {
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final status = ref.watch(agentStatusProvider);
    final isRunning = status == 'thinking' || status == 'running';

    return Container(
      padding: const EdgeInsets.fromLTRB(8, 4, 8, 8),
      decoration: const BoxDecoration(
        color: Color(0xFF0D0D14),
        border: Border(top: BorderSide(color: Colors.white12)),
      ),
      child: Row(
        children: [
          Expanded(
            child: TextField(
              controller: widget.controller,
              style: const TextStyle(color: Colors.white, fontSize: 14),
              decoration: InputDecoration(
                hintText: isRunning ? 'Agent is working...' : 'Type a message...',
                hintStyle: TextStyle(color: Colors.white.withOpacity(0.3)),
                filled: true,
                fillColor: const Color(0xFF1A1A2E),
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(12),
                  borderSide: BorderSide.none,
                ),
                contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
              ),
              enabled: !isRunning,
              onSubmitted: (_) => _send(),
            ),
          ),
          const SizedBox(width: 8),
          if (isRunning)
            IconButton(
              icon: const Icon(Icons.stop_circle, color: Colors.redAccent),
              onPressed: () {
                ref.read(connectionProvider.notifier).send({
                  'type': 'interrupt',
                  'data': {},
                });
              },
              tooltip: 'Interrupt',
            )
          else
            IconButton(
              icon: const Icon(Icons.send, color: Colors.blueAccent),
              onPressed: _send,
              tooltip: 'Send',
            ),
        ],
      ),
    );
  }

  void _send() {
    final text = widget.controller.text.trim();
    if (text.isEmpty) return;
    widget.controller.clear();
    ref.read(chatProvider.notifier).addUserMessage(text);
  }
}
