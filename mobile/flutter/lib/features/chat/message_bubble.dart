import 'package:flutter/material.dart';

import '../../core/providers/session_provider.dart';
import 'markdown_view.dart';

class MessageBubble extends StatelessWidget {
  final ChatMessage message;
  final String? streamingText;

  const MessageBubble({
    super.key,
    required this.message,
    this.streamingText,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final screenWidth = MediaQuery.of(context).size.width;

    switch (message.type) {
      case MessageType.user:
        return _buildUserBubble(theme, screenWidth);
      case MessageType.agent:
        return _buildAgentBubble(theme, screenWidth);
      default:
        return const SizedBox.shrink();
    }
  }

  Widget _buildUserBubble(ThemeData theme, double screenWidth) {
    return Align(
      alignment: Alignment.centerRight,
      child: Container(
        constraints: BoxConstraints(maxWidth: screenWidth * 0.75),
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
        margin: const EdgeInsets.symmetric(vertical: 4, horizontal: 12),
        decoration: BoxDecoration(
          color: theme.colorScheme.primary,
          borderRadius: const BorderRadius.only(
            topLeft: Radius.circular(16),
            topRight: Radius.circular(16),
            bottomLeft: Radius.circular(16),
            bottomRight: Radius.circular(4),
          ),
        ),
        child: Text(
          message.text ?? '',
          style: TextStyle(
            color: theme.colorScheme.onPrimary,
            fontSize: 15,
          ),
        ),
      ),
    );
  }

  Widget _buildAgentBubble(ThemeData theme, double screenWidth) {
    final text = message.text ?? streamingText ?? '';
    if (text.isEmpty) return const SizedBox.shrink();

    return Align(
      alignment: Alignment.centerLeft,
      child: Container(
        constraints: BoxConstraints(maxWidth: screenWidth * 0.85),
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
        margin: const EdgeInsets.symmetric(vertical: 4, horizontal: 12),
        decoration: BoxDecoration(
          color: theme.colorScheme.surfaceContainerHigh,
          borderRadius: const BorderRadius.only(
            topLeft: Radius.circular(4),
            topRight: Radius.circular(16),
            bottomLeft: Radius.circular(16),
            bottomRight: Radius.circular(16),
          ),
        ),
        child: MarkdownView(data: text),
      ),
    );
  }
}
