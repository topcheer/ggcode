import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers/session_provider.dart';
import '../../core/l10n/app_localizations.dart';
import '../../core/theme/app_theme.dart';

class StatusBar extends ConsumerWidget {
  const StatusBar({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final status = ref.watch(displayedAgentStatusProvider);
    final message = ref.watch(displayedAgentStatusMessageProvider);
    final label = _statusLabel(status);
    final detail = _statusDetail(status, message);

    return Container(
      margin: const EdgeInsets.fromLTRB(12, 8, 12, 0),
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
      decoration: BoxDecoration(
        color: AppColors.surface,
        borderRadius: BorderRadius.circular(AppRadii.md),
        border: Border.all(color: AppColors.border),
        boxShadow: AppShadows.panel,
      ),
      child: Row(
        children: [
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
            decoration: BoxDecoration(
              color: _statusColor(status).withValues(alpha: 0.14),
              borderRadius: BorderRadius.circular(999),
              border: Border.all(
                  color: _statusColor(status).withValues(alpha: 0.25)),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
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
                  label,
                  style: TextStyle(
                    color: _statusColor(status),
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ],
            ),
          ),
          if (detail.isNotEmpty) ...[
            const SizedBox(width: 10),
            Expanded(
              child: Text(
                detail,
                style: const TextStyle(
                  color: AppColors.textSecondary,
                  fontSize: 12,
                ),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ],
        ],
      ),
    );
  }

  Color _statusColor(String status) {
    switch (status) {
      case 'idle':
        return AppColors.textMuted;
      case 'thinking':
        return AppColors.warning;
      case 'running':
        return AppColors.accent;
      case 'waiting':
        return const Color(0xFFFF9C54);
      case 'error':
        return AppColors.danger;
      default:
        return AppColors.textMuted;
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
