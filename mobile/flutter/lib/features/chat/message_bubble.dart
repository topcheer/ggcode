import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_mermaid/flutter_mermaid.dart';

import '../../core/providers/session_provider.dart';

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
        margin: const EdgeInsets.symmetric(vertical: 2, horizontal: 4),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        constraints: BoxConstraints(
          maxWidth: MediaQuery.of(context).size.width * 0.75,
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

  Widget _buildAgentBubble(BuildContext context) {
    // Split text into segments: markdown text and mermaid diagrams
    final segments = _parseSegments(message.text);

    return Align(
      alignment: Alignment.centerLeft,
      child: Container(
        margin: const EdgeInsets.symmetric(vertical: 2, horizontal: 4),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        constraints: BoxConstraints(
          maxWidth: MediaQuery.of(context).size.width * 0.85,
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

  Widget _buildMermaidDiagram(String code) {
    return Container(
      margin: const EdgeInsets.symmetric(vertical: 6),
      padding: const EdgeInsets.all(8),
      decoration: BoxDecoration(
        color: const Color(0xFF2A2A3E),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: Colors.white12),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Row(
            children: [
              Icon(Icons.account_tree, size: 14, color: Colors.blueAccent),
              SizedBox(width: 4),
              Text(
                'Diagram',
                style: TextStyle(
                  color: Colors.blueAccent,
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
      p: TextStyle(color: Colors.white.withOpacity(0.9), fontSize: 14),
      code: TextStyle(
        color: Colors.blueAccent[100],
        fontSize: 13,
        backgroundColor: const Color(0xFF2A2A3E),
      ),
      codeblockDecoration: BoxDecoration(
        color: const Color(0xFF1A1A2E),
        borderRadius: BorderRadius.circular(8),
      ),
      codeblockPadding: const EdgeInsets.all(8),
      listBullet: TextStyle(color: Colors.white.withOpacity(0.9), fontSize: 14),
      h2: const TextStyle(color: Colors.white, fontSize: 16, fontWeight: FontWeight.w600),
      h3: const TextStyle(color: Colors.white, fontSize: 15, fontWeight: FontWeight.w600),
      strong: const TextStyle(color: Colors.white, fontWeight: FontWeight.w700),
      em: const TextStyle(color: Colors.white70, fontStyle: FontStyle.italic),
      blockquote: TextStyle(color: Colors.white.withOpacity(0.7), fontSize: 14),
      blockquoteDecoration: BoxDecoration(
        color: const Color(0xFF2A2A3E),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: Colors.white12),
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
      return Colors.green;
    }
  }
}

class _TextSegment {
  final String content;
  final bool isMermaid;

  _TextSegment({required this.content, this.isMermaid = false});
}
