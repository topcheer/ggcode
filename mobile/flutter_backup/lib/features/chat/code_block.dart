import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:highlight/highlight.dart' show highlight;

class CodeBlock extends StatelessWidget {
  final String code;
  final String? language;

  const CodeBlock({
    super.key,
    required this.code,
    this.language,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);

    return Container(
      margin: const EdgeInsets.symmetric(vertical: 8),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: theme.colorScheme.outlineVariant),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // Header with language label and copy button
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
            decoration: BoxDecoration(
              color: theme.colorScheme.surfaceContainerHigh,
              borderRadius: const BorderRadius.only(
                topLeft: Radius.circular(12),
                topRight: Radius.circular(12),
              ),
            ),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text(
                  language ?? 'code',
                  style: theme.textTheme.labelSmall?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                IconButton(
                  icon: const Icon(Icons.copy, size: 16),
                  tooltip: 'Copy code',
                  constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
                  padding: EdgeInsets.zero,
                  onPressed: () {
                    Clipboard.setData(ClipboardData(text: code));
                    ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(
                        content: Text('Code copied'),
                        duration: Duration(seconds: 1),
                      ),
                    );
                  },
                ),
              ],
            ),
          ),
          // Code content
          Padding(
            padding: const EdgeInsets.all(12),
            child: _buildHighlightedCode(theme),
          ),
        ],
      ),
    );
  }

  Widget _buildHighlightedCode(ThemeData theme) {
    final mode = allLanguages[language ?? ''];
    final result = highlight.parse(code, language: language, autoDetection: language == null);

    // Generate styled text spans from highlight nodes
    final spans = <TextSpan>[];
    for (final node in result.nodes ?? []) {
      _buildSpans(node, spans, theme);
    }

    return SelectableText.rich(
      TextSpan(
        children: spans.isEmpty
            ? [TextSpan(text: code, style: TextStyle(fontFamily: 'monospace', fontSize: 13, color: theme.colorScheme.onSurface))]
            : spans,
        style: TextStyle(
          fontFamily: 'monospace',
          fontSize: 13,
          color: theme.colorScheme.onSurface,
        ),
      ),
    );
  }

  void _buildSpans(dynamic node, List<TextSpan> spans, ThemeData theme) {
    if (node.value != null) {
      spans.add(TextSpan(text: node.value, style: _styleForClass(node.className, theme)));
    }
    if (node.children != null) {
      for (final child in node.children!) {
        _buildSpans(child, spans, theme);
      }
    }
  }

  TextStyle _styleForClass(String? className, ThemeData theme) {
    final base = TextStyle(fontFamily: 'monospace', fontSize: 13);
    switch (className) {
      case 'keyword':
        return base.copyWith(color: const Color(0xFFC792EA));
      case 'string':
      case 'subst':
        return base.copyWith(color: const Color(0xFFC3E88D));
      case 'number':
      case 'literal':
        return base.copyWith(color: const Color(0xFFF78C6C));
      case 'comment':
        return base.copyWith(color: const Color(0xFF546E7A));
      case 'title':
      case 'function':
        return base.copyWith(color: const Color(0xFF82AAFF));
      case 'params':
        return base.copyWith(color: const Color(0xFFFFCB6B));
      case 'built_in':
        return base.copyWith(color: const Color(0xFFFFCB6B));
      case 'type':
        return base.copyWith(color: const Color(0xFFFFCB6B));
      case 'attr':
        return base.copyWith(color: const Color(0xFFFFCB6B));
      case 'tag':
        return base.copyWith(color: const Color(0xFFF07178));
      case 'name':
        return base.copyWith(color: const Color(0xFFF07178));
      case 'attribute':
        return base.copyWith(color: const Color(0xFFC792EA));
      case 'variable':
        return base.copyWith(color: const Color(0xFFEEFFFF));
      case 'regexp':
        return base.copyWith(color: const Color(0xFF89DDFF));
      case 'symbol':
        return base.copyWith(color: const Color(0xFFF78C6C));
      case 'bullet':
        return base.copyWith(color: const Color(0xFF89DDFF));
      case 'meta':
        return base.copyWith(color: const Color(0xFFFFCB6B));
      case 'addition':
        return base.copyWith(color: const Color(0xFFC3E88D));
      case 'deletion':
        return base.copyWith(color: const Color(0xFFF07178));
      default:
        return base.copyWith(color: theme.colorScheme.onSurface);
    }
  }
}
