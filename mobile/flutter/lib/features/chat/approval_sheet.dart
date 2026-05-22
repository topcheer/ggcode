import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers/session_provider.dart';
import '../../core/l10n/app_localizations.dart';

class ApprovalSheet extends ConsumerWidget {
  final ApprovalInfo approval;
  const ApprovalSheet({super.key, required this.approval});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: const Color(0xFF1E1E2E),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: Colors.orange.withValues(alpha: 0.3)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(Icons.warning_amber, color: Colors.orange, size: 18),
              const SizedBox(width: 8),
              Text(
                t('approval.title'),
                style: TextStyle(
                  color: Colors.orange.withValues(alpha: 0.9),
                  fontWeight: FontWeight.w600,
                  fontSize: 14,
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            approval.toolName,
            style: const TextStyle(
                color: Colors.white, fontWeight: FontWeight.w600),
          ),
          const SizedBox(height: 4),
          Container(
            width: double.infinity,
            padding: const EdgeInsets.all(8),
            decoration: BoxDecoration(
              color: Colors.black.withValues(alpha: 0.3),
              borderRadius: BorderRadius.circular(6),
            ),
            child: Text(
              approval.input.length > 300
                  ? '${approval.input.substring(0, 300)}...'
                  : approval.input,
              style: const TextStyle(
                  color: Colors.white70, fontSize: 12, fontFamily: 'monospace'),
            ),
          ),
          const SizedBox(height: 12),
          Row(
            mainAxisAlignment: MainAxisAlignment.end,
            children: [
              TextButton(
                onPressed: () => _respond(ref, 'deny'),
                child: Text(t('approval.deny'),
                    style: const TextStyle(color: Colors.redAccent)),
              ),
              const SizedBox(width: 8),
              TextButton(
                onPressed: () => _respond(ref, 'allow'),
                child:
                    Text(t('approval.allow'), style: const TextStyle(color: Colors.green)),
              ),
              const SizedBox(width: 8),
              FilledButton(
                onPressed: () => _respond(ref, 'always_allow'),
                style:
                    FilledButton.styleFrom(backgroundColor: Colors.blueAccent),
                child: Text(t('approval.always_allow')),
              ),
            ],
          ),
        ],
      ),
    );
  }

  void _respond(WidgetRef ref, String decision) {
    ref.read(connectionProvider.notifier).send({
      'type': 'approval_response',
      'data': {'id': approval.id, 'decision': decision},
    });
    ref.read(approvalProvider.notifier).set(null);
  }
}
