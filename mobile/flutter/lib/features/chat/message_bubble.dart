import 'package:flutter/material.dart';
import 'package:flutter_markdown_plus/flutter_markdown_plus.dart';
import 'package:flutter_mermaid/flutter_mermaid.dart';

import '../../core/providers/session_provider.dart';
import '../../core/theme/app_theme.dart';

const _shellCommandKind = 'shell_command';
const _shellOutputKind = 'shell_output';

class MessageBubble extends StatelessWidget {
  final ChatMessage message;

  const MessageBubble({super.key, required this.message});

  @override
  Widget build(BuildContext context) {
    if (_isShellOutputMessage) {
      return _buildShellOutputBubble(context);
    }
    if (_isShellCommandMessage) {
      return _buildShellCommandBubble(context);
    }
    if (message.isUser) {
      return _buildUserBubble(context);
    }
    return _buildAgentBubble(context);
  }

  bool get _isShellCommandMessage =>
      message.kind == _shellCommandKind ||
      _parseShellCommand(message.text) != null;

  bool get _isShellOutputMessage => message.kind == _shellOutputKind;

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
          gradient: LinearGradient(
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

  Widget _buildShellCommandBubble(BuildContext context) {
    final parsed = _parseShellCommand(message.text);
    final prefix = parsed?.prefix ?? r'$';
    final command = parsed?.command ?? message.text.trim();
    return Align(
      alignment: Alignment.centerRight,
      child: Container(
        key: Key('shellCommandBubble-${message.id}'),
        margin: const EdgeInsets.symmetric(vertical: 4),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        constraints: BoxConstraints(
          maxWidth: MediaQuery.of(context).size.width * 0.82,
        ),
        decoration: BoxDecoration(
          color: const Color(0xFF111827),
          borderRadius: BorderRadius.circular(18),
          border: Border.all(color: const Color(0xFF374151)),
          boxShadow: AppShadows.panel,
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
              decoration: BoxDecoration(
                color: const Color(0xFF1F2937),
                borderRadius: BorderRadius.circular(999),
                border: Border.all(color: const Color(0xFF4B5563)),
              ),
              child: Text(
                prefix,
                style: const TextStyle(
                  color: Color(0xFF60A5FA),
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                  fontFamily: 'monospace',
                ),
              ),
            ),
            const SizedBox(width: 10),
            Flexible(
              child: SelectableText(
                command,
                style: const TextStyle(
                  color: Color(0xFFF9FAFB),
                  fontSize: 13,
                  height: 1.45,
                  fontFamily: 'monospace',
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildAgentBubble(BuildContext context) {
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
                child: Center(
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

  Widget _buildShellOutputBubble(BuildContext context) {
    return Align(
      alignment: Alignment.centerLeft,
      child: Container(
        key: Key('shellOutputBubble-${message.id}'),
        margin: const EdgeInsets.symmetric(vertical: 4),
        padding: const EdgeInsets.fromLTRB(14, 12, 14, 12),
        constraints: BoxConstraints(
          maxWidth: MediaQuery.of(context).size.width * 0.88,
        ),
        decoration: BoxDecoration(
          color: const Color(0xFF111827),
          borderRadius: BorderRadius.circular(18),
          border: Border.all(color: const Color(0xFF374151)),
          boxShadow: AppShadows.panel,
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text(
              'Terminal',
              style: TextStyle(
                color: Color(0xFF9CA3AF),
                fontSize: 11,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 8),
            SelectableText.rich(
              _buildAnsiText(message.text),
              key: Key('shellOutputText-${message.id}'),
              style: const TextStyle(
                color: Color(0xFFE5E7EB),
                fontSize: 13,
                height: 1.45,
                fontFamily: 'monospace',
              ),
            ),
            if (message.streaming)
              Container(
                margin: const EdgeInsets.only(top: 8),
                width: 6,
                height: 6,
                decoration: const BoxDecoration(
                  color: Color(0xFF60A5FA),
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
        color: AppColors.backgroundElevated,
        borderRadius: BorderRadius.circular(AppRadii.sm),
        border: Border.all(color: AppColors.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.account_tree, size: 14, color: AppColors.accent),
              const SizedBox(width: 4),
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
      p: TextStyle(color: AppColors.textPrimary, fontSize: 14, height: 1.5),
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
      listBullet: TextStyle(color: AppColors.textPrimary, fontSize: 14),
      h2: TextStyle(
          color: AppColors.textPrimary,
          fontSize: 16,
          fontWeight: FontWeight.w600),
      h3: TextStyle(
          color: AppColors.textPrimary,
          fontSize: 15,
          fontWeight: FontWeight.w600),
      strong:
          TextStyle(color: AppColors.textPrimary, fontWeight: FontWeight.w700),
      em: TextStyle(
          color: AppColors.textSecondary, fontStyle: FontStyle.italic),
      blockquote: TextStyle(color: AppColors.textSecondary, fontSize: 14),
      blockquoteDecoration: BoxDecoration(
        color: AppColors.backgroundElevated,
        borderRadius: BorderRadius.circular(AppRadii.xs),
        border: Border.all(color: AppColors.border),
      ),
    );
  }

  List<_TextSegment> _parseSegments(String text) {
    final segments = <_TextSegment>[];
    final regex = RegExp(r'```mermaid\s*\n([\s\S]*?)```', multiLine: true);

    int lastEnd = 0;
    for (final match in regex.allMatches(text)) {
      if (match.start > lastEnd) {
        final before = text.substring(lastEnd, match.start);
        if (before.trim().isNotEmpty) {
          segments.add(_TextSegment(content: before));
        }
      }
      segments.add(_TextSegment(
        content: match.group(1)?.trim() ?? '',
        isMermaid: true,
      ));
      lastEnd = match.end;
    }

    if (lastEnd < text.length) {
      final remaining = text.substring(lastEnd);
      if (remaining.trim().isNotEmpty) {
        segments.add(_TextSegment(content: remaining));
      }
    }

    if (segments.isEmpty) {
      segments.add(_TextSegment(content: text));
    }

    return segments;
  }

  _ShellCommandData? _parseShellCommand(String text) {
    final trimmed = text.trim();
    if (trimmed.startsWith(r'$ ')) {
      return _ShellCommandData(r'$', trimmed.substring(2).trimLeft());
    }
    if (trimmed.startsWith('! ')) {
      return _ShellCommandData('!', trimmed.substring(2).trimLeft());
    }
    return null;
  }

  TextSpan _buildAnsiText(String text) {
    final regex = RegExp(r'\x1B\[[0-9;]*m');
    final spans = <TextSpan>[];
    final state = _AnsiState();
    int lastEnd = 0;
    for (final match in regex.allMatches(text)) {
      if (match.start > lastEnd) {
        spans.add(TextSpan(
          text: text.substring(lastEnd, match.start),
          style: state.toTextStyle(),
        ));
      }
      state.apply(match.group(0) ?? '');
      lastEnd = match.end;
    }
    if (lastEnd < text.length) {
      spans.add(TextSpan(
        text: text.substring(lastEnd),
        style: state.toTextStyle(),
      ));
    }
    if (spans.isEmpty) {
      spans.add(TextSpan(text: text, style: state.toTextStyle()));
    }
    return TextSpan(children: spans);
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

class _ShellCommandData {
  final String prefix;
  final String command;

  _ShellCommandData(this.prefix, this.command);
}

class _AnsiState {
  bool bold = false;
  Color? foreground;
  Color? background;

  void apply(String sequence) {
    final body = sequence.substring(2, sequence.length - 1);
    final codes = body.isEmpty
        ? <int>[0]
        : body.split(';').map((part) => int.tryParse(part) ?? 0).toList();
    for (int i = 0; i < codes.length; i++) {
      final code = codes[i];
      switch (code) {
        case 0:
          bold = false;
          foreground = null;
          background = null;
          break;
        case 1:
          bold = true;
          break;
        case 22:
          bold = false;
          break;
        case 39:
          foreground = null;
          break;
        case 49:
          background = null;
          break;
        default:
          if (30 <= code && code <= 37) {
            foreground = _ansiPalette[code - 30];
          } else if (90 <= code && code <= 97) {
            foreground = _ansiBrightPalette[code - 90];
          } else if (40 <= code && code <= 47) {
            background = _ansiPalette[code - 40];
          } else if (100 <= code && code <= 107) {
            background = _ansiBrightPalette[code - 100];
          } else if ((code == 38 || code == 48) &&
              i + 2 < codes.length &&
              codes[i + 1] == 5) {
            final color = _ansi256Color(codes[i + 2]);
            if (code == 38) {
              foreground = color;
            } else {
              background = color;
            }
            i += 2;
          }
      }
    }
  }

  TextStyle toTextStyle() => TextStyle(
        color: foreground ?? const Color(0xFFE5E7EB),
        backgroundColor: background,
        fontWeight: bold ? FontWeight.w700 : FontWeight.w400,
      );
}

const _ansiPalette = <Color>[
  Color(0xFF111827),
  Color(0xFFF87171),
  Color(0xFF4ADE80),
  Color(0xFFFBBF24),
  Color(0xFF60A5FA),
  Color(0xFFC084FC),
  Color(0xFF22D3EE),
  Color(0xFFE5E7EB),
];

const _ansiBrightPalette = <Color>[
  Color(0xFF6B7280),
  Color(0xFFFCA5A5),
  Color(0xFF86EFAC),
  Color(0xFFFCD34D),
  Color(0xFF93C5FD),
  Color(0xFFD8B4FE),
  Color(0xFF67E8F9),
  Color(0xFFFFFFFF),
];

Color _ansi256Color(int code) {
  if (code < 0) {
    return const Color(0xFFE5E7EB);
  }
  if (code < 8) {
    return _ansiPalette[code];
  }
  if (code < 16) {
    return _ansiBrightPalette[code - 8];
  }
  if (code >= 232 && code <= 255) {
    final level = ((code - 232) * 10) + 8;
    return Color.fromARGB(0xFF, level, level, level);
  }
  if (code >= 16 && code <= 231) {
    final value = code - 16;
    final r = value ~/ 36;
    final g = (value % 36) ~/ 6;
    final b = value % 6;
    int channel(int component) => component == 0 ? 0 : 55 + (component * 40);
    return Color.fromARGB(0xFF, channel(r), channel(g), channel(b));
  }
  return const Color(0xFFE5E7EB);
}
