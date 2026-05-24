import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers/session_provider.dart';
import '../../core/l10n/app_localizations.dart';
import '../../core/theme/app_theme.dart';

class InputBar extends ConsumerStatefulWidget {
  final TextEditingController controller;
  const InputBar({super.key, required this.controller});

  @override
  ConsumerState<InputBar> createState() => _InputBarState();
}

class _InputBarState extends ConsumerState<InputBar>
    with SingleTickerProviderStateMixin {
  late final AnimationController _busyPulseController;

  @override
  void initState() {
    super.initState();
    _busyPulseController = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 1400),
    );
  }

  @override
  void dispose() {
    _busyPulseController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final status = ref.watch(displayedAgentStatusProvider);
    final isRunning = status == 'busy';
    final canSend = ref.watch(canSendMessagesProvider);
    final isHistorical = ref.watch(isHistoricalViewProvider);
    final connState = ref.watch(connectionProvider);
    final inputEnabled = canSend;
    if (isRunning) {
      if (!_busyPulseController.isAnimating) {
        _busyPulseController.repeat(reverse: true);
      }
    } else if (_busyPulseController.isAnimating) {
      _busyPulseController.stop();
      _busyPulseController.value = 0;
    }
    String hintText;
    if (isHistorical) {
      hintText = t('chat.placeholder.cached');
    } else if (connState.status != ConnectionStatus.connected) {
      hintText = t('chat.placeholder.disconnected');
    } else if (isRunning) {
      hintText = t('chat.placeholder.working');
    } else {
      hintText = t('chat.placeholder.idle');
    }

    return Container(
      padding: EdgeInsets.fromLTRB(12, 8, 12, 14),
      decoration: BoxDecoration(
        color: AppColors.background,
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Expanded(
            child: AnimatedBuilder(
              animation: _busyPulseController,
              builder: (context, child) {
                final pulse = _busyPulseController.value;
                final borderColor = isRunning
                    ? AppColors.accent.withValues(alpha: 0.60 + pulse * 0.20)
                    : AppColors.border;
                final shadowColor = AppColors.accent
                    .withValues(alpha: isRunning ? 0.10 + pulse * 0.18 : 0);
                return DecoratedBox(
                  decoration: BoxDecoration(
                    color: AppColors.surface,
                    borderRadius: BorderRadius.circular(AppRadii.lg),
                    border: Border.all(color: borderColor),
                    boxShadow: [
                      ...AppShadows.panel,
                      if (isRunning)
                        BoxShadow(
                          color: shadowColor,
                          blurRadius: 12 + pulse * 10,
                          spreadRadius: pulse * 1.5,
                        ),
                    ],
                  ),
                  child: child,
                );
              },
              child: TextField(
                controller: widget.controller,
                style: TextStyle(color: AppColors.textPrimary, fontSize: 14),
                decoration: InputDecoration(
                  hintText: hintText,
                  hintStyle: TextStyle(color: AppColors.textMuted),
                  filled: true,
                  fillColor: AppColors.surface,
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(AppRadii.lg),
                    borderSide: BorderSide.none,
                  ),
                  enabledBorder: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(AppRadii.lg),
                    borderSide: BorderSide.none,
                  ),
                  focusedBorder: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(AppRadii.lg),
                    borderSide: BorderSide.none,
                  ),
                  contentPadding: const EdgeInsets.fromLTRB(18, 14, 18, 14),
                ),
                enabled: inputEnabled,
                onSubmitted: (_) => _send(),
              ),
            ),
          ),
          SizedBox(width: 10),
          if (isRunning && canSend)
            _ActionButton(
              icon: Icons.stop_circle,
              color: AppColors.danger,
              onPressed: () {
                ref.read(connectionProvider.notifier).send({
                  'type': 'interrupt',
                  'data': {},
                });
              },
              tooltip: 'Interrupt',
            ),
          SizedBox(width: 10),
          _ActionButton(
            icon: Icons.send,
            color: inputEnabled ? AppColors.accent : AppColors.textMuted,
            onPressed: inputEnabled ? _send : null,
            tooltip: 'Send',
          ),
        ],
      ),
    );
  }

  void _send() {
    if (!ref.read(canSendMessagesProvider)) return;
    final text = widget.controller.text.trim();
    if (text.isEmpty) return;
    widget.controller.clear();
    ref.read(chatProvider.notifier).addUserMessage(text);
  }
}

class _ActionButton extends StatelessWidget {
  final IconData icon;
  final Color color;
  final VoidCallback? onPressed;
  final String tooltip;

  const _ActionButton({
    required this.icon,
    required this.color,
    required this.onPressed,
    required this.tooltip,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 46,
      height: 46,
      decoration: BoxDecoration(
        color: AppColors.surface,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: AppColors.border),
        boxShadow: AppShadows.panel,
      ),
      child: IconButton(
        icon: Icon(icon, color: color, size: 20),
        onPressed: onPressed,
        tooltip: tooltip,
      ),
    );
  }
}
