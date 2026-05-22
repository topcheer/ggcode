import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers/session_provider.dart';
import '../../core/l10n/app_localizations.dart';

class StatusBar extends ConsumerWidget {
  const StatusBar({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final status = ref.watch(displayedAgentStatusProvider);
    final message = ref.watch(displayedAgentStatusMessageProvider);
    final label = _statusLabel(status);
    final detail = _statusDetail(status, message);

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
            detail.isNotEmpty ? '$label · $detail' : label,
            style: TextStyle(
              color: Colors.white.withValues(alpha: 0.7),
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
        return t('status.idle');
      case 'thinking':
        return t('status.thinking');
      case 'running':
        return t('status.running');
      case 'waiting':
        return t('status.approval_needed');
      case 'error':
        return t('tool.error');
      default:
        return status;
    }
  }

  String _statusDetail(String status, String message) {
    final trimmed = message.trim();
    if (trimmed.isEmpty) return '';
    final normalized = trimmed.toLowerCase();
    if ((status == 'idle' && normalized == 'ready') ||
        (status == 'thinking' && normalized == 'processing')) {
      return '';
    }
    return trimmed;
  }
}
