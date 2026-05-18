import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../core/models/protocol.dart';

class ToolCard extends StatefulWidget {
  final dynamic toolData; // ToolCall or ToolResult

  const ToolCard({super.key, required this.toolData});

  @override
  State<ToolCard> createState() => _ToolCardState();
}

class _ToolCardState extends State<ToolCard> {
  bool _expanded = false;

  IconData _iconForTool(String name) {
    switch (name) {
      case 'read_file':
        return Icons.description_outlined;
      case 'write_file':
      case 'edit_file':
        return Icons.edit_outlined;
      case 'run_command':
      case 'start_command':
        return Icons.terminal;
      case 'list_directory':
        return Icons.folder_outlined;
      case 'search_files':
      case 'glob':
        return Icons.search;
      case 'git_status':
      case 'git_diff':
      case 'git_log':
        return Icons.git_branch;
      case 'web_fetch':
      case 'web_search':
        return Icons.language;
      default:
        return Icons.build_outlined;
    }
  }

  Color _colorForTool(String name) {
    switch (name) {
      case 'run_command':
      case 'start_command':
        return Colors.orange.shade300;
      case 'write_file':
      case 'edit_file':
        return Colors.blue.shade300;
      case 'read_file':
        return Colors.green.shade300;
      default:
        return Colors.purple.shade300;
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final isCall = widget.toolData is ToolCall;
    final toolName = isCall
        ? (widget.toolData as ToolCall).toolName
        : (widget.toolData as ToolResult).toolName;
    final content = isCall
        ? (widget.toolData as ToolCall).detail.isNotEmpty
            ? (widget.toolData as ToolCall).detail
            : (widget.toolData as ToolCall).args
        : (widget.toolData as ToolResult).result;
    final isError =
        !isCall && (widget.toolData as ToolResult).isError;

    return Container(
      margin: const EdgeInsets.symmetric(vertical: 4, horizontal: 12),
      decoration: BoxDecoration(
        color: isError
            ? Colors.red.shade900.withOpacity(0.3)
            : theme.colorScheme.surfaceContainer,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(
          color: isError
              ? Colors.red.shade800
              : theme.colorScheme.outlineVariant,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          InkWell(
            borderRadius: BorderRadius.circular(12),
            onTap: () => setState(() => _expanded = !_expanded),
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
              child: Row(
                children: [
                  Icon(
                    _iconForTool(toolName),
                    size: 18,
                    color: _colorForTool(toolName),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      isCall ? 'Running $toolName' : 'Result: $toolName',
                      style: theme.textTheme.bodySmall?.copyWith(
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                  Icon(
                    _expanded ? Icons.expand_less : Icons.expand_more,
                    size: 20,
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ],
              ),
            ),
          ),
          if (_expanded) ...[
            const Divider(height: 1),
            Padding(
              padding: const EdgeInsets.all(12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Container(
                    width: double.infinity,
                    padding: const EdgeInsets.all(8),
                    decoration: BoxDecoration(
                      color: theme.colorScheme.surfaceContainerHighest,
                      borderRadius: BorderRadius.circular(8),
                    ),
                    child: SelectableText(
                      content.length > 2000
                          ? '${content.substring(0, 2000)}\n... (truncated)'
                          : content,
                      style: TextStyle(
                        fontFamily: 'monospace',
                        fontSize: 12,
                        color: isError
                            ? Colors.red.shade300
                            : theme.colorScheme.onSurfaceVariant,
                      ),
                    ),
                  ),
                  const SizedBox(height: 8),
                  Align(
                    alignment: Alignment.centerRight,
                    child: IconButton(
                      icon: const Icon(Icons.copy, size: 16),
                      tooltip: 'Copy',
                      constraints:
                          const BoxConstraints(minWidth: 32, minHeight: 32),
                      padding: EdgeInsets.zero,
                      onPressed: () {
                        Clipboard.setData(ClipboardData(text: content));
                        ScaffoldMessenger.of(context).showSnackBar(
                          const SnackBar(
                            content: Text('Copied'),
                            duration: Duration(seconds: 1),
                          ),
                        );
                      },
                    ),
                  ),
                ],
              ),
            ),
          ],
        ],
      ),
    );
  }
}
