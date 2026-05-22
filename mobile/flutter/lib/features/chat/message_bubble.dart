import 'package:flutter/material.dart';
import 'package:flutter_markdown_plus/flutter_markdown_plus.dart';
import 'package:flutter_mermaid/flutter_mermaid.dart';

import '../../core/providers/session_provider.dart';
import '../../core/theme/app_theme.dart';

class MessageBubble extends StatelessWidget {
  final ChatMessage message;

  const MessageBubble({super.key, required this.message});

  @override
  Widget build(BuildContext context) {
    if (message.isUser) {
      return _buildUserBubble(context);
    }
    return _buildAgentBubble(context);
  }

  Widget _buildUserBubble(BuildContext context) {
    return Align(
      alignment: Alignment.centerRight,
      child: Container(
        margin: const EdgeInsets.symmetric(vertical: 4),
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        constraints: BoxConstraints(
          maxWidth: MediaQuery.of(context).size.width * 0.78,
        ),
        decoration: BoxDecoration(
          gradient: const LinearGradient(
            colors: [AppColors.accentSoft, AppColors.accent],
            begin: Alignment.topLeft,
            end: Alignment.bottomRight,
          ),
          borderRadius: BorderRadius.circular(22),
          boxShadow: AppShadows.panel,
        ),
        child: SelectableText(
          message.text,
          style:
              const TextStyle(color: Colors.white, fontSize: 14, height: 1.4),
        ),
      ),
    );
  }

  Widget _buildAgentBubble(BuildContext context) {
    // Split text into segments: markdown text and mermaid diagrams
    final segments = _parseSegments(message.text);

    return Align(
      alignment: Alignment.centerLeft,
      child: Container(
        margin: const EdgeInsets.symmetric(vertical: 4),
        padding: const EdgeInsets.fromLTRB(16, 14, 16, 14),
        constraints: BoxConstraints(
          maxWidth: MediaQuery.of(context).size.width * 0.85,
        ),
        decoration: BoxDecoration(
          color: AppColors.surface,
          borderRadius: BorderRadius.circular(22),
          border: Border.all(color: AppColors.border),
          boxShadow: AppShadows.panel,
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
                      width: 8,
                      height: 8,
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
            // Render segments
            ...segments.map((seg) {
              if (seg.isMermaid) {
                return _buildMermaidDiagram(seg.content);
              }
              if (seg.content.trim().isEmpty) return const SizedBox.shrink();
              return MarkdownBody(
                data: seg.content,
                selectable: true,
                styleSheet: _markdownStyleSheet(),
              );
            }),
            if (message.streaming)
              Container(
                margin: const EdgeInsets.only(top: 6),
                width: 18,
                height: 18,
                decoration: BoxDecoration(
                  color: AppColors.accent.withValues(alpha: 0.14),
                  shape: BoxShape.circle,
                ),
                child: const Center(
                  child: SizedBox(
                    width: 6,
                    height: 6,
                    child: DecoratedBox(
                      decoration: BoxDecoration(
                        color: AppColors.accent,
                        shape: BoxShape.circle,
                      ),
                    ),
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }

  Widget _buildMermaidDiagram(String code) {
    return Container(
      margin: const EdgeInsets.symmetric(vertical: 6),
      padding: const EdgeInsets.all(8),
      decoration: BoxDecoration(
        color: AppColors.backgroundElevated,
        borderRadius: BorderRadius.circular(AppRadii.sm),
        border: Border.all(color: AppColors.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Row(
            children: [
              Icon(Icons.account_tree, size: 14, color: AppColors.accent),
              SizedBox(width: 4),
              Text(
                'Diagram',
                style: TextStyle(
                  color: AppColors.accent,
                  fontSize: 11,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          ClipRRect(
            borderRadius: BorderRadius.circular(4),
            child: MermaidDiagram(code: code),
          ),
        ],
      ),
    );
  }

  MarkdownStyleSheet _markdownStyleSheet() {
    return MarkdownStyleSheet(
      p: const TextStyle(
          color: AppColors.textPrimary, fontSize: 14, height: 1.5),
      code: TextStyle(
        color: AppColors.accent.withValues(alpha: 0.95),
        fontSize: 13,
        backgroundColor: AppColors.backgroundElevated,
      ),
      codeblockDecoration: BoxDecoration(
        color: AppColors.backgroundElevated,
        borderRadius: BorderRadius.circular(AppRadii.sm),
        border: Border.all(color: AppColors.border),
      ),
      codeblockPadding: const EdgeInsets.all(8),
      listBullet: const TextStyle(color: AppColors.textPrimary, fontSize: 14),
      h2: const TextStyle(
          color: AppColors.textPrimary,
          fontSize: 16,
          fontWeight: FontWeight.w600),
      h3: const TextStyle(
          color: AppColors.textPrimary,
          fontSize: 15,
          fontWeight: FontWeight.w600),
      strong: const TextStyle(
          color: AppColors.textPrimary, fontWeight: FontWeight.w700),
      em: const TextStyle(
          color: AppColors.textSecondary, fontStyle: FontStyle.italic),
      blockquote: const TextStyle(color: AppColors.textSecondary, fontSize: 14),
      blockquoteDecoration: BoxDecoration(
        color: AppColors.backgroundElevated,
        borderRadius: BorderRadius.circular(AppRadii.xs),
        border: Border.all(color: AppColors.border),
      ),
    );
  }

  /// Parse text into segments, extracting ```mermaid blocks.
  List<_TextSegment> _parseSegments(String text) {
    final segments = <_TextSegment>[];
    final regex = RegExp(r'```mermaid\s*\n([\s\S]*?)```', multiLine: true);

    int lastEnd = 0;
    for (final match in regex.allMatches(text)) {
      // Add text before this mermaid block
      if (match.start > lastEnd) {
        final before = text.substring(lastEnd, match.start);
        if (before.trim().isNotEmpty) {
          segments.add(_TextSegment(content: before));
        }
      }
      // Add mermaid block
      segments.add(_TextSegment(
        content: match.group(1)?.trim() ?? '',
        isMermaid: true,
      ));
      lastEnd = match.end;
    }

    // Add remaining text after last mermaid block
    if (lastEnd < text.length) {
      final remaining = text.substring(lastEnd);
      if (remaining.trim().isNotEmpty) {
        segments.add(_TextSegment(content: remaining));
      }
    }

    // If no segments, return the whole text
    if (segments.isEmpty) {
      segments.add(_TextSegment(content: text));
    }

    return segments;
  }

  Color _parseColor(String hex) {
    try {
      return Color(int.parse(hex.replaceFirst('#', '0xFF')));
    } catch (_) {
      return AppColors.success;
    }
  }
}

class _TextSegment {
  final String content;
  final bool isMermaid;

  _TextSegment({required this.content, this.isMermaid = false});
}
