import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers/session_provider.dart';

class StatusBar extends ConsumerWidget {
  const StatusBar({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final status = ref.watch(agentStatusProvider);
    final message = ref.watch(agentStatusMessageProvider);

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      color: const Color(0xFF1A1A2E),
      child: Row(
        children: [
          SizedBox(
            width: 8,
            height: 8,
            child: DecoratedBox(
              decoration: BoxDecoration(
                color: _statusColor(status),
                shape: BoxShape.circle,
              ),
            ),
          ),
          const SizedBox(width: 8),
          Text(
            message.isNotEmpty ? message : _statusLabel(status),
            style: TextStyle(
              color: Colors.white.withOpacity(0.7),
              fontSize: 12,
            ),
          ),
        ],
      ),
    );
  }

  Color _statusColor(String status) {
    switch (status) {
      case 'idle':
        return Colors.grey;
      case 'thinking':
        return Colors.amber;
      case 'running':
        return Colors.blue;
      case 'waiting':
        return Colors.orange;
      case 'error':
        return Colors.red;
      default:
        return Colors.grey;
    }
  }

  String _statusLabel(String status) {
    switch (status) {
      case 'idle':
        return 'Ready';
      case 'thinking':
        return 'Thinking...';
      case 'running':
        return 'Working...';
      case 'waiting':
        return 'Waiting for approval';
      case 'error':
        return 'Error';
      default:
        return status;
    }
  }
}
