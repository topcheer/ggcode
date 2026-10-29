import 'dart:convert';

import 'package:flutter/material.dart';

import '../../core/l10n/app_localizations.dart' show t;

import '../../core/providers/workspace_cache.dart';
import '../../core/theme/app_theme.dart';

/// #4585: width-based size classes. iPhone Duo unfolded / iPad / desktop
/// widths get the two-pane layout; compact phones keep the single column
/// (and the modal workspace switcher).
enum WindowSizeClass { compact, expanded }

WindowSizeClass windowSizeClassOf(BuildContext context) {
  final width = MediaQuery.sizeOf(context).width;
  return width >= 600 ? WindowSizeClass.expanded : WindowSizeClass.compact;
}

/// Persistent master list for expanded widths (#4585): workspaces with
/// their sessions, mirroring the modal switcher's group rendering.
class SessionRail extends StatelessWidget {
  final List<WorkspaceRecord> workspaces;
  final List<CachedSessionRecord> Function(String workspaceKey) sessionsFor;
  final String activeWorkspaceKey;
  final String liveSessionId;
  final Set<String> liveSessionIds;
  final void Function(CachedSessionRecord session) onSessionTap;

  const SessionRail({
    super.key,
    required this.workspaces,
    required this.sessionsFor,
    required this.activeWorkspaceKey,
    required this.liveSessionId,
    required this.liveSessionIds,
    required this.onSessionTap,
  });

  @override
  Widget build(BuildContext context) {
    return Material(
      color: AppColors.surface,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(8, 12, 8, 24),
        children: [
          for (final workspace in workspaces)
            WorkspaceGroup(
              workspace: workspace,
              sessions: sessionsFor(workspace.key),
              isActiveWorkspace: workspace.key == activeWorkspaceKey,
              liveSessionId: liveSessionId,
              liveSessionIds: liveSessionIds,
              onSessionTap: onSessionTap,
            ),
        ],
      ),
    );
  }
}

/// A collapsible workspace group with its sessions underneath.

/// Derive display name from workspaceKey for session titles.
String _displayNameFromKeyForSession(String key, String fallback) {
  if (fallback.isNotEmpty) return fallback;
  try {
    final padded = key + '=' * ((4 - key.length % 4) % 4);
    final decoded = utf8.decode(base64Url.decode(padded));
    final parts = decoded.split('/').where((s) => s.isNotEmpty).toList();
    return parts.isNotEmpty ? parts.last : fallback;
  } catch (_) {
    return fallback;
  }
}

class WorkspaceGroup extends StatefulWidget {
  final WorkspaceRecord workspace;
  final List<CachedSessionRecord> sessions;
  final bool isActiveWorkspace;
  final String liveSessionId;
  final Set<String> liveSessionIds;
  final void Function(CachedSessionRecord) onSessionTap;

  const WorkspaceGroup({
    required this.workspace,
    required this.sessions,
    required this.isActiveWorkspace,
    required this.liveSessionId,
    required this.liveSessionIds,
    required this.onSessionTap,
  });

  @override
  State<WorkspaceGroup> createState() => WorkspaceGroupState();
}

class WorkspaceGroupState extends State<WorkspaceGroup> {
  bool _expanded = false;

  @override
  void initState() {
    super.initState();
    // Auto-expand the active workspace
    _expanded = widget.isActiveWorkspace;
  }

