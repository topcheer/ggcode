import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers/session_provider.dart';
import '../../core/l10n/app_localizations.dart';
import '../../core/theme/app_theme.dart';

class ApprovalSheet extends ConsumerStatefulWidget {
  final ApprovalInfo approval;
  const ApprovalSheet({super.key, required this.approval});

  @override
  ConsumerState<ApprovalSheet> createState() => _ApprovalSheetState();
}

class _ApprovalSheetState extends ConsumerState<ApprovalSheet> {
  bool _responded = false;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.backgroundElevated,
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
            widget.approval.toolName,
            style: TextStyle(
                color: AppColors.textPrimary, fontWeight: FontWeight.w600),
          ),
          const SizedBox(height: 4),
          Container(
            width: double.infinity,
            padding: const EdgeInsets.all(8),
            decoration: BoxDecoration(
              color: AppColors.surfaceMuted,
              borderRadius: BorderRadius.circular(6),
            ),
            child: Text(
              widget.approval.input.length > 300
                  ? '${widget.approval.input.substring(0, 300)}...'
                  : widget.approval.input,
              style: TextStyle(
                  color: AppColors.textSecondary, fontSize: 12, fontFamily: 'monospace'),
            ),
          ),
          const SizedBox(height: 12),
          Wrap(
            alignment: WrapAlignment.end,
            runAlignment: WrapAlignment.end,
            crossAxisAlignment: WrapCrossAlignment.center,
            spacing: 8,
            runSpacing: 8,
            children: [
              TextButton(
                onPressed: () => _respond(ref, 'deny'),
                child: Text(t('approval.deny'),
                    style: const TextStyle(color: Colors.redAccent)),
              ),
              TextButton(
                onPressed: () => _respond(ref, 'allow'),
                child: Text(
                  t('approval.allow'),
                  style: const TextStyle(color: Colors.green),
                ),
              ),
              FilledButton(
                onPressed: () => _respond(ref, 'always_allow'),
                style:
                    FilledButton.styleFrom(backgroundColor: AppColors.accent),
                child: Text(t('approval.always_allow')),
              ),
            ],
          ),
        ],
      ),
    );
  }

  void _respond(WidgetRef ref, String decision) {
    // #1023: guard against multi-tap same-frame duplicate responses
    // (host is first-wins, this only avoids redundant messages).
    if (_responded) return;
    // #1023: when the tunnel is down the send is a silent no-op
    // (_socket?.add on a null socket) and the user would believe the
    // tool call was approved while the host blocks forever. Keep the
    // sheet visible and surface the failure instead of clearing it.
    if (ref.read(connectionProvider).status != ConnectionStatus.connected) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(t('approval.send_failed_reconnect')),
      ));
      return;
    }
    _responded = true;
    ref.read(connectionProvider.notifier).send({
      'type': 'approval_response',
      'data': {'id': widget.approval.id, 'decision': decision},
    });
    ref.read(approvalProvider.notifier).set(null);
  }
}
