import 'package:flutter/material.dart';

import '../../core/providers/session_provider.dart';

class MessageBubble extends StatelessWidget {
  final ChatMessage message;

  const MessageBubble({super.key, required this.message});

  @override
  Widget build(BuildContext context) {
    if (message.isUser) {
      return _buildUserBubble();
    }
    return _buildAgentBubble();
  }

  Widget _buildUserBubble() {
    return Align(
      alignment: Alignment.centerRight,
      child: Container(
        margin: const EdgeInsets.symmetric(vertical: 2, horizontal: 4),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        constraints: BoxConstraints(
          maxWidth: MediaQuery.of(navigatorKey.currentContext!).size.width * 0.75,
        ),
        decoration: BoxDecoration(
          color: Colors.blueAccent,
          borderRadius: BorderRadius.circular(16),
        ),
        child: SelectableText(
          message.text,
          style: const TextStyle(color: Colors.white, fontSize: 14),
        ),
      ),
    );
  }

  Widget _buildAgentBubble() {
    return Align(
      alignment: Alignment.centerLeft,
      child: Container(
        margin: const EdgeInsets.symmetric(vertical: 2, horizontal: 4),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        constraints: BoxConstraints(
          maxWidth: MediaQuery.of(navigatorKey.currentContext!).size.width * 0.8,
        ),
        decoration: BoxDecoration(
          color: const Color(0xFF1E1E2E),
          borderRadius: BorderRadius.circular(16),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (message.sourceName != null)
              Padding(
                padding: const EdgeInsets.only(bottom: 4),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Container(
                      width: 6,
                      height: 6,
                      decoration: BoxDecoration(
                        color: _parseColor(message.sourceColor ?? '#4CAF50'),
                        shape: BoxShape.circle,
                      ),
                    ),
                    const SizedBox(width: 4),
                    Text(
                      message.sourceName!,
                      style: TextStyle(
                        color: _parseColor(message.sourceColor ?? '#4CAF50'),
                        fontSize: 11,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ],
                ),
              ),
            SelectableText(
              message.text,
              style: TextStyle(
                color: Colors.white.withOpacity(0.9),
                fontSize: 14,
                fontFamily: message.text.contains('\n') ? 'monospace' : null,
              ),
            ),
            if (message.streaming)
              Container(
                margin: const EdgeInsets.only(top: 2),
                width: 6,
                height: 6,
                decoration: const BoxDecoration(
                  color: Colors.blueAccent,
                  shape: BoxShape.circle,
                ),
              ),
          ],
        ),
      ),
    );
  }

  Color _parseColor(String hex) {
    try {
      return Color(int.parse(hex.replaceFirst('#', '0xFF')));
    } catch (_) {
      return Colors.green;
    }
  }
}

// Global navigator key for MediaQuery access
final navigatorKey = GlobalKey<NavigatorState>();
