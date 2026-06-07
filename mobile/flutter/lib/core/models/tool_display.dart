/// Tool display name and detail generation.
///
/// Mirrors the TUI's toolCallDisplayName + describeTool logic.
/// The server pushes raw toolName + args; the mobile client
/// decides how to present them.
library;

import 'dart:convert';

/// Generates a display name for a tool call from raw data.
///
/// Priority:
/// 1. If args JSON contains a "description" field, use it
/// 2. Otherwise, prettify the tool name (bash_command → Bash Command)
String toolCallDisplayName(String toolName, String rawArgs) {
  final args = _parseArgs(rawArgs);
  final desc = _argString(args, 'description');
  if (desc.isNotEmpty) {
    final pretty = _friendlyToolName(toolName);
    return '$desc ($pretty)';
  }
  return _prettifyToolName(toolName);
}

/// Generates a detail string for a tool call from raw data.
///
/// Tries to extract the most relevant detail field from args:
/// command, path, file_path, query, pattern, etc.
String toolCallDetail(String toolName, String rawArgs) {
  final args = _parseArgs(rawArgs);

  // Try common detail fields in priority order
  for (final key in [
    'command',
    'path',
    'file_path',
    'query',
    'pattern',
    'url',
    'directory',
  ]) {
    final val = _argString(args, key);
    if (val.isNotEmpty) return val;
  }
  return '';
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

Map<String, dynamic> _parseArgs(String rawArgs) {
  if (rawArgs.isEmpty) return {};
  try {
    return jsonDecode(rawArgs) as Map<String, dynamic>;
  } catch (_) {
    return {};
  }
}

String _argString(Map<String, dynamic> args, String key) {
  final v = args[key];
  if (v == null) return '';
  if (v is String) return v;
  return v.toString();
}

/// Converts a tool name like "bash_command" or "swarm-task-create"
/// into a human-readable form like "Bash Command".
String _prettifyToolName(String name) {
  name = name.replaceAll('-', ' ').replaceAll('_', ' ');
  final parts = name.split(RegExp(r'\s+'));
  return parts
      .where((p) => p.isNotEmpty)
      .map((p) => p[0].toUpperCase() + p.substring(1))
      .join(' ');
}

/// Maps tool names to friendly verb names, mirroring TUI's friendlyToolName.
String _friendlyToolName(String name) {
  const mapping = <String, String>{
    'bash_command': 'Shell',
    'run_command': 'Shell',
    'read_file': 'Read',
    'edit_file': 'Edit',
    'write_file': 'Write',
    'glob': 'Find',
    'grep': 'Search',
    'search_files': 'Search',
    'web_fetch': 'Fetch',
    'web_search': 'Search',
    'ask_user': 'Ask',
    'list_directory': 'List',
    'delegate': 'Delegate',
  };
  return mapping[name] ?? _prettifyToolName(name);
}
