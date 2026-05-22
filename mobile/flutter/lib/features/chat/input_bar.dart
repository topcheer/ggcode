import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers/session_provider.dart';
import '../../core/l10n/app_localizations.dart';

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
    final isRunning = status == 'thinking' || status == 'running';
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
      padding: const EdgeInsets.fromLTRB(8, 4, 8, 8),
      decoration: const BoxDecoration(
        color: Color(0xFF0D0D14),
        border: Border(top: BorderSide(color: Colors.white12)),
      ),
      child: Row(
        children: [
          Expanded(
            child: AnimatedBuilder(
              animation: _busyPulseController,
              builder: (context, child) {
                final pulse = _busyPulseController.value;
                final borderColor = isRunning
                    ? Colors.blueAccent.withValues(alpha: 0.55 + pulse * 0.35)
                    : Colors.white12;
                final shadowColor = Colors.blueAccent
                    .withValues(alpha: isRunning ? 0.10 + pulse * 0.18 : 0);
                return DecoratedBox(
                  decoration: BoxDecoration(
                    borderRadius: BorderRadius.circular(14),
                    border: Border.all(color: borderColor),
                    boxShadow: [
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
                style: const TextStyle(color: Colors.white, fontSize: 14),
                decoration: InputDecoration(
                  hintText: hintText,
                  hintStyle:
                      TextStyle(color: Colors.white.withValues(alpha: 0.3)),
                  filled: true,
                  fillColor: const Color(0xFF1A1A2E),
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(12),
                    borderSide: BorderSide.none,
                  ),
                  enabledBorder: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(12),
                    borderSide: BorderSide.none,
                  ),
                  focusedBorder: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(12),
                    borderSide: BorderSide.none,
                  ),
                  contentPadding:
                      const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
                ),
                enabled: inputEnabled,
                onSubmitted: (_) => _send(),
              ),
            ),
          ),
          const SizedBox(width: 8),
          if (isRunning && canSend)
            IconButton(
              icon: const Icon(Icons.stop_circle, color: Colors.redAccent),
              onPressed: () {
                ref.read(connectionProvider.notifier).send({
                  'type': 'interrupt',
                  'data': {},
                });
              },
              tooltip: 'Interrupt',
            ),
          IconButton(
            icon: Icon(Icons.send,
                color: inputEnabled ? Colors.blueAccent : Colors.white24),
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