  @override
  Widget build(BuildContext context) {
    final hasLiveSession =
        widget.sessions.any((s) => s.sessionId == widget.liveSessionId);
    final hasBackgroundLive =
        widget.sessions.any((s) => widget.liveSessionIds.contains(s.sessionId));

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // Workspace header
        ListTile(
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(14),
          ),
          tileColor: AppColors.backgroundElevated.withValues(alpha: 0.5),
          contentPadding: EdgeInsets.zero,
          leading: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(
                _expanded ? Icons.folder_open : Icons.folder,
                color: widget.isActiveWorkspace
                    ? Colors.blueAccent
                    : AppColors.textSecondary,
                size: 20,
              ),
              if (hasLiveSession)
                Container(
                  margin: EdgeInsets.only(left: 2),
                  width: 6,
                  height: 6,
                  decoration: BoxDecoration(
                    color: Colors.greenAccent,
                    shape: BoxShape.circle,
                  ),
                ),
              if (!hasLiveSession && hasBackgroundLive)
                Container(
                  margin: EdgeInsets.only(left: 2),
                  width: 6,
                  height: 6,
                  decoration: BoxDecoration(
                    color: Colors.orangeAccent,
                    shape: BoxShape.circle,
                  ),
                ),
            ],
          ),
          title: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                widget.workspace.displayName,
                style: TextStyle(
                  color: AppColors.textPrimary,
                  fontSize: 14,
                  fontWeight: widget.isActiveWorkspace
                      ? FontWeight.w600
                      : FontWeight.normal,
                ),
              ),
              // Show parent dir when display name might be ambiguous
              if (widget.workspace.parentPath != null &&
                  widget.workspace.parentPath!.isNotEmpty)
                Text(
                  widget.workspace.parentPath!,
                  style: TextStyle(
                    color: AppColors.textMuted,
                    fontSize: 11,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
            ],
          ),
          // trailing removed — status dots are inline in leading
          trailing: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (widget.isActiveWorkspace)
                Padding(
                  padding: EdgeInsets.only(right: 4),
                  child: const Icon(Icons.check,
                      color: Colors.blueAccent, size: 18),
                ),
              Icon(
                _expanded ? Icons.expand_less : Icons.expand_more,
                color: AppColors.textSecondary,
                size: 20,
              ),
            ],
          ),
          onTap: () => setState(() => _expanded = !_expanded),
        ),
        // Sessions underneath
        if (_expanded && widget.sessions.isNotEmpty)
          Padding(
            padding: EdgeInsets.only(left: 16),
            child: Column(
              children: [
                for (final session in widget.sessions)
                  ListTile(
                    dense: true,
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(10),
                    ),
                    tileColor:
                        AppColors.backgroundElevated.withValues(alpha: 0.25),
                    contentPadding: EdgeInsets.only(left: 8, right: 8),
                    leading: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Icon(
                          session.sessionId == widget.liveSessionId
                              ? Icons.bolt
                              : Icons.chat_bubble_outline,
                          color: session.sessionId == widget.liveSessionId
                              ? Colors.greenAccent
                              : (widget.liveSessionIds
                                      .contains(session.sessionId)
                                  ? Colors.orangeAccent
                                  : AppColors.textMuted),
                          size: 16,
                        ),
                        if (session.sessionId == widget.liveSessionId ||
                            widget.liveSessionIds.contains(session.sessionId))
                          Container(
                            margin: EdgeInsets.only(left: 2),
                            width: 5,
                            height: 5,
                            decoration: BoxDecoration(
                              color: session.sessionId == widget.liveSessionId
                                  ? Colors.greenAccent
                                  : Colors.orangeAccent,
                              shape: BoxShape.circle,
                            ),
                          ),
                      ],
                    ),
                    title: Text(
                      () {
                        // Prefer host-provided session title
                        if (session.title.isNotEmpty) {
                          return session.title;
                        }
                        // Fallback: workspace name + date
                        final wsName = _displayNameFromKeyForSession(
                            widget.workspace.key, widget.workspace.displayName);
                        final date = session.sessionId.length >= 8
                            ? session.sessionId.substring(0, 8)
                            : session.sessionId;
                        return '$wsName · $date';
                      }(),
                      style: TextStyle(
                        color: AppColors.textPrimary,
                        fontSize: 13,
                      ),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                    subtitle: Text(
                      session.model.isNotEmpty
                          ? '${session.provider} / ${session.model}'
                          : session.provider,
                      style:
                          TextStyle(color: AppColors.textMuted, fontSize: 11),
                    ),
                    trailing: session.sessionId == widget.liveSessionId
                        ? Text(t('chat.live'),
                            style: TextStyle(
                              color: Colors.greenAccent,
                              fontSize: 10,
                              fontWeight: FontWeight.w700,
                            ))
                        : (widget.liveSessionIds.contains(session.sessionId)
                            ? Text('online',
                                style: TextStyle(
                                  color: Colors.orangeAccent,
                                  fontSize: 10,
                                ))
                            : Text(_formatTimeAgo(session.lastUpdatedAt),
                                style: TextStyle(
                                  color: AppColors.textMuted,
                                  fontSize: 10,
                                ))),
                    onTap: () => widget.onSessionTap(session),
                  ),
              ],
            ),
          ),
      ],
    );
  }

  String _formatTimeAgo(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inMinutes < 1) return 'now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
    if (diff.inHours < 24) return '${diff.inHours}h ago';
    if (diff.inDays < 7) return '${diff.inDays}d ago';
    return '${(diff.inDays / 7).floor()}w ago';
  }
}
