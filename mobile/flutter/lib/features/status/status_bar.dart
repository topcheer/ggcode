import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers/session_provider.dart';
import '../../core/models/protocol.dart';

class StatusBar extends ConsumerWidget {
  const StatusBar({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final status = ref.watch(agentStatusProvider);
    final theme = Theme.of(context);

    final (color, icon, text) = _statusInfo(status);

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
      decoration: BoxDecoration(
        color: color.withOpacity(0.15),
        border: Border(
          top: BorderSide(color: color.withOpacity(0.3)),
        ),
      ),
      child: Row(
        children: [
          SizedBox(
            width: 8,
            height: 8,
            child: DecoratedBox(
              decoration: BoxDecoration(
                color: color,
                shape: BoxShape.circle,
              ),
            ),
          ),
          const SizedBox(width: 8),
          Icon(icon, size: 14, color: color),
          const SizedBox(width: 6),
          Expanded(
            child: Text(
              text,
              style: theme.textTheme.bodySmall?.copyWith(
                color: color,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
        ],
      ),
    );
  }

  (Color, IconData, String) _statusInfo(AgentStatus status) {
    switch (status.status) {
      case 'thinking':
        return (Colors.blue.shade300, Icons.psychology, 'Thinking...');
      case 'running':
        final msg = status.message.isNotEmpty
            ? 'Running ${status.message}...'
            : 'Running...';
        return (Colors.orange.shade300, Icons.play_arrow, msg);
      case 'waiting':
        return (Colors.amber.shade300, Icons.hourglass_top, 'Waiting for approval...');
      case 'idle':
      default:
        return (Colors.green.shade300, Icons.check_circle_outline, 'Ready');
    }
  }
}
