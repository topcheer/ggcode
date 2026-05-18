import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:markdown/markdown.dart' as md;

import 'code_block.dart';

class MarkdownView extends StatelessWidget {
  final String data;

  const MarkdownView({super.key, required this.data});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);

    return MarkdownBody(
      data: data,
      selectable: true,
      builders: {
        'pre': _CodeBlockBuilder(),
      },
      extensionSet: md.ExtensionSet(
        md.ExtensionSet.gitHubFlavored.blockSyntaxes,
        [
          md.ExtensionSet.gitHubFlavored.inlineSyntaxes,
          md.InlineHtmlSyntax(),
        ].expand((e) => e),
      ),
      styleSheet: MarkdownStyleSheet(
        p: TextStyle(
          color: theme.colorScheme.onSurface,
          fontSize: 15,
          height: 1.5,
        ),
        h1: TextStyle(
          color: theme.colorScheme.onSurface,
          fontSize: 22,
          fontWeight: FontWeight.bold,
        ),
        h2: TextStyle(
          color: theme.colorScheme.onSurface,
          fontSize: 20,
          fontWeight: FontWeight.bold,
        ),
        h3: TextStyle(
          color: theme.colorScheme.onSurface,
          fontSize: 18,
          fontWeight: FontWeight.bold,
        ),
        code: TextStyle(
          color: theme.colorScheme.primary,
          backgroundColor: theme.colorScheme.surfaceContainerHighest,
          fontFamily: 'monospace',
          fontSize: 13,
        ),
        codeblockDecoration: BoxDecoration(
          color: theme.colorScheme.surfaceContainerHighest,
          borderRadius: BorderRadius.circular(12),
        ),
        blockquote: TextStyle(
          color: theme.colorScheme.onSurfaceVariant,
          fontSize: 15,
        ),
        blockquoteDecoration: BoxDecoration(
          color: theme.colorScheme.surfaceContainerHigh,
          borderRadius: BorderRadius.circular(8),
          border: Border(
            left: BorderSide(
              color: theme.colorScheme.primary,
              width: 3,
            ),
          ),
        ),
        listBullet: TextStyle(color: theme.colorScheme.primary),
        tableHead: TextStyle(
          fontWeight: FontWeight.w600,
          color: theme.colorScheme.onSurface,
        ),
        tableBody: TextStyle(color: theme.colorScheme.onSurface),
        tableBorder: TableBorder.all(
          color: theme.colorScheme.outlineVariant,
          width: 1,
        ),
        a: TextStyle(
          color: theme.colorScheme.primary,
          decoration: TextDecoration.underline,
        ),
      ),
    );
  }
}

/// Custom builder for fenced code blocks to add syntax highlighting
class _CodeBlockBuilder extends MarkdownElementBuilder {
  @override
  Widget visitElementAfterWithContext(
    BuildContext context,
    md.Element element,
    _, __,
  ) {
    final code = element.textContent;
    final langClass = element.attributes['class'] ?? '';
    final language = langClass.replaceFirst('language-', '');

    return CodeBlock(
      code: code,
      language: language.isEmpty ? null : language,
    );
  }
}
