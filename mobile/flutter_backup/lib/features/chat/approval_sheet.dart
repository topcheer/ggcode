import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers/session_provider.dart';
import '../../core/models/protocol.dart';

class ApprovalSheet extends ConsumerWidget {
  const ApprovalSheet({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final approval = ref.watch(pendingApprovalProvider);
    if (approval == null) return const SizedBox.shrink();

    final theme = Theme.of(context);

    return Container(
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHigh,
        borderRadius: const BorderRadius.vertical(top: Radius.circular(20)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Center(
            child: Container(
              width: 40,
              height: 4,
              decoration: BoxDecoration(
                color: theme.colorScheme.outlineVariant,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
          const SizedBox(height: 16),
          Row(
            children: [
              Icon(
                Icons.shield_outlined,
                color: theme.colorScheme.error,
                size: 28,
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Text(
                  'Approval Required',
                  style: theme.textTheme.titleMedium?.copyWith(
                    fontWeight: FontWeight.bold,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 16),
          _buildInfoRow(context, 'Tool', approval.toolName),
          const SizedBox(height: 12),
          Text(
            'Input:',
            style: theme.textTheme.labelMedium,
          ),
          const SizedBox(height: 4),
          Container(
            width: double.infinity,
            padding: const EdgeInsets.all(12),
            constraints: const BoxConstraints(maxHeight: 200),
            decoration: BoxDecoration(
              color: theme.colorScheme.surfaceContainerHighest,
              borderRadius: BorderRadius.circular(8),
              border: Border.all(color: theme.colorScheme.outlineVariant),
            ),
            child: SingleChildScrollView(
              child: SelectableText(
                approval.input,
                style: TextStyle(
                  fontFamily: 'monospace',
                  fontSize: 13,
                  color: theme.colorScheme.onSurface,
                ),
              ),
            ),
          ),
          const SizedBox(height: 20),
          Row(
            children: [
              Expanded(
                child: OutlinedButton(
                  onPressed: () {
                    ref.read(connectionProvider).sendApprovalResponse(
                        approval.id, 'deny');
                    ref.read(pendingApprovalProvider.notifier).state = null;
                    Navigator.pop(context);
                  },
                  style: OutlinedButton.styleFrom(
                    foregroundColor: theme.colorScheme.error,
                    side: BorderSide(color: theme.colorScheme.error),
                    padding: const EdgeInsets.symmetric(vertical: 12),
                  ),
                  child: const Text('Deny'),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: FilledButton(
                  onPressed: () {
                    ref.read(connectionProvider).sendApprovalResponse(
                        approval.id, 'allow');
                    ref.read(pendingApprovalProvider.notifier).state = null;
                    Navigator.pop(context);
                  },
                  style: FilledButton.styleFrom(
                    padding: const EdgeInsets.symmetric(vertical: 12),
                  ),
                  child: const Text('Allow'),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: FilledButton.tonal(
                  onPressed: () {
                    ref.read(connectionProvider).sendApprovalResponse(
                        approval.id, 'always_allow');
                    ref.read(pendingApprovalProvider.notifier).state = null;
                    Navigator.pop(context);
                  },
                  style: FilledButton.tonalStyleFrom(
                    padding: const EdgeInsets.symmetric(vertical: 12),
                  ),
                  child: const Text(
                    'Always',
                    style: TextStyle(fontSize: 12),
                  ),
                ),
              ),
            ],
          ),
          SizedBox(height: MediaQuery.of(context).padding.bottom + 8),
        ],
      ),
    );
  }

  Widget _buildInfoRow(BuildContext context, String label, String value) {
    return Row(
      children: [
        Text(
          '$label: ',
          style: Theme.of(context).textTheme.labelMedium,
        ),
        Expanded(
          child: Text(
            value,
            style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                  fontWeight: FontWeight.w600,
                ),
          ),
        ),
      ],
    );
  }
}
