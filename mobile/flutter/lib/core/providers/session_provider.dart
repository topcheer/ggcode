import 'dart:async';
import 'dart:collection';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:path/path.dart' as p;
import 'package:path_provider/path_provider.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:sqlite3/sqlite3.dart';
import 'package:uuid/uuid.dart';

import '../connection_service.dart';
export '../connection_service.dart' show ConnectionStatus;
import '../crypto.dart';
import '../l10n/app_localizations.dart';
import '../models/protocol.dart' as proto;
import '../theme/app_theme.dart';

const _reasoningKind = 'reasoning';
const _redactedReasoningPlaceholder = 'Reasoning hidden by model.';

// ---- Connection Service Provider ----

final connectionProvider =
    NotifierProvider<ConnectionNotifier, TunnelConnectionState>(
  ConnectionNotifier.new,
);

String normalizeTunnelUrl(String raw) {
  String url = raw.trim();
  if (url.startsWith('ggcode://')) {
    url = url.replaceFirst('ggcode://', 'wss://');
  }
  if (url.startsWith('http://')) {
    url = url.replaceFirst('http://', 'ws://');
  } else if (url.startsWith('https://')) {
    url = url.replaceFirst('https://', 'wss://');
  }
  return url;
}

class TunnelConnectionState {
  final ConnectionStatus status;
  final String? url;
  final String? error;

  TunnelConnectionState({required this.status, this.url, this.error});

  TunnelConnectionState copyWith(
          {ConnectionStatus? status, String? url, String? error}) =>
      TunnelConnectionState(
        status: status ?? this.status,
        url: url ?? this.url,
        error: error ?? this.error,
      );
}

class ConnectionNotifier extends Notifier<TunnelConnectionState> {
  ConnectionService? service;
  static const _resumeClientIdKey = 'ggcode_tunnel_client_id';
  static const _resumeSessionIdKey = 'ggcode_tunnel_session_id';
  static const _resumeEventIdKey = 'ggcode_tunnel_last_event_id';

  String _clientId = '';
  String _sessionId = '';
  String _lastAppliedEventId = '';
  bool _hasAuthoritativeProjection = false;
  final List<String> _recentEventIds = <String>[];
  final Set<String> _recentEventSet = <String>{};
  Future<void>? _connectInFlight;
  String? _connectInFlightUrl;
  bool _awaitingSnapshotProjection = false;
  bool _fullHistoryReplayInProgress = false;

  @override
  TunnelConnectionState build() {
    return TunnelConnectionState(status: ConnectionStatus.disconnected);
  }

  String get currentSessionId => _sessionId;
  String get lastAppliedEventId => _lastAppliedEventId;

  Future<void> restoreSelectedWorkspace() async {
    final cache = ref.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    final workspaceKey = ref.read(workspaceCacheProvider).selectedWorkspaceKey;
    if (workspaceKey == null || workspaceKey.isEmpty) return;
    _restoreCachedAgentStatus(workspaceKey: workspaceKey);
    await connectWorkspace(workspaceKey, clearState: false);
  }

  Future<void> connectWorkspace(String workspaceKey,
      {bool clearState = true}) async {
    final cache = ref.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    final url = cache.urlForWorkspace(workspaceKey);
    if (url == null || url.isEmpty) return;
    _restoreCachedAgentStatus(workspaceKey: workspaceKey);
    await connect(url, clearState: clearState);
  }

  Future<void> connectScannedCode(String rawCode,
      {bool clearState = true}) async {
    final url = normalizeTunnelUrl(rawCode);
    if (url.isEmpty) return;
    await connect(url, clearState: clearState);
  }

  ConnectionService createConnectionService(String url, TunnelCrypto crypto) =>
      ConnectionService(url: url, crypto: crypto);

  Future<void> connect(String url, {bool clearState = true}) async {
    url = normalizeTunnelUrl(url);
    if (_connectInFlight != null && _connectInFlightUrl == url) {
      return _connectInFlight!;
    }
    final future = _connectImpl(url, clearState: clearState);
    _connectInFlight = future;
    _connectInFlightUrl = url;
    try {
      await future;
    } finally {
      if (identical(_connectInFlight, future)) {
        _connectInFlight = null;
        _connectInFlightUrl = null;
      }
    }
  }

  Future<void> _connectImpl(String url, {required bool clearState}) async {
    final cache = ref.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    await cache.activateWorkspaceUrl(url);

    // Disconnect previous if any
    if (service != null) {
      service!.dispose();
      service = null;
    }

    state = state.copyWith(
        status: ConnectionStatus.connecting, url: url, error: null);

    // Extract token from URL for encryption
    final token = _extractToken(url) ?? '';
    if (token.isEmpty) {
      state = state.copyWith(
          status: ConnectionStatus.disconnected,
          error: 'Invalid URL: no token');
      return;
    }

    final crypto = TunnelCrypto(token);
    service = createConnectionService(url, crypto);
    await _loadResumeState();
    var restoredProjection = false;
    if (!clearState && _hasEmptyUiProjection()) {
      restoredProjection = _restoreProjectionFromCache(
        adoptCursor: false,
        seedCursorIfUnset: true,
      );
    } else if (clearState) {
      // Fresh/manual connects should not synchronously restore large cached
      // projections before the relay binds the active session. That work can
      // stall the first-connect UX and briefly render stale history for the
      // wrong room. We restore again once active_session/resume_ack arrives.
      _clearUiProjection();
      _sessionId = '';
      _lastAppliedEventId = '';
        _awaitingSnapshotProjection = false;
    _fullHistoryReplayInProgress = false;
      _fullHistoryReplayInProgress = false;
          _recentEventIds.clear();
      _recentEventSet.clear();
    }
    if (!restoredProjection && _hasEmptyUiProjection()) {
      // A stale cursor without restored local UI can skip earlier subagent_spawn
      // events, which leaves teammate/subagent tabs missing after reconnect.
      _lastAppliedEventId = '';
    }

    // Listen to connection status changes
    service!.statusStream.listen(
      (status) {
        state = state.copyWith(status: status);
        if (status == ConnectionStatus.connected) {
          _saveUrl(url);
          service?.sendResumeHello(
            clientId: _clientId,
            sessionId: _sessionId.isNotEmpty ? _sessionId : null,
          );
        }
      },
    );

    // Listen to error messages
    service!.errorStream.listen(
      (error) {
        if (isPermanentRoomFailureMessage(error)) {
          unawaited(_handlePermanentRoomFailure(url, error));
          return;
        }
        state = state.copyWith(error: error);
      },
    );

    // Listen to messages from server
    service!.messageStream.listen(
      (msg) => _dispatchMessage(msg),
    );

    // Listen to ack events and forward to ChatNotifier
    service!.ackStream.listen(
      (ack) {
        final chatNotifier = ref.read(chatProvider.notifier);
        switch (ack.type) {
          case 'relay_ack':
            chatNotifier.updateMessageStatus(
                ack.messageId, MessageStatus.delivered);
            break;
          case 'server_ack':
            chatNotifier.updateMessageStatus(
                ack.messageId, MessageStatus.acknowledged);
            break;
        }
      },
    );

    try {
      // dart:io WebSocket.connect properly awaits handshake
      await service!.connect();
    } catch (e) {
      state = state.copyWith(
          status: ConnectionStatus.disconnected, error: e.toString());
    }
  }

  /// Reconnect using the last known URL (e.g. after app resumes from background).
  /// Preserves existing chat state — server will replay recent messages.
  Future<void> reconnect() async {
    final url = state.url;
    if (url == null || url.isEmpty) {
      await restoreSelectedWorkspace();
      return;
    }
    await connect(url, clearState: false);
  }

  void disconnect() {
    service?.disconnect();
    ref.read(workspaceCacheProvider.notifier).markDisconnected();
    state = state.copyWith(status: ConnectionStatus.disconnected);
  }

  Future<void> leaveSession() async {
    service?.dispose();
    service = null;
    _clearUiProjection();
    _sessionId = '';
    _lastAppliedEventId = '';
    _awaitingSnapshotProjection = false;
    _fullHistoryReplayInProgress = false;
    _recentEventIds.clear();
    _recentEventSet.clear();
    final prefs = await SharedPreferences.getInstance();
    await _clearPersistedResumeState(prefs);
    await ref.read(workspaceCacheProvider.notifier).clearSelection();
    state = TunnelConnectionState(status: ConnectionStatus.disconnected);
  }

  void send(Map<String, dynamic> data) {
    final msg = proto.WsMessage(
        type: data['type'] as String? ?? 'message',
        messageId: data['message_id'] as String?,
        data: data['data'] as Map<String, dynamic>?);
    service?.sendEncrypted(msg);
  }

  String? _extractToken(String url) {
    final uri = Uri.tryParse(url);
    return uri?.queryParameters['token'];
  }

  void _dispatchMessage(proto.WsMessage msg) {
    final chatNotifier = ref.read(chatProvider.notifier);

    // Handle server_ack from encrypted channel (Desktop → Client ack).
    if (msg.type == 'server_ack') {
      final messageId = msg.data?['message_id'] as String? ?? '';
      if (messageId.isNotEmpty) {
        chatNotifier.updateMessageStatus(messageId, MessageStatus.acknowledged);
      }
      return;
    }

    switch (msg.type) {
      case 'active_session':
        final sessionId =
            msg.sessionId ?? msg.data?['session_id'] as String? ?? '';
        if (sessionId.isEmpty) break;
        if (_sessionId.isNotEmpty && _sessionId != sessionId) {
          _clearUiProjection();
          _lastAppliedEventId = '';
                      _hasAuthoritativeProjection = false;
          _recentEventIds.clear();
          _recentEventSet.clear();
        }
        _sessionId = sessionId;
        unawaited(ref.read(workspaceCacheProvider.notifier).registerLiveSession(
            sessionId, ref.read(sessionInfoProvider),
            lastEventId: _lastAppliedEventId));
        _restoreSessionProjectionIfAvailable(sessionId);
        _persistResumeState();
        break;

      case 'resume_ack':
        final resumeMode = msg.data?['resume_mode'] as String? ?? 'incremental';
            final sessionId =
            msg.sessionId ?? msg.data?['session_id'] as String? ?? '';
        _sessionId = sessionId;
        if (resumeMode == 'full_history') {
          _clearUiProjection();
          _lastAppliedEventId = '';
                            _recentEventIds.clear();
          _recentEventSet.clear();
          _awaitingSnapshotProjection = false;
    _fullHistoryReplayInProgress = false;
          _fullHistoryReplayInProgress = true;
          // Prevent an earlier async cache-restore task (scheduled from
          // active_session) from reseeding a stale cursor while authoritative
          // full-history replay is in flight.
          _markProjectionAuthoritative();
        } else {
          // For incremental resume, clear pending events and cancel the
          // watchdog.  Replay events will flow in and be accepted directly
          // If a gap still exists after replay completes, a fresh
          // checks during the replay burst.
        }
        unawaited(ref.read(workspaceCacheProvider.notifier).registerLiveSession(
            sessionId, ref.read(sessionInfoProvider),
            lastEventId: _lastAppliedEventId));
        if (resumeMode != 'full_history') {
          _restoreSessionProjectionIfAvailable(sessionId);
        }
        _restoreCachedAgentStatus(sessionId: sessionId);
        _persistResumeState();
        break;

      case 'resume_miss':
                    break;

      case 'language_change':
        if (msg.data != null) {
          final lang = msg.data!['language'] as String? ?? '';
          if (lang.isNotEmpty) {
            ref.read(languageProvider.notifier).setLanguage(lang);
            unawaited(loadTranslations(lang));
          }
        }
        break;

      case 'theme_change':
        if (msg.data != null) {
          final theme = msg.data!['theme'] as String? ?? '';
          if (theme.isNotEmpty) {
            ref.read(themeProvider.notifier).setTheme(theme);
          }
        }
        break;

      case 'snapshot_reset':
        _clearUiProjection();
        _lastAppliedEventId = '';
                    _recentEventIds.clear();
        _recentEventSet.clear();
        _awaitingSnapshotProjection = true;
        // Prevent late cache-restore callbacks from reviving stale snapshots
        // after the desktop has started an authoritative reset.
        _markProjectionAuthoritative();
        if (msg.sessionId != null && msg.sessionId!.isNotEmpty) {
          _sessionId = msg.sessionId!;
        }
        unawaited(ref.read(workspaceCacheProvider.notifier).registerLiveSession(
            _sessionId, ref.read(sessionInfoProvider),
            lastEventId: _lastAppliedEventId));
        _restoreCachedAgentStatus(sessionId: _sessionId);
        _persistResumeState();
        break;

      case 'session_info':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        final data = proto.SessionInfoData.fromJson(msg.data!);
        ref.read(sessionInfoProvider.notifier).set(data);
        ref.read(currentModeProvider.notifier).set(data.mode);
        // Sync language from desktop
        if (data.language.isNotEmpty) {
          ref.read(languageProvider.notifier).setLanguage(data.language);
          unawaited(loadTranslations(data.language));
        }
        if (data.theme.isNotEmpty) {
          ref.read(themeProvider.notifier).setTheme(data.theme);
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        if (_awaitingSnapshotProjection) {
          _awaitingSnapshotProjection = false;
    _fullHistoryReplayInProgress = false;
        }
        if (_fullHistoryReplayInProgress) {
          _fullHistoryReplayInProgress = false;
        }
        unawaited(ref.read(workspaceCacheProvider.notifier).registerLiveSession(
              _sessionId.isNotEmpty ? _sessionId : (msg.sessionId ?? ''),
              data,
              lastEventId: _lastAppliedEventId,
            ));
        break;

      case 'activity':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.ActivityData.fromJson(msg.data!);
          _setAgentActivity(data.activity);
        }
        _markEventApplied(msg);
        break;

      case 'user_message':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.MessageData.fromJson(msg.data!);
          final displayText =
              data.displayText.isNotEmpty ? data.displayText : data.text;
          final remoteMessageId = msg.eventId ??
              'remote-user-${DateTime.now().millisecondsSinceEpoch}';
          if (data.kind == 'cron') {
            chatNotifier.addRemoteSystemMessage(
              displayText.isNotEmpty ? displayText : '⏰ Cron job triggered',
              messageId: remoteMessageId,
              kind: data.kind,
            );
          } else if (displayText.isNotEmpty) {
            final absorbed = chatNotifier.bindRemoteUserMessage(
              data.text,
              remoteMessageId: remoteMessageId,
            );
            if (!absorbed) {
              chatNotifier.addRemoteUserMessage(
                displayText,
                messageId: remoteMessageId,
                kind: data.kind,
              );
            }
          }
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'system_message':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.MessageData.fromJson(msg.data!);
          final displayText =
              data.displayText.isNotEmpty ? data.displayText : data.text;
          if (displayText.isNotEmpty) {
            chatNotifier.addRemoteSystemMessage(
              displayText,
              messageId: msg.eventId ??
                  'remote-system-${DateTime.now().millisecondsSinceEpoch}',
              kind: data.kind,
            );
          }
        }
        _markEventApplied(msg);
        break;

      case 'text':
      case 'stream_text':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.TextData.fromJson(msg.data!);
          final text = data.chunk.isNotEmpty
              ? data.chunk
              : (msg.data!['text'] as String? ?? '');
          final msgId = data.id.isNotEmpty
              ? data.id
              : 'msg-${DateTime.now().millisecondsSinceEpoch}';
          chatNotifier.handleTextChunk(proto.TextData(
            id: msgId,
            chunk: text,
            done: data.done,
            kind: data.kind,
          ));
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'reasoning':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.TextData.fromJson(msg.data!);
          final reasoningId = data.id.isNotEmpty
              ? data.id
              : 'reasoning-${DateTime.now().millisecondsSinceEpoch}';
          chatNotifier.handleReasoningChunk(
            proto.TextData(
              id: reasoningId,
              chunk: data.chunk,
              done: data.done,
            ),
          );
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'stream_start':
        break;

      case 'stream_end':
      case 'text_done':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        final msgId = msg.data?['id'] as String? ?? msg.streamId;
        if (msgId != null && msgId.isNotEmpty) {
          chatNotifier.finalizeStreaming(msgId);
        }
        _markEventApplied(msg);
        break;

      case 'reasoning_done':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        final msgId = msg.data?['id'] as String? ?? msg.streamId;
        if (msgId != null && msgId.isNotEmpty) {
          chatNotifier.finalizeReasoning(msgId);
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'status':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.StatusData.fromJson(msg.data!);
          final normalized = _normalizeAgentStatus(data.status);
          _setAgentStatus(normalized, '');
          if (normalized == 'idle') {
            _setAgentActivity('');
          } else if (data.message.isNotEmpty) {
            // Backward compatibility with older tunnel senders that packed the
            // transient activity text into status.message.
            _setAgentActivity(data.message);
          }
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'tool_call':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          chatNotifier.handleToolCall(
            proto.ToolCallData.fromJson(msg.data!),
            messageId: msg.eventId ??
                msg.streamId ??
                'tool-${DateTime.now().millisecondsSinceEpoch}',
          );
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'tool_result':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          chatNotifier
              .handleToolResult(proto.ToolResultData.fromJson(msg.data!));
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'approval_request':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.ApprovalRequestData.fromJson(msg.data!);
          ref.read(approvalProvider.notifier).set(ApprovalInfo(
              id: data.id, toolName: data.toolName, input: data.input));
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'approval_result':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.ApprovalResultData.fromJson(msg.data!);
          final approval = ref.read(approvalProvider);
          if (approval != null && approval.id == data.id) {
            ref.read(approvalProvider.notifier).set(null);
          }
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'ask_user_request':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.AskUserRequestData.fromJson(msg.data!);
          // Build a human-readable summary of the questions
          final detail =
              data.questions.map(_describeAskUserQuestion).join('\n');
          final amsgId = msg.eventId ?? _newAskUserMessageId();
          chatNotifier.addAskUserRequest(amsgId, data.title, detail);
          ref.read(askUserProvider.notifier).set(AskUserInfo(
              id: data.id,
              title: data.title,
              questions: data.questions,
              msgId: amsgId));
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'ask_user_response':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.AskUserResponseData.fromJson(msg.data!);
          final askUser = ref.read(askUserProvider);
          if (askUser != null && askUser.id == data.id) {
            if (askUser.msgId.isNotEmpty) {
              chatNotifier.updateAskUserAnswer(
                askUser.msgId,
                _summarizeAskUserResponse(
                    askUser.questions, data.answers, data.status),
              );
            }
            ref.read(askUserProvider.notifier).set(null);
          }
        }
        _markEventApplied(msg);
        break;

      case 'subagent_spawn':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.SubagentSpawnData.fromJson(msg.data!);
          _upsertSubagent(
            agentId: data.agentId,
            name: data.name,
            task: data.task,
            color: data.color,
            parentId: data.parentId,
            status: 'running',
          );
        }
        _markEventApplied(msg);
        break;

      case 'subagent_text':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.SubagentTextData.fromJson(msg.data!);
          _upsertSubagent(
            agentId: data.agentId,
            status: 'running',
          );
          chatNotifier.handleSubagentText(data);
        }
        _markEventApplied(msg);
        break;

      case 'subagent_reasoning':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.SubagentReasoningData.fromJson(msg.data!);
          _upsertSubagent(
            agentId: data.agentId,
            status: 'thinking',
          );
          chatNotifier.handleSubagentReasoning(data);
        }
        _markEventApplied(msg);
        break;

      case 'subagent_reasoning_done':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.SubagentReasoningData.fromJson(msg.data!);
          final reasoningId = '${data.agentId}-${data.id}';
          chatNotifier.finalizeReasoning(reasoningId);
        }
        _markEventApplied(msg);
        break;

      case 'subagent_status':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.SubagentStatusData.fromJson(msg.data!);
          _upsertSubagent(
            agentId: data.agentId,
            status: data.status,
          );
        }
        _markEventApplied(msg);
        break;

      case 'subagent_complete':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.SubagentCompleteData.fromJson(msg.data!);
          chatNotifier._finalizePendingReasoning(
            sourceId: data.agentId,
            collapse: true,
          );
          _upsertSubagent(
            agentId: data.agentId,
            name: data.name,
            status: 'completed',
            completed: true,
            success: data.success,
            summary: data.summary,
          );
          // Auto-remove completed agent tab after 5 seconds
          Future.delayed(const Duration(seconds: 5), () {
            final current =
                Map<String, SubagentInfo>.from(ref.read(subagentProvider));
            current.remove(data.agentId);
            ref.read(subagentProvider.notifier).set(current);
            // Also remove messages for this agent
            final msgs = ref.read(chatProvider);
            ref
                .read(chatProvider.notifier)
                .set(msgs.where((m) => m.sourceId != data.agentId).toList());
          });
        }
        _markEventApplied(msg);
        break;

      case 'subagent_tool_call':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.SubagentToolCallData.fromJson(msg.data!);
          final agent = _upsertSubagent(
            agentId: data.agentId,
            status: 'running',
          );
          final chatNotifier = ref.read(chatProvider.notifier);
          chatNotifier.addSubagentToolCall(
            messageId: msg.eventId ?? data.toolId,
            agentId: data.agentId,
            toolId: data.toolId,
            toolName: data.toolName,
            displayName: data.displayName,
            args: data.args,
            detail: data.detail,
            sourceName: agent.name,
            sourceColor: agent.color,
          );
        }
        _markEventApplied(msg);
        break;

      case 'subagent_tool_result':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final data = proto.SubagentToolResultData.fromJson(msg.data!);
          _upsertSubagent(agentId: data.agentId);
          final chatNotifier = ref.read(chatProvider.notifier);
          chatNotifier.updateSubagentToolResult(
            agentId: data.agentId,
            toolId: data.toolId,
            toolName: data.toolName,
            result: data.result,
            summary: data.summary,
            payload: data.payload,
            payloadMode: data.payloadMode,
            isError: data.isError,
          );
        }
        _markEventApplied(msg);
        break;

      case 'error':
        if (!_shouldApplyEvent(msg)) { _ackSkippedEvent(msg); break; }
        if (msg.data != null) {
          final errMsg = msg.data!['message'] as String? ?? 'Unknown error';
          chatNotifier.addErrorMessage(
            errMsg,
            messageId:
                msg.eventId ?? 'error-${DateTime.now().millisecondsSinceEpoch}',
          );
        }
        _markEventApplied(msg);
        break;

      case 'server_offline':
        // Relay told us the room is temporarily offline. Keep the current
        // projection and selection so reconnect stays pinned to this room.
        ref.read(workspaceCacheProvider.notifier).markDisconnected();
        state = state.copyWith(
          status: ConnectionStatus.disconnected,
          error: state.error?.isNotEmpty == true
              ? state.error
              : 'Relay recovering',
        );
        break;

      case 'sharing_stopped':
        unawaited(_handlePermanentRoomFailure(
          state.url ?? '',
          'Sharing stopped',
        ));
        break;
    }
  }

  void _clearUiProjection() {
    ref.read(chatProvider.notifier).clearMessages();
    ref.read(subagentProvider.notifier).clear();
    ref.read(approvalProvider.notifier).set(null);
    ref.read(askUserProvider.notifier).set(null);
    ref.read(sessionInfoProvider.notifier).set(null);
    ref.read(currentModeProvider.notifier).set('supervised');
    _setAgentStatus('idle', '');
    _setAgentActivity('');
    _hasAuthoritativeProjection = false;
  }

  void _setAgentStatus(String status, String message) {
    ref.read(agentStatusProvider.notifier).set(_normalizeAgentStatus(status));
    if (message.isNotEmpty) {
      ref.read(agentStatusMessageProvider.notifier).set(message);
    }
  }

  void _setAgentActivity(String activity) {
    ref.read(agentStatusMessageProvider.notifier).set(activity);
  }

  String _normalizeAgentStatus(String status) {
    switch (status) {
      case 'busy':
      case 'running':
      case 'thinking':
      case 'waiting':
      case 'error':
        return 'busy';
      default:
        return 'idle';
    }
  }

  void _restoreCachedAgentStatus({String? workspaceKey, String? sessionId}) {
    final cacheState = ref.read(workspaceCacheProvider);
    final resolvedWorkspaceKey = workspaceKey ??
        cacheState.selectedWorkspaceKey ??
        cacheState.liveWorkspaceKey;
    final resolvedSessionId =
        sessionId ?? cacheState.selectedSessionId ?? cacheState.liveSessionId;
    if (resolvedWorkspaceKey == null ||
        resolvedWorkspaceKey.isEmpty ||
        resolvedSessionId == null ||
        resolvedSessionId.isEmpty) {
      return;
    }
    final snapshot = ref
        .read(workspaceCacheProvider.notifier)
        .snapshotFor(resolvedWorkspaceKey, resolvedSessionId);
    if (snapshot == null) return;
    _setAgentStatus(snapshot.agentStatus, '');
    _setAgentActivity(snapshot.agentStatusMessage);
  }

  Future<void> _saveUrl(String url) async {
    final prefs = await SharedPreferences.getInstance();
    final urls = prefs.getStringList('ggcode_history') ?? [];
    if (!urls.contains(url)) {
      urls.insert(0, url);
      if (urls.length > 10) urls.removeLast();
      prefs.setStringList('ggcode_history', urls);
    }
  }

  static Future<List<String>> loadHistory() async {
    final prefs = await SharedPreferences.getInstance();
    return prefs.getStringList('ggcode_history') ?? [];
  }

  Future<void> _loadResumeState() async {
    final prefs = await SharedPreferences.getInstance();
    _clientId = prefs.getString(_resumeClientIdKey) ?? '';
    if (_clientId.isEmpty) {
      _clientId = const Uuid().v4();
      await prefs.setString(_resumeClientIdKey, _clientId);
    }
    _sessionId = prefs.getString(_resumeSessionIdKey) ?? _sessionId;
    _lastAppliedEventId =
        prefs.getString(_resumeEventIdKey) ?? _lastAppliedEventId;
  }

  Future<void> _handlePermanentRoomFailure(
      String failedUrl, String error) async {
    final normalizedFailedUrl = normalizeTunnelUrl(failedUrl);
    final currentUrl = normalizeTunnelUrl(state.url ?? '');
    if (normalizedFailedUrl.isEmpty || currentUrl != normalizedFailedUrl) {
      return;
    }
    service?.dispose();
    service = null;
    _clearUiProjection();
    _sessionId = '';
    _lastAppliedEventId = '';
    _awaitingSnapshotProjection = false;
    _fullHistoryReplayInProgress = false;
    _recentEventIds.clear();
    _recentEventSet.clear();
    final prefs = await SharedPreferences.getInstance();
    await _clearPersistedResumeState(prefs);
    await ref.read(workspaceCacheProvider.notifier).clearSelection();
    if (!ref.mounted) return;
    state = TunnelConnectionState(
      status: ConnectionStatus.disconnected,
      error: error,
    );
  }

  bool _shouldApplyEvent(proto.WsMessage msg) {
    final eventId = msg.eventId;
    final sessionId = msg.sessionId ?? _sessionId;
    if (sessionId.isNotEmpty &&
        _sessionId.isNotEmpty &&
        sessionId != _sessionId) {
      final previousSessionId = _sessionId;
      _clearUiProjection();
      _hasAuthoritativeProjection = true;
      _recentEventIds.clear();
      _recentEventSet.clear();
      _lastAppliedEventId = '';
      _sessionId = sessionId;
      unawaited(ref.read(workspaceCacheProvider.notifier).observeLiveSession(
          sessionId,
          previousSessionId: previousSessionId,
          sessionInfo: ref.read(sessionInfoProvider)));
    } else if (sessionId.isNotEmpty) {
      _sessionId = sessionId;
    }
    if (eventId == null || eventId.isEmpty) {
      return true;
    }
    // Dedup: exact match in recent window.
    if (_recentEventSet.contains(eventId)) {
      return false;
    }
    // Skip already-cached events: relay may replay events earlier than our
    // snapshot's lastEventId (ACK latency).  Skip + ACK so relay advances.
    final ord = _parseEventOrdinal(eventId);
    final last = _parseEventOrdinal(_lastAppliedEventId);
    if (ord != null && last != null && ord <= last) {
      return false;
    }
    return true;
  }

  /// ACK a skipped (already-cached) event so relay can advance its cursor.
  void _ackSkippedEvent(proto.WsMessage msg) {
    final eventId = msg.eventId;
    if (eventId != null && eventId.isNotEmpty && _clientId.isNotEmpty) {
      service?.sendAck(clientId: _clientId, eventId: eventId);
    }
  }

  void _markEventApplied(proto.WsMessage msg) {
    final eventId = msg.eventId;
    if (msg.sessionId != null && msg.sessionId!.isNotEmpty) {
      _sessionId = msg.sessionId!;
    }
    if (eventId == null || eventId.isEmpty) {
      _persistResumeState();
      unawaited(ref
          .read(workspaceCacheProvider.notifier)
          .updateLiveCursor(_sessionId, _lastAppliedEventId));
      return;
    }
    _lastAppliedEventId = eventId;
    // Send ACK to relay so it advances the cursor.
    if (_clientId.isNotEmpty) {
      service?.sendAck(clientId: _clientId, eventId: eventId);
    }
    _recentEventSet.add(eventId);
    _recentEventIds.add(eventId);
    if (_recentEventIds.length > 1000) {
      final evicted = _recentEventIds.removeAt(0);
      _recentEventSet.remove(evicted);
    }
    _persistResumeState();
    unawaited(ref
        .read(workspaceCacheProvider.notifier)
        .updateLiveCursor(_sessionId, _lastAppliedEventId));
  }

  int? _parseEventOrdinal(String? eventId) {
    if (eventId == null || eventId.isEmpty) return null;
    final idx = eventId.lastIndexOf('-');
    final raw = idx >= 0 ? eventId.substring(idx + 1) : eventId;
    return int.tryParse(raw);
  }


  Future<void> _reconnectAfterReplayFailure(String url) async {
    await _persistResumeStateNow();
    await connect(url, clearState: false);
  }

  bool _hasEmptyUiProjection() {
    return ref.read(chatProvider).isEmpty &&
        ref.read(subagentProvider).isEmpty &&
        ref.read(sessionInfoProvider) == null;
  }

  bool _hasReplayCursorBaseline() {
    return _sessionId.isNotEmpty && _lastAppliedEventId.isNotEmpty;
  }

  bool _canRestoreSessionProjection() {
    return !_hasAuthoritativeProjection &&
        ref.read(subagentProvider).isEmpty &&
        ref.read(sessionInfoProvider) == null;
  }

  bool _restoreProjectionFromCache({
    bool adoptCursor = true,
    bool seedCursorIfUnset = false,
  }) {
    final cacheState = ref.read(workspaceCacheProvider);
    final workspaceKey = cacheState.selectedWorkspaceKey;
    final sessionId = cacheState.selectedSessionId;
    if (workspaceKey == null ||
        workspaceKey.isEmpty ||
        sessionId == null ||
        sessionId.isEmpty) {
      return false;
    }
    final snapshot = ref
        .read(workspaceCacheProvider.notifier)
        .snapshotFor(workspaceKey, sessionId);
    if (snapshot == null) {
      return false;
    }
    ref
        .read(chatProvider.notifier)
        .set(List<ChatMessage>.from(snapshot.messages));
    ref.read(subagentProvider.notifier).set(
          Map<String, SubagentInfo>.from(snapshot.subagents),
        );
    ref.read(sessionInfoProvider.notifier).set(snapshot.sessionInfo);
    if (snapshot.sessionInfo != null && snapshot.sessionInfo!.mode.isNotEmpty) {
      ref.read(currentModeProvider.notifier).set(snapshot.sessionInfo!.mode);
    }
    _setAgentStatus(snapshot.agentStatus, '');
    _setAgentActivity(snapshot.agentStatusMessage);
    final snapshotCursor = snapshot.lastEventId;
    if (adoptCursor) {
      _sessionId = sessionId;
      _lastAppliedEventId = snapshotCursor;
    } else if (seedCursorIfUnset &&
        _lastAppliedEventId.isEmpty &&
        snapshotCursor.isNotEmpty) {
      if (_sessionId.isEmpty || _sessionId == sessionId) {
        _sessionId = sessionId;
        _lastAppliedEventId = snapshotCursor;
      }
    }
    return true;
  }

  void _restoreSessionProjectionIfAvailable(String sessionId) {
    if (sessionId.isEmpty || !_canRestoreSessionProjection()) {
      return;
    }
    unawaited(
      ref
          .read(workspaceCacheProvider.notifier)
          .attachSessionToActiveWorkspace(sessionId)
          .then((restored) {
        if (restored && _canRestoreSessionProjection()) {
          _restoreProjectionFromCache(
            adoptCursor: false,
            seedCursorIfUnset: true,
          );
        }
      }),
    );
  }

  void _markProjectionAuthoritative() {
    _hasAuthoritativeProjection = true;
  }

  void handleIncomingForTest(proto.WsMessage msg) {
    _dispatchMessage(msg);
  }

  bool restoreProjectionFromCacheForTest({bool adoptCursor = true}) {
    return _restoreProjectionFromCache(adoptCursor: adoptCursor);
  }

  SubagentInfo _upsertSubagent({
    required String agentId,
    String? name,
    String? task,
    String? color,
    String? parentId,
    String? status,
    bool? completed,
    bool? success,
    String? summary,
  }) {
    final agents = Map<String, SubagentInfo>.from(ref.read(subagentProvider));
    final existing = agents[agentId];
    final next = SubagentInfo(
      agentId: agentId,
      name: (name != null && name.trim().isNotEmpty)
          ? name.trim()
          : existing?.name ?? agentId,
      task: (task != null && task.trim().isNotEmpty)
          ? task.trim()
          : existing?.task ?? '',
      color: (color != null && color.trim().isNotEmpty)
          ? color.trim()
          : existing?.color ?? '#4CAF50',
      parentId: (parentId != null && parentId.trim().isNotEmpty)
          ? parentId.trim()
          : existing?.parentId ?? '',
      status: (status != null && status.trim().isNotEmpty)
          ? status.trim()
          : existing?.status ?? 'running',
      completed: completed ?? existing?.completed ?? false,
      success: success ?? existing?.success ?? false,
      summary: (summary != null && summary.trim().isNotEmpty)
          ? summary.trim()
          : existing?.summary,
    );
    agents[agentId] = next;
    ref.read(subagentProvider.notifier).set(agents);
    return next;
  }

  void _persistResumeState() {
    unawaited(_persistResumeStateNow());
  }

  Future<void> _persistResumeStateNow() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_resumeClientIdKey, _clientId);
    await prefs.setString(_resumeSessionIdKey, _sessionId);
    await prefs.setString(_resumeEventIdKey, _lastAppliedEventId);
  }

  Future<void> _clearPersistedResumeState([SharedPreferences? prefs]) async {
    final store = prefs ?? await SharedPreferences.getInstance();
    await store.remove(_resumeSessionIdKey);
    await store.remove(_resumeEventIdKey);
  }
}

// ---- Message delivery status for ack tracking ----

enum MessageStatus {
  sending, // not yet acknowledged by relay
  delivered, // relay acknowledged (relay_ack received)
  acknowledged, // desktop acknowledged (server_ack received)
  failed, // timed out waiting for relay_ack
}

// ---- Chat Messages Provider ----

class ChatMessage {
  final String id;
  final String? sourceId; // null = main agent
  final String? sourceName;
  final String? sourceColor;
  final String kind;
  final bool isUser;
  final String text;
  final bool streaming;
  final String? toolId;
  final String? toolName;
  final String? toolDisplayName;
  final String? toolDetail;
  final String? toolResult;
  final String? toolPayload;
  final String? toolPayloadMode;
  final bool toolCompleted;
  final bool isToolError;
  final bool reasoningCollapsed;
  final DateTime time;
  final MessageStatus status; // ack tracking for user messages

  ChatMessage({
    required this.id,
    this.sourceId,
    this.sourceName,
    this.sourceColor,
    this.kind = '',
    this.isUser = false,
    this.text = '',
    this.streaming = false,
    this.toolId,
    this.toolName,
    this.toolDisplayName,
    this.toolDetail,
    this.toolResult,
    this.toolPayload,
    this.toolPayloadMode,
    this.toolCompleted = false,
    this.isToolError = false,
    this.reasoningCollapsed = false,
    required this.time,
    this.status = MessageStatus.acknowledged, // default for server-originated
  });

  ChatMessage copyWith({
    String? id,
    String? text,
    String? kind,
    bool? streaming,
    String? toolResult,
    String? toolPayload,
    String? toolPayloadMode,
    bool? toolCompleted,
    bool? isToolError,
    bool? reasoningCollapsed,
    MessageStatus? status,
  }) =>
      ChatMessage(
        id: id ?? this.id,
        sourceId: sourceId,
        sourceName: sourceName,
        sourceColor: sourceColor,
        kind: kind ?? this.kind,
        isUser: isUser,
        text: text ?? this.text,
        streaming: streaming ?? this.streaming,
        toolId: toolId,
        toolName: toolName,
        toolDisplayName: toolDisplayName,
        toolDetail: toolDetail,
        toolResult: toolResult ?? this.toolResult,
        toolPayload: toolPayload ?? this.toolPayload,
        toolPayloadMode: toolPayloadMode ?? this.toolPayloadMode,
        toolCompleted: toolCompleted ?? this.toolCompleted,
        isToolError: isToolError ?? this.isToolError,
        reasoningCollapsed: reasoningCollapsed ?? this.reasoningCollapsed,
        time: time,
        status: status ?? this.status,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'source_id': sourceId,
        'source_name': sourceName,
        'source_color': sourceColor,
        'kind': kind,
        'is_user': isUser,
        'text': text,
        'streaming': streaming,
        'tool_id': toolId,
        'tool_name': toolName,
        'tool_display_name': toolDisplayName,
        'tool_detail': toolDetail,
        'tool_result': toolResult,
        'tool_payload': toolPayload,
        'tool_payload_mode': toolPayloadMode,
        'tool_completed': toolCompleted,
        'is_tool_error': isToolError,
        'reasoning_collapsed': reasoningCollapsed,
        'time': time.toIso8601String(),
      };

  factory ChatMessage.fromJson(Map<String, dynamic> json) => ChatMessage(
        id: json['id'] as String? ?? '',
        sourceId: json['source_id'] as String?,
        sourceName: json['source_name'] as String?,
        sourceColor: json['source_color'] as String?,
        kind: json['kind'] as String? ?? '',
        isUser: json['is_user'] as bool? ?? false,
        text: json['text'] as String? ?? '',
        streaming: json['streaming'] as bool? ?? false,
        toolId: json['tool_id'] as String?,
        toolName: json['tool_name'] as String?,
        toolDisplayName: json['tool_display_name'] as String?,
        toolDetail: json['tool_detail'] as String?,
        toolResult: json['tool_result'] as String?,
        toolPayload: json['tool_payload'] as String?,
        toolPayloadMode: json['tool_payload_mode'] as String?,
        toolCompleted: json['tool_completed'] as bool? ?? false,
        isToolError: json['is_tool_error'] as bool? ?? false,
        reasoningCollapsed: json['reasoning_collapsed'] as bool? ?? false,
        time:
            DateTime.tryParse(json['time'] as String? ?? '') ?? DateTime.now(),
      );
}

final chatProvider = NotifierProvider<ChatNotifier, List<ChatMessage>>(
  ChatNotifier.new,
);

class ChatNotifier extends Notifier<List<ChatMessage>> {
  int _msgCounter = 0;

  @override
  List<ChatMessage> build() => [];

  /// Send a user message with ack tracking.
  /// Generates a message_id, adds the message in 'sending' status,
  /// and sets a 5s timeout to mark as failed if no relay_ack arrives.
  void addUserMessage(String text) {
    final messageId =
        'user-${_msgCounter++}-${DateTime.now().millisecondsSinceEpoch}';
    final msg = ChatMessage(
      id: messageId,
      isUser: true,
      text: text,
      time: DateTime.now(),
      status: MessageStatus.sending,
    );
    state = [...state, msg];

    // Send with message_id for ack tracking.
    ref.read(connectionProvider.notifier).send({
      'type': 'message',
      'message_id': messageId,
      'data': {'text': text, 'message_id': messageId},
    });

    // 5s timeout: if no relay_ack by then, assume delivered via TCP.
    // This handles the case where an older relay doesn't send relay_ack.
    // Only mark as truly failed if the connection itself is broken.
    Future.delayed(const Duration(seconds: 5), () {
      final idx = state.indexWhere((m) => m.id == messageId);
      if (idx < 0) return;
      final current = state[idx];
      if (current.status == MessageStatus.sending) {
        final connState = ref.read(connectionProvider);
        final newStatus = connState.status == ConnectionStatus.connected
            ? MessageStatus.delivered // connection ok, assume TCP delivered
            : MessageStatus.failed; // connection lost, likely failed
        state = [
          for (int i = 0; i < state.length; i++)
            if (i == idx) state[i].copyWith(status: newStatus) else state[i],
        ];
      }
    });
  }

  /// Update message status by message_id (called when ack events arrive).
  void updateMessageStatus(String messageId, MessageStatus status) {
    final idx = state.indexWhere((m) => m.id == messageId);
    if (idx < 0) return;
    // Only advance status forward: sending → delivered → acknowledged.
    final current = state[idx].status;
    if (status.index <= current.index) return;
    state = [
      for (int i = 0; i < state.length; i++)
        if (i == idx) state[i].copyWith(status: status) else state[i],
    ];
  }

  void addRemoteUserMessage(String text,
      {String? messageId, String kind = ''}) {
    state = [
      ...state,
      ChatMessage(
        id: messageId ?? 'remote-user-${_msgCounter++}',
        isUser: true,
        kind: kind,
        text: text,
        time: DateTime.now(),
      ),
    ];
  }

  void addRemoteSystemMessage(String text,
      {String? messageId, String kind = ''}) {
    _finalizePendingReasoning(sourceId: null, collapse: true);
    state = [
      ...state,
      ChatMessage(
        id: messageId ?? 'remote-system-${_msgCounter++}',
        kind: kind,
        text: text,
        time: DateTime.now(),
      ),
    ];
  }

  bool bindRemoteUserMessage(String text, {required String remoteMessageId}) {
    final idx = state.lastIndexWhere(
      (m) =>
          m.isUser &&
          m.sourceId == null &&
          m.toolName == null &&
          m.text == text &&
          m.id.startsWith('user-'),
    );
    if (idx < 0) {
      return false;
    }
    final msg = state[idx];
    state = [
      for (int i = 0; i < state.length; i++)
        if (i == idx) msg.copyWith(id: remoteMessageId) else state[i],
    ];
    return true;
  }

  void clearMessages() {
    state = [];
  }

  void handleTextChunk(proto.TextData data) {
    _finalizePendingReasoning(sourceId: null, collapse: true);
    final idx = state.indexWhere((m) => m.id == data.id);
    if (idx >= 0) {
      final msg = state[idx];
      state = [
        for (int i = 0; i < state.length; i++)
          if (i == idx)
            msg.copyWith(
              text: msg.text + data.chunk,
              kind: data.kind.isNotEmpty ? data.kind : null,
              streaming: !data.done,
            )
          else
            state[i],
      ];
    } else {
      state = [
        ...state,
        ChatMessage(
          id: data.id,
          kind: data.kind,
          text: data.chunk,
          streaming: !data.done,
          time: DateTime.now(),
        ),
      ];
    }
  }

  void handleSubagentText(proto.SubagentTextData data) {
    _finalizePendingReasoning(sourceId: data.agentId, collapse: true);
    final msgId = '${data.agentId}-${data.id}';
    final idx = state.indexWhere((m) => m.id == msgId);
    final agent = _findSubagent(data.agentId);

    if (idx >= 0) {
      final msg = state[idx];
      state = [
        for (int i = 0; i < state.length; i++)
          if (i == idx)
            msg.copyWith(text: msg.text + data.chunk, streaming: !data.done)
          else
            state[i],
      ];
    } else {
      state = [
        ...state,
        ChatMessage(
          id: msgId,
          sourceId: data.agentId,
          sourceName: agent?.name ?? data.agentId,
          sourceColor: agent?.color ?? '#4CAF50',
          text: data.chunk,
          streaming: !data.done,
          time: DateTime.now(),
        ),
      ];
    }
  }

  void addSubagentToolCall({
    String? messageId,
    required String agentId,
    required String toolId,
    required String toolName,
    required String displayName,
    required String args,
    required String detail,
    required String sourceName,
    required String sourceColor,
  }) {
    _finalizePendingReasoning(sourceId: agentId, collapse: true);
    final msgId = messageId ??
        '$agentId-tool-${toolId.isNotEmpty ? toolId : _msgCounter++}';
    state = [
      ...state,
      ChatMessage(
        id: msgId,
        sourceId: agentId,
        sourceName: sourceName,
        sourceColor: sourceColor,
        toolId: toolId,
        toolName: toolName,
        toolDisplayName: displayName,
        toolDetail: detail.isNotEmpty
            ? detail
            : (args.length > 100 ? '${args.substring(0, 100)}...' : args),
        time: DateTime.now(),
      ),
    ];
  }

  void updateSubagentToolResult({
    required String agentId,
    required String toolId,
    required String toolName,
    required String result,
    String summary = '',
    String payload = '',
    String payloadMode = '',
    required bool isError,
  }) {
    _finalizePendingReasoning(sourceId: agentId, collapse: true);
    // Match by toolId (exact), fallback to agentId+toolName
    int idx = -1;
    if (toolId.isNotEmpty) {
      idx = state.lastIndexWhere(
        (m) => m.toolId == toolId && m.toolResult == null,
      );
    }
    if (idx < 0) {
      idx = state.lastIndexWhere(
        (m) =>
            m.sourceId == agentId &&
            m.toolName == toolName &&
            m.toolResult == null,
      );
    }
    if (idx >= 0) {
      final msg = state[idx];
      state = [
        for (int i = 0; i < state.length; i++)
          if (i == idx)
            msg.copyWith(
              toolResult:
                  _resolvedToolSummary(toolName, result, summary, isError),
              toolPayload: _resolvedToolPayload(
                toolName,
                result,
                payload,
                payloadMode,
              ),
              toolPayloadMode: payloadMode,
              toolCompleted: true,
              isToolError: isError,
            )
          else
            state[i],
      ];
    }
  }

  void handleToolCall(proto.ToolCallData data, {String? messageId}) {
    _finalizePendingReasoning(sourceId: null, collapse: true);
    state = [
      ...state,
      ChatMessage(
        id: messageId ??
            'tool-${data.toolId.isNotEmpty ? data.toolId : _msgCounter++}',
        toolId: data.toolId,
        toolName: data.toolName,
        toolDisplayName: data.displayName,
        toolDetail: data.detail,
        text: data.displayName.isNotEmpty ? data.displayName : data.toolName,
        time: DateTime.now(),
      ),
    ];
  }

  void handleToolResult(proto.ToolResultData data) {
    _finalizePendingReasoning(sourceId: null, collapse: true);
    // Match by toolId (exact), fallback to toolName
    int idx = -1;
    if (data.toolId.isNotEmpty) {
      idx = state.lastIndexWhere(
          (m) => m.toolId == data.toolId && m.toolResult == null);
    }
    if (idx < 0) {
      idx = state.lastIndexWhere((m) =>
          m.toolName == data.toolName &&
          m.toolResult == null &&
          m.sourceId == null);
    }
    if (idx >= 0) {
      final msg = state[idx];
      state = [
        for (int i = 0; i < state.length; i++)
          if (i == idx)
            msg.copyWith(
              toolResult: _resolvedToolSummary(
                data.toolName,
                data.result,
                data.summary,
                data.isError,
              ),
              toolPayload: _resolvedToolPayload(
                data.toolName,
                data.result,
                data.payload,
                data.payloadMode,
              ),
              toolPayloadMode: data.payloadMode,
              toolCompleted: true,
              isToolError: data.isError,
            )
          else
            state[i],
      ];
    }
  }

  String _formatToolResultForDisplay(
    String toolName,
    String result,
    bool isError,
  ) {
    switch (toolName) {
      case 'team_create':
        return _formatTeamCreateResult(result);
      case 'teammate_spawn':
        return _formatTeammateSpawnResult(result);
      case 'swarm_task_create':
        return _formatSwarmTaskCreateResult(result);
      case 'start_command':
        return _formatStartCommandResult(result, isError);
      default:
        return result;
    }
  }

  String _resolvedToolSummary(
    String toolName,
    String result,
    String summary,
    bool isError,
  ) {
    if (summary.isNotEmpty) {
      return summary;
    }
    return _formatToolResultForDisplay(toolName, result, isError);
  }

  String? _resolvedToolPayload(
    String toolName,
    String result,
    String payload,
    String payloadMode,
  ) {
    if (payload.isNotEmpty) {
      return payload;
    }
    if (payloadMode.isNotEmpty) {
      return '';
    }
    return null;
  }

  String _formatStartCommandResult(String result, bool isError) {
    if (isError) {
      return 'Failed';
    }
    final trimmed = result.trim();
    if (trimmed.isEmpty) {
      return 'Started';
    }
    for (final rawLine in trimmed.split('\n')) {
      final line = rawLine.trim();
      if (!line.startsWith('Status:')) {
        continue;
      }
      final status = line.substring('Status:'.length).trim().toLowerCase();
      switch (status) {
        case 'failed':
        case 'error':
        case 'cancelled':
        case 'timed_out':
        case 'timeout':
          return 'Failed';
        default:
          return 'Started';
      }
    }
    return 'Started';
  }

  String _formatTeamCreateResult(String result) {
    final trimmed = result.trim();
    if (trimmed.isEmpty) {
      return result;
    }
    try {
      final decoded = jsonDecode(trimmed);
      if (decoded is Map<String, dynamic>) {
        final rawName = decoded['Name'] ?? decoded['name'];
        if (rawName is String && rawName.trim().isNotEmpty) {
          return 'Team ${rawName.trim()} Created';
        }
      }
    } catch (_) {
      return result;
    }
    return result;
  }

  String _formatTeammateSpawnResult(String result) {
    final trimmed = result.trim();
    if (trimmed.isEmpty) {
      return result;
    }
    try {
      final decoded = jsonDecode(trimmed);
      if (decoded is Map<String, dynamic>) {
        final rawName = decoded['Name'] ?? decoded['name'];
        if (rawName is String && rawName.trim().isNotEmpty) {
          return 'Teammate ${rawName.trim()} Created';
        }
      }
    } catch (_) {
      return result;
    }
    return result;
  }

  String _formatSwarmTaskCreateResult(String result) {
    final trimmed = result.trim();
    if (trimmed.isEmpty) {
      return result;
    }
    try {
      final decoded = jsonDecode(trimmed);
      if (decoded is Map<String, dynamic>) {
        final rawDescription = decoded['Description'] ?? decoded['description'];
        if (rawDescription is String && rawDescription.trim().isNotEmpty) {
          return rawDescription.trim();
        }
      }
    } catch (_) {
      return result;
    }
    return result;
  }

  SubagentInfo? _findSubagent(String id) {
    return ref.read(subagentProvider)[id];
  }

  void finalizeStreaming(String msgId) {
    final idx = state.indexWhere((m) => m.id == msgId);
    if (idx < 0) return;
    final msg = state[idx];
    state = [
      for (int i = 0; i < state.length; i++)
        if (i == idx) msg.copyWith(streaming: false) else state[i],
    ];
  }

  void handleReasoningChunk(
    proto.TextData data, {
    String? sourceId,
    String? sourceName,
    String? sourceColor,
  }) {
    final chunk = _normalizeReasoningChunk(data.chunk);
    if (chunk.isEmpty) return;
    final idx = state.indexWhere(
      (m) => m.id == data.id && m.kind == _reasoningKind,
    );
    if (idx >= 0) {
      final msg = state[idx];
      state = [
        for (int i = 0; i < state.length; i++)
          if (i == idx)
            msg.copyWith(
              text: msg.text + chunk,
              streaming: !data.done,
            )
          else
            state[i],
      ];
      return;
    }
    state = [
      ...state,
      ChatMessage(
        id: data.id,
        sourceId: sourceId,
        sourceName: sourceName,
        sourceColor: sourceColor,
        kind: _reasoningKind,
        text: chunk,
        streaming: !data.done,
        reasoningCollapsed: false,
        time: DateTime.now(),
      ),
    ];
  }

  void handleSubagentReasoning(proto.SubagentReasoningData data) {
    final agent = _findSubagent(data.agentId);
    handleReasoningChunk(
      proto.TextData(
        id: '${data.agentId}-${data.id}',
        chunk: data.chunk,
        done: data.done,
      ),
      sourceId: data.agentId,
      sourceName: agent?.name ?? data.agentId,
      sourceColor: agent?.color ?? '#4CAF50',
    );
  }

  void finalizeReasoning(String msgId) {
    final idx = state.lastIndexWhere(
      (m) => m.id == msgId && m.kind == _reasoningKind,
    );
    if (idx < 0) return;
    final msg = state[idx];
    state = [
      for (int i = 0; i < state.length; i++)
        if (i == idx) msg.copyWith(streaming: false) else state[i],
    ];
  }

  String _normalizeReasoningChunk(String chunk) {
    final trimmed = chunk.trim();
    if (trimmed.isEmpty) return '';
    if (trimmed == '__redacted_thinking__') {
      return _redactedReasoningPlaceholder;
    }
    return chunk;
  }

  void _finalizePendingReasoning({
    required String? sourceId,
    required bool collapse,
  }) {
    final idx = state.lastIndexWhere(
      (m) =>
          m.kind == _reasoningKind &&
          m.sourceId == sourceId &&
          (m.streaming || (!m.reasoningCollapsed && collapse)),
    );
    if (idx < 0) return;
    final msg = state[idx];
    state = [
      for (int i = 0; i < state.length; i++)
        if (i == idx)
          msg.copyWith(
            streaming: false,
            reasoningCollapsed: collapse ? true : msg.reasoningCollapsed,
          )
        else
          state[i],
    ];
  }

  void addErrorMessage(String message, {String? messageId}) {
    _finalizePendingReasoning(sourceId: null, collapse: true);
    state = [
      ...state,
      ChatMessage(
        id: messageId ?? 'error-${_msgCounter++}',
        text: message,
        time: DateTime.now(),
      ),
    ];
  }

  void addAskUserRequest(String msgId, String title, String detail) {
    state = [
      ...state,
      ChatMessage(
        id: msgId,
        toolId: msgId,
        toolName: 'ask_user',
        toolDetail: title.isNotEmpty ? title : detail,
        time: DateTime.now(),
      ),
    ];
  }

  void updateAskUserAnswer(String msgId, String answer) {
    final idx = state.indexWhere((m) => m.id == msgId);
    if (idx >= 0) {
      final msg = state[idx];
      state = [
        for (int i = 0; i < state.length; i++)
          if (i == idx)
            msg.copyWith(
              toolResult: answer,
              toolCompleted: true,
              isToolError: false,
            )
          else
            state[i],
      ];
    }
  }

  // Expose setter for direct state updates (used by subagent_complete)
  void set(List<ChatMessage> messages) {
    state = messages;
  }
}

// ---- Agent Status Provider ----

// Simple value notifier wrapper for Riverpod 3
class _ValueNotifier<T> extends Notifier<T> {
  final T Function() _initialValue;
  late T _value;

  _ValueNotifier(this._initialValue);

  @override
  T build() {
    _value = _initialValue();
    return _value;
  }

  void set(T value) {
    _value = value;
    state = value;
  }
}

final agentStatusProvider = NotifierProvider<_ValueNotifier<String>, String>(
  () => _ValueNotifier(() => 'idle'),
);
final agentStatusMessageProvider =
    NotifierProvider<_ValueNotifier<String>, String>(
  () => _ValueNotifier(() => ''),
);

// ---- Sub-agent Provider ----

class SubagentInfo {
  final String agentId;
  final String name;
  final String task;
  final String color;
  final String parentId;
  final String status;
  final String? summary;
  final bool completed;
  final bool success;

  SubagentInfo({
    required this.agentId,
    required this.name,
    required this.task,
    this.color = '#4CAF50',
    this.parentId = '',
    this.status = 'running',
    this.summary,
    this.completed = false,
    this.success = false,
  });

  SubagentInfo copyWith({
    String? status,
    String? summary,
    bool? completed,
    bool? success,
  }) =>
      SubagentInfo(
        agentId: agentId,
        name: name,
        task: task,
        color: color,
        parentId: parentId,
        status: status ?? this.status,
        summary: summary ?? this.summary,
        completed: completed ?? this.completed,
        success: success ?? this.success,
      );

  Map<String, dynamic> toJson() => {
        'agent_id': agentId,
        'name': name,
        'task': task,
        'color': color,
        'parent_id': parentId,
        'status': status,
        'summary': summary,
        'completed': completed,
        'success': success,
      };

  factory SubagentInfo.fromJson(Map<String, dynamic> json) => SubagentInfo(
        agentId: json['agent_id'] as String? ?? '',
        name: json['name'] as String? ?? '',
        task: json['task'] as String? ?? '',
        color: json['color'] as String? ?? '#4CAF50',
        parentId: json['parent_id'] as String? ?? '',
        status: json['status'] as String? ?? 'running',
        summary: json['summary'] as String?,
        completed: json['completed'] as bool? ?? false,
        success: json['success'] as bool? ?? false,
      );
}

class _SubagentNotifier extends Notifier<Map<String, SubagentInfo>> {
  @override
  Map<String, SubagentInfo> build() => {};

  void set(Map<String, SubagentInfo> agents) {
    state = agents;
  }

  void clear() {
    state = {};
  }
}

final subagentProvider =
    NotifierProvider<_SubagentNotifier, Map<String, SubagentInfo>>(
  _SubagentNotifier.new,
);

// ---- Approval Provider ----

class ApprovalInfo {
  final String id;
  final String toolName;
  final String input;

  ApprovalInfo({required this.id, required this.toolName, required this.input});
}

class _NullableValueNotifier<T> extends Notifier<T> {
  final T Function() _initialValue;
  late T _value;

  _NullableValueNotifier(this._initialValue);

  @override
  T build() {
    _value = _initialValue();
    return _value;
  }

  void set(T value) {
    _value = value;
    state = value;
  }
}

final approvalProvider =
    NotifierProvider<_NullableValueNotifier<ApprovalInfo?>, ApprovalInfo?>(
  () => _NullableValueNotifier(() => null),
);

// ---- Ask User Provider ----

class AskUserInfo {
  final String id;
  final String title;
  final List<proto.AskUserQuestion> questions;
  final String msgId; // chat message ID for updating the answer

  AskUserInfo(
      {required this.id,
      required this.title,
      required this.questions,
      required this.msgId});
}

final askUserProvider =
    NotifierProvider<_NullableValueNotifier<AskUserInfo?>, AskUserInfo?>(
  () => _NullableValueNotifier(() => null),
);

String _newAskUserMessageId() =>
    'askuser-${DateTime.now().millisecondsSinceEpoch}';

String _describeAskUserQuestion(proto.AskUserQuestion q) {
  final prefix = q.kind == 'single'
      ? '[Single]'
      : q.kind == 'multi'
          ? '[Multi]'
          : '[Text]';
  final choices = q.choices.isNotEmpty
      ? ' (${q.choices.map((c) => c.label).join(', ')})'
      : '';
  return '$prefix ${q.prompt}$choices';
}

String _summarizeAskUserResponse(
  List<proto.AskUserQuestion> questions,
  List<proto.AskUserAnswer> answers,
  String status,
) {
  if (status == 'cancelled') {
    return 'Cancelled';
  }
  final answerByQuestion = <String, proto.AskUserAnswer>{
    for (final answer in answers) answer.questionId: answer,
  };
  final parts = <String>[];
  for (final question in questions) {
    final answer = answerByQuestion[question.id];
    if (answer == null) continue;
    String text = '';
    if (answer.choiceIds.isNotEmpty) {
      final labels = question.choices
          .where((choice) => answer.choiceIds.contains(choice.id))
          .map((choice) => choice.label)
          .toList();
      text = labels.join(', ');
      if (answer.freeformText.trim().isNotEmpty) {
        text = text.isEmpty
            ? answer.freeformText.trim()
            : '$text — ${answer.freeformText.trim()}';
      }
    } else {
      text = answer.freeformText.trim();
    }
    if (text.isNotEmpty) {
      parts.add(text);
    }
  }
  return parts.isEmpty ? 'Answered' : parts.join(' / ');
}

// ---- Session Info Provider ----

final sessionInfoProvider = NotifierProvider<
    _NullableValueNotifier<proto.SessionInfoData?>, proto.SessionInfoData?>(
  () => _NullableValueNotifier(() => null),
);

// ---- Current mode provider ----

final currentModeProvider = NotifierProvider<_ValueNotifier<String>, String>(
  () => _ValueNotifier(() => 'supervised'),
);

class WorkspaceRecord {
  final String key;
  final String url;
  final String displayName;
  final String lastSessionId;
  final DateTime lastOpenedAt;

  const WorkspaceRecord({
    required this.key,
    required this.url,
    required this.displayName,
    required this.lastSessionId,
    required this.lastOpenedAt,
  });

  WorkspaceRecord copyWith({
    String? url,
    String? displayName,
    String? lastSessionId,
    DateTime? lastOpenedAt,
  }) =>
      WorkspaceRecord(
        key: key,
        url: url ?? this.url,
        displayName: displayName ?? this.displayName,
        lastSessionId: lastSessionId ?? this.lastSessionId,
        lastOpenedAt: lastOpenedAt ?? this.lastOpenedAt,
      );

  Map<String, dynamic> toJson() => {
        'key': key,
        'url': url,
        'display_name': displayName,
        'last_session_id': lastSessionId,
        'last_opened_at': lastOpenedAt.toIso8601String(),
      };

  factory WorkspaceRecord.fromJson(Map<String, dynamic> json) =>
      WorkspaceRecord(
        key: json['key'] as String? ?? '',
        url: json['url'] as String? ?? '',
        displayName: json['display_name'] as String? ?? '',
        lastSessionId: json['last_session_id'] as String? ?? '',
        lastOpenedAt:
            DateTime.tryParse(json['last_opened_at'] as String? ?? '') ??
                DateTime.fromMillisecondsSinceEpoch(0),
      );
}

class CachedSessionRecord {
  final String workspaceKey;
  final String sessionId;
  final String title;
  final String model;
  final String provider;
  final String mode;
  final String version;
  final String lastEventId;
  final DateTime lastUpdatedAt;

  const CachedSessionRecord({
    required this.workspaceKey,
    required this.sessionId,
    required this.title,
    required this.model,
    required this.provider,
    required this.mode,
    required this.version,
    required this.lastEventId,
    required this.lastUpdatedAt,
  });

  CachedSessionRecord copyWith({
    String? title,
    String? model,
    String? provider,
    String? mode,
    String? version,
    String? lastEventId,
    DateTime? lastUpdatedAt,
  }) =>
      CachedSessionRecord(
        workspaceKey: workspaceKey,
        sessionId: sessionId,
        title: title ?? this.title,
        model: model ?? this.model,
        provider: provider ?? this.provider,
        mode: mode ?? this.mode,
        version: version ?? this.version,
        lastEventId: lastEventId ?? this.lastEventId,
        lastUpdatedAt: lastUpdatedAt ?? this.lastUpdatedAt,
      );

  Map<String, dynamic> toJson() => {
        'workspace_key': workspaceKey,
        'session_id': sessionId,
        'title': title,
        'model': model,
        'provider': provider,
        'mode': mode,
        'version': version,
        'last_event_id': lastEventId,
        'last_updated_at': lastUpdatedAt.toIso8601String(),
      };

  factory CachedSessionRecord.fromJson(Map<String, dynamic> json) =>
      CachedSessionRecord(
        workspaceKey: json['workspace_key'] as String? ?? '',
        sessionId: json['session_id'] as String? ?? '',
        title: json['title'] as String? ?? '',
        model: json['model'] as String? ?? '',
        provider: json['provider'] as String? ?? '',
        mode: json['mode'] as String? ?? '',
        version: json['version'] as String? ?? '',
        lastEventId: json['last_event_id'] as String? ?? '',
        lastUpdatedAt:
            DateTime.tryParse(json['last_updated_at'] as String? ?? '') ??
                DateTime.fromMillisecondsSinceEpoch(0),
      );
}

class CachedSessionSnapshot {
  final List<ChatMessage> messages;
  final Map<String, SubagentInfo> subagents;
  final proto.SessionInfoData? sessionInfo;
  final String agentStatus;
  final String agentStatusMessage;
  final String lastEventId;

  const CachedSessionSnapshot({
    required this.messages,
    required this.subagents,
    required this.sessionInfo,
    this.agentStatus = 'idle',
    this.agentStatusMessage = '',
    this.lastEventId = '',
  });

  Map<String, dynamic> toJson() => {
        'messages': messages.map((m) => m.toJson()).toList(),
        'subagents': subagents.map((k, v) => MapEntry(k, v.toJson())),
        'session_info': _sessionInfoToJson(sessionInfo),
        'agent_status': _normalizedCachedAgentStatus(agentStatus),
        'agent_status_message': agentStatusMessage,
        'last_event_id': lastEventId,
      };

  factory CachedSessionSnapshot.fromJson(Map<String, dynamic> json) {
    final rawSubagents = json['subagents'] as Map<String, dynamic>? ?? {};
    return CachedSessionSnapshot(
      messages: (json['messages'] as List<dynamic>? ?? const [])
          .map((m) => ChatMessage.fromJson(Map<String, dynamic>.from(m)))
          .toList(),
      subagents: rawSubagents.map((key, value) => MapEntry(
            key,
            SubagentInfo.fromJson(Map<String, dynamic>.from(value)),
          )),
      sessionInfo: _sessionInfoFromJson(
        json['session_info'] is Map<String, dynamic>
            ? Map<String, dynamic>.from(json['session_info'])
            : null,
      ),
      agentStatus: _normalizedCachedAgentStatus(
        json['agent_status'] as String? ?? 'idle',
      ),
      agentStatusMessage: json['agent_status_message'] as String? ?? '',
      lastEventId: json['last_event_id'] as String? ?? '',
    );
  }
}

class WorkspaceCacheState {
  final bool initialized;
  final Map<String, WorkspaceRecord> workspaces;
  final Map<String, CachedSessionRecord> sessions;
  final Map<String, CachedSessionSnapshot> snapshots;
  final String? selectedWorkspaceKey;
  final String? selectedSessionId;
  final String? liveWorkspaceKey;
  final String? liveSessionId;

  const WorkspaceCacheState({
    required this.initialized,
    required this.workspaces,
    required this.sessions,
    required this.snapshots,
    this.selectedWorkspaceKey,
    this.selectedSessionId,
    this.liveWorkspaceKey,
    this.liveSessionId,
  });

  WorkspaceCacheState copyWith({
    bool? initialized,
    Map<String, WorkspaceRecord>? workspaces,
    Map<String, CachedSessionRecord>? sessions,
    Map<String, CachedSessionSnapshot>? snapshots,
    Object? selectedWorkspaceKey = _workspaceCacheSentinel,
    Object? selectedSessionId = _workspaceCacheSentinel,
    Object? liveWorkspaceKey = _workspaceCacheSentinel,
    Object? liveSessionId = _workspaceCacheSentinel,
  }) =>
      WorkspaceCacheState(
        initialized: initialized ?? this.initialized,
        workspaces: workspaces ?? this.workspaces,
        sessions: sessions ?? this.sessions,
        snapshots: snapshots ?? this.snapshots,
        selectedWorkspaceKey:
            identical(selectedWorkspaceKey, _workspaceCacheSentinel)
                ? this.selectedWorkspaceKey
                : selectedWorkspaceKey as String?,
        selectedSessionId: identical(selectedSessionId, _workspaceCacheSentinel)
            ? this.selectedSessionId
            : selectedSessionId as String?,
        liveWorkspaceKey: identical(liveWorkspaceKey, _workspaceCacheSentinel)
            ? this.liveWorkspaceKey
            : liveWorkspaceKey as String?,
        liveSessionId: identical(liveSessionId, _workspaceCacheSentinel)
            ? this.liveSessionId
            : liveSessionId as String?,
      );
}

const _workspaceCacheSentinel = Object();
const _workspaceCacheIndexKey = 'ggcode_workspace_cache_v1';
const _workspaceCacheIndexSelectedWorkspaceUrlKey = 'selected_workspace_url';
const _workspaceSnapshotPrefix = 'ggcode_workspace_snapshot_v1_';
const _workspaceCacheStorageSchemaVersion = 1;
const _workspaceCacheProjectionVersion = 2;
String? debugWorkspaceCacheDatabasePathOverride;

class _SnapshotWrite {
  const _SnapshotWrite({
    required this.workspaceKey,
    required this.sessionId,
    required this.snapshot,
  });

  final String workspaceKey;
  final String sessionId;
  final CachedSessionSnapshot snapshot;
}

class _WorkspaceCacheSqlStore {
  _WorkspaceCacheSqlStore._(this._db);

  final Database _db;

  static Future<_WorkspaceCacheSqlStore> open() async {
    final path = await _resolveDatabasePath();
    final parent = Directory(p.dirname(path));
    if (!parent.existsSync()) {
      parent.createSync(recursive: true);
    }
    final db = sqlite3.open(path);
    final store = _WorkspaceCacheSqlStore._(db);
    store._initialize();
    return store;
  }

  static Future<String> _resolveDatabasePath() async {
    if (debugWorkspaceCacheDatabasePathOverride != null &&
        debugWorkspaceCacheDatabasePathOverride!.isNotEmpty) {
      return debugWorkspaceCacheDatabasePathOverride!;
    }
    try {
      final dir = await getApplicationSupportDirectory();
      return p.join(dir.path, 'workspace_cache_v2.sqlite');
    } catch (_) {
      return p.join(
        Directory.systemTemp.path,
        'ggcode_mobile_workspace_cache_v2.sqlite',
      );
    }
  }

  void _initialize() {
    _db.execute('''
      CREATE TABLE IF NOT EXISTS cache_meta (
        key TEXT PRIMARY KEY,
        value TEXT NOT NULL
      );

      CREATE TABLE IF NOT EXISTS cache_workspaces (
        key TEXT PRIMARY KEY,
        url TEXT NOT NULL,
        display_name TEXT NOT NULL,
        last_session_id TEXT NOT NULL,
        last_opened_at TEXT NOT NULL
      );

      CREATE TABLE IF NOT EXISTS cache_sessions (
        workspace_key TEXT NOT NULL,
        session_id TEXT NOT NULL,
        title TEXT NOT NULL,
        model TEXT NOT NULL,
        provider TEXT NOT NULL,
        mode TEXT NOT NULL,
        version TEXT NOT NULL,
        last_event_id TEXT NOT NULL,
        last_updated_at TEXT NOT NULL,
        PRIMARY KEY (workspace_key, session_id)
      );

      CREATE TABLE IF NOT EXISTS cache_snapshots (
        workspace_key TEXT NOT NULL,
        session_id TEXT NOT NULL,
        payload_json TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        PRIMARY KEY (workspace_key, session_id)
      );
    ''');
    _ensureVersion(
      key: 'storage_schema_version',
      expected: '$_workspaceCacheStorageSchemaVersion',
      resetStore: false,
    );
    _ensureVersion(
      key: 'projection_version',
      expected: '$_workspaceCacheProjectionVersion',
      resetStore: true,
    );
  }

  void _ensureVersion({
    required String key,
    required String expected,
    required bool resetStore,
  }) {
    final current = _metaValue(key);
    if (current == expected) {
      return;
    }
    if (resetStore) {
      clearAll();
    }
    _db.execute(
      'INSERT INTO cache_meta(key, value) VALUES(?, ?) '
      'ON CONFLICT(key) DO UPDATE SET value = excluded.value',
      [key, expected],
    );
  }

  String? _metaValue(String key) {
    final result = _db.select(
      'SELECT value FROM cache_meta WHERE key = ?',
      [key],
    );
    if (result.isEmpty) {
      return null;
    }
    return result.first['value'] as String?;
  }

  bool get isEmpty {
    final rows = _db.select('SELECT COUNT(*) AS count FROM cache_sessions');
    final count = rows.first['count'];
    if (count is int) {
      return count == 0;
    }
    if (count is BigInt) {
      return count == BigInt.zero;
    }
    return true;
  }

  void clearAll() {
    _db.execute('DELETE FROM cache_snapshots');
    _db.execute('DELETE FROM cache_sessions');
    _db.execute('DELETE FROM cache_workspaces');
  }

  List<WorkspaceRecord> loadWorkspaces() {
    final rows = _db.select('''
      SELECT key, url, display_name, last_session_id, last_opened_at
      FROM cache_workspaces
      ORDER BY last_opened_at DESC
    ''');
    return rows
        .map(
          (row) => WorkspaceRecord(
            key: row['key'] as String? ?? '',
            url: row['url'] as String? ?? '',
            displayName: row['display_name'] as String? ?? '',
            lastSessionId: row['last_session_id'] as String? ?? '',
            lastOpenedAt: DateTime.tryParse(
                  row['last_opened_at'] as String? ?? '',
                ) ??
                DateTime.fromMillisecondsSinceEpoch(0),
          ),
        )
        .toList();
  }

  List<CachedSessionRecord> loadSessions() {
    final rows = _db.select('''
      SELECT workspace_key, session_id, title, model, provider, mode, version,
             last_event_id, last_updated_at
      FROM cache_sessions
      ORDER BY last_updated_at DESC
    ''');
    return rows
        .map(
          (row) => CachedSessionRecord(
            workspaceKey: row['workspace_key'] as String? ?? '',
            sessionId: row['session_id'] as String? ?? '',
            title: row['title'] as String? ?? '',
            model: row['model'] as String? ?? '',
            provider: row['provider'] as String? ?? '',
            mode: row['mode'] as String? ?? '',
            version: row['version'] as String? ?? '',
            lastEventId: row['last_event_id'] as String? ?? '',
            lastUpdatedAt: DateTime.tryParse(
                  row['last_updated_at'] as String? ?? '',
                ) ??
                DateTime.fromMillisecondsSinceEpoch(0),
          ),
        )
        .toList();
  }

  CachedSessionSnapshot? loadSnapshot(String workspaceKey, String sessionId) {
    final rows = _db.select(
      '''
      SELECT payload_json
      FROM cache_snapshots
      WHERE workspace_key = ? AND session_id = ?
      ''',
      [workspaceKey, sessionId],
    );
    if (rows.isEmpty) {
      return null;
    }
    final payload = rows.first['payload_json'] as String? ?? '';
    if (payload.isEmpty) {
      return null;
    }
    final json = _decodeJsonObjectOrEmpty(
      payload,
      'decode cached snapshot $workspaceKey/$sessionId',
    );
    if (json.isEmpty) {
      return null;
    }
    return CachedSessionSnapshot.fromJson(json);
  }

  void upsertWorkspace(WorkspaceRecord record) {
    _db.execute(
      '''
      INSERT INTO cache_workspaces(key, url, display_name, last_session_id, last_opened_at)
      VALUES(?, ?, ?, ?, ?)
      ON CONFLICT(key) DO UPDATE SET
        url = excluded.url,
        display_name = excluded.display_name,
        last_session_id = excluded.last_session_id,
        last_opened_at = excluded.last_opened_at
      ''',
      [
        record.key,
        record.url,
        record.displayName,
        record.lastSessionId,
        record.lastOpenedAt.toIso8601String(),
      ],
    );
  }

  void upsertSession(CachedSessionRecord record) {
    _db.execute(
      '''
      INSERT INTO cache_sessions(
        workspace_key, session_id, title, model, provider, mode, version,
        last_event_id, last_updated_at
      )
      VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
      ON CONFLICT(workspace_key, session_id) DO UPDATE SET
        title = excluded.title,
        model = excluded.model,
        provider = excluded.provider,
        mode = excluded.mode,
        version = excluded.version,
        last_event_id = excluded.last_event_id,
        last_updated_at = excluded.last_updated_at
      ''',
      [
        record.workspaceKey,
        record.sessionId,
        record.title,
        record.model,
        record.provider,
        record.mode,
        record.version,
        record.lastEventId,
        record.lastUpdatedAt.toIso8601String(),
      ],
    );
  }

  void upsertSnapshot(
    String workspaceKey,
    String sessionId,
    CachedSessionSnapshot snapshot,
  ) {
    _db.execute(
      '''
      INSERT INTO cache_snapshots(workspace_key, session_id, payload_json, updated_at)
      VALUES(?, ?, ?, ?)
      ON CONFLICT(workspace_key, session_id) DO UPDATE SET
        payload_json = excluded.payload_json,
        updated_at = excluded.updated_at
      ''',
      [
        workspaceKey,
        sessionId,
        jsonEncode(snapshot.toJson()),
        DateTime.now().toIso8601String(),
      ],
    );
  }

  void writeBatch({
    required List<WorkspaceRecord> workspaces,
    required List<CachedSessionRecord> sessions,
    required List<_SnapshotWrite> snapshots,
  }) {
    if (workspaces.isEmpty && sessions.isEmpty && snapshots.isEmpty) {
      return;
    }
    _db.execute('BEGIN IMMEDIATE');
    try {
      for (final workspace in workspaces) {
        upsertWorkspace(workspace);
      }
      for (final session in sessions) {
        upsertSession(session);
      }
      for (final snapshot in snapshots) {
        upsertSnapshot(
          snapshot.workspaceKey,
          snapshot.sessionId,
          snapshot.snapshot,
        );
      }
      _db.execute('COMMIT');
    } catch (_) {
      _db.execute('ROLLBACK');
      rethrow;
    }
  }

  Future<void> importLegacyPreferences(SharedPreferences prefs) async {
    if (!isEmpty) {
      return;
    }
    final raw = prefs.getString(_workspaceCacheIndexKey);
    final json = _decodeJsonObjectOrEmpty(raw, 'decode legacy workspace cache');
    if (json.isEmpty) {
      return;
    }
    for (final item in json['workspaces'] as List<dynamic>? ?? const []) {
      final record = WorkspaceRecord.fromJson(Map<String, dynamic>.from(item));
      if (record.key.isEmpty) {
        continue;
      }
      upsertWorkspace(record);
    }
    for (final item in json['sessions'] as List<dynamic>? ?? const []) {
      final record =
          CachedSessionRecord.fromJson(Map<String, dynamic>.from(item));
      if (record.workspaceKey.isEmpty || record.sessionId.isEmpty) {
        continue;
      }
      upsertSession(record);
      final sessionKey =
          _sessionCacheKey(record.workspaceKey, record.sessionId);
      final snapshotRaw = prefs.getString(_snapshotStorageKey(sessionKey));
      if (snapshotRaw == null || snapshotRaw.isEmpty) {
        continue;
      }
      final snapshotJson = _decodeJsonObjectOrEmpty(
        snapshotRaw,
        'decode legacy workspace snapshot $sessionKey',
      );
      if (snapshotJson.isEmpty) {
        continue;
      }
      upsertSnapshot(
        record.workspaceKey,
        record.sessionId,
        CachedSessionSnapshot.fromJson(snapshotJson),
      );
    }
  }

  void dispose() {
    _db.dispose();
  }
}

final workspaceCacheProvider =
    NotifierProvider<WorkspaceCacheNotifier, WorkspaceCacheState>(
  WorkspaceCacheNotifier.new,
);

class WorkspaceCacheNotifier extends Notifier<WorkspaceCacheState> {
  SharedPreferences? _prefs;
  _WorkspaceCacheSqlStore? _store;
  Future<void>? _initializeFuture;
  Timer? _flushTimer;
  final Set<String> _dirtySnapshots = <String>{};
  final Set<String> _dirtySessions = <String>{};
  final Set<String> _dirtyWorkspaces = <String>{};
  bool _selectionDirty = false;

  @override
  WorkspaceCacheState build() {
    ref.onDispose(() {
      _flushTimer?.cancel();
      _flushTimer = null;
      _store?.dispose();
      _store = null;
    });
    return const WorkspaceCacheState(
      initialized: false,
      workspaces: {},
      sessions: {},
      snapshots: {},
    );
  }

  Future<void> initialize() async {
    if (state.initialized) return;
    final inFlight = _initializeFuture;
    if (inFlight != null) {
      await inFlight;
      return;
    }
    final future = _initializeImpl();
    _initializeFuture = future;
    try {
      await future;
    } finally {
      if (identical(_initializeFuture, future)) {
        _initializeFuture = null;
      }
    }
  }

  Future<void> _initializeImpl() async {
    _prefs ??= await SharedPreferences.getInstance();
    if (!ref.mounted) return;
    final index = _decodeJsonObjectOrEmpty(
      _prefs!.getString(_workspaceCacheIndexKey),
      'decode workspace cache index',
    );
    final workspaces = <String, WorkspaceRecord>{};
    final sessions = <String, CachedSessionRecord>{};
    try {
      _store ??= await _WorkspaceCacheSqlStore.open();
      if (!ref.mounted) return;
      await _store!.importLegacyPreferences(_prefs!);
      if (!ref.mounted) return;
      for (final record in _store!.loadWorkspaces()) {
        if (record.key.isNotEmpty) {
          workspaces[record.key] = record;
        }
      }
      for (final record in _store!.loadSessions()) {
        if (record.workspaceKey.isNotEmpty && record.sessionId.isNotEmpty) {
          sessions[_sessionCacheKey(record.workspaceKey, record.sessionId)] =
              record;
        }
      }
    } catch (error, stackTrace) {
      _reportWorkspaceCacheError(
        'initialize workspace cache store',
        error,
        stackTrace,
      );
      _store?.dispose();
      _store = null;
    }
    final selectedWorkspaceKey = index['selected_workspace_key'] as String?;
    final selectedSessionId = index['selected_session_id'] as String?;
    final selectedWorkspaceUrl = normalizeTunnelUrl(
      index[_workspaceCacheIndexSelectedWorkspaceUrlKey] as String? ?? '',
    );
    if (selectedWorkspaceKey != null &&
        selectedWorkspaceKey.isNotEmpty &&
        selectedWorkspaceUrl.isNotEmpty) {
      final existing = workspaces[selectedWorkspaceKey];
      workspaces[selectedWorkspaceKey] = (existing ??
              WorkspaceRecord(
                key: selectedWorkspaceKey,
                url: selectedWorkspaceUrl,
                displayName: _workspaceDisplayName(selectedWorkspaceUrl, null),
                lastSessionId: selectedSessionId ?? '',
                lastOpenedAt: DateTime.fromMillisecondsSinceEpoch(0),
              ))
          .copyWith(
        url: selectedWorkspaceUrl,
        displayName: existing?.displayName.isNotEmpty == true
            ? existing!.displayName
            : _workspaceDisplayName(selectedWorkspaceUrl, null),
        lastSessionId: selectedSessionId ?? existing?.lastSessionId ?? '',
      );
    }
    if (!ref.mounted) return;
    state = WorkspaceCacheState(
      initialized: true,
      workspaces: workspaces,
      sessions: sessions,
      snapshots: {},
      selectedWorkspaceKey: selectedWorkspaceKey,
      selectedSessionId: selectedSessionId,
    );
  }

  String? urlForWorkspace(String workspaceKey) =>
      state.workspaces[workspaceKey]?.url;

  Future<void> activateWorkspaceUrl(String url) async {
    await initialize();
    if (!ref.mounted) return;
    final normalized = normalizeTunnelUrl(url);
    final key = _workspaceKeyForUrl(normalized);
    final now = DateTime.now();
    final current = state.workspaces[key];
    final sessions = sessionsForWorkspace(key);
    final selectedSessionId = current?.lastSessionId.isNotEmpty == true
        ? current!.lastSessionId
        : (sessions.isNotEmpty ? sessions.first.sessionId : null);
    final workspaces = Map<String, WorkspaceRecord>.from(state.workspaces)
      ..[key] = (current ??
              WorkspaceRecord(
                key: key,
                url: normalized,
                displayName: _workspaceDisplayName(normalized, null),
                lastSessionId: selectedSessionId ?? '',
                lastOpenedAt: now,
              ))
          .copyWith(url: normalized, lastOpenedAt: now);
    state = state.copyWith(
      workspaces: workspaces,
      selectedWorkspaceKey: key,
      selectedSessionId: selectedSessionId,
      liveWorkspaceKey: key,
      liveSessionId: state.liveWorkspaceKey == key ? state.liveSessionId : null,
    );
    _dirtyWorkspaces.add(key);
    _selectionDirty = true;
    _scheduleFlush();
  }

  Future<void> clearSelection() async {
    await initialize();
    if (!ref.mounted) return;
    state = state.copyWith(
      selectedWorkspaceKey: null,
      selectedSessionId: null,
      liveWorkspaceKey: null,
      liveSessionId: null,
    );
    _selectionDirty = true;
    _scheduleFlush();
  }

  void markDisconnected() {
    state = state.copyWith(liveWorkspaceKey: null, liveSessionId: null);
  }

  Future<bool> attachSessionToActiveWorkspace(String sessionId) async {
    if (sessionId.isEmpty) return false;
    await initialize();
    if (!ref.mounted) return false;
    final workspaceKey = state.liveWorkspaceKey ?? state.selectedWorkspaceKey;
    if (workspaceKey == null || workspaceKey.isEmpty) return false;

    final targetKey = _sessionCacheKey(workspaceKey, sessionId);
    await _ensureSnapshotLoaded(workspaceKey, sessionId);
    if (!ref.mounted) return false;
    final targetRecord = state.sessions[targetKey];
    final hasTargetSnapshot = state.snapshots.containsKey(targetKey);
    if (targetRecord == null) {
      return false;
    }
    if (state.selectedWorkspaceKey == workspaceKey &&
        (state.selectedSessionId == null || state.selectedSessionId!.isEmpty)) {
      state = state.copyWith(selectedSessionId: sessionId);
      await _persistIndex();
    }
    return hasTargetSnapshot;
  }

  Future<void> selectSession(String workspaceKey, String sessionId) async {
    await initialize();
    if (!ref.mounted) return;
    state = state.copyWith(
      selectedWorkspaceKey: workspaceKey,
      selectedSessionId: sessionId,
    );
    await _ensureSnapshotLoaded(workspaceKey, sessionId);
    if (!ref.mounted) return;
    _selectionDirty = true;
    _scheduleFlush();
  }

  Future<void> registerLiveSession(
    String sessionId,
    proto.SessionInfoData? sessionInfo, {
    String? lastEventId,
  }) async {
    if (sessionId.isEmpty) return;
    await initialize();
    if (!ref.mounted) return;
    final workspaceKey = state.liveWorkspaceKey ?? state.selectedWorkspaceKey;
    if (workspaceKey == null || workspaceKey.isEmpty) return;
    final now = DateTime.now();
    final previousLiveSessionId = state.liveSessionId;
    final lastKnownLiveSessionId =
        state.workspaces[workspaceKey]?.lastSessionId;
    final selectionFollowedLive = state.selectedWorkspaceKey == workspaceKey &&
        (state.selectedSessionId == null ||
            state.selectedSessionId!.isEmpty ||
            state.selectedSessionId == previousLiveSessionId ||
            (previousLiveSessionId == null &&
                state.selectedSessionId == lastKnownLiveSessionId));
    final sessionKey = _sessionCacheKey(workspaceKey, sessionId);
    final sessions = Map<String, CachedSessionRecord>.from(state.sessions)
      ..[sessionKey] = (state.sessions[sessionKey] ??
              CachedSessionRecord(
                workspaceKey: workspaceKey,
                sessionId: sessionId,
                title: _sessionTitle(sessionInfo, sessionId),
                model: sessionInfo?.model ?? '',
                provider: sessionInfo?.provider ?? '',
                mode: sessionInfo?.mode ?? '',
                version: sessionInfo?.version ?? '',
                lastEventId: lastEventId ?? '',
                lastUpdatedAt: now,
              ))
          .copyWith(
        title: _sessionTitle(sessionInfo, sessionId),
        model: sessionInfo?.model ?? state.sessions[sessionKey]?.model,
        provider: sessionInfo?.provider ?? state.sessions[sessionKey]?.provider,
        mode: sessionInfo?.mode ?? state.sessions[sessionKey]?.mode,
        version: sessionInfo?.version ?? state.sessions[sessionKey]?.version,
        lastEventId: lastEventId ?? state.sessions[sessionKey]?.lastEventId,
        lastUpdatedAt: now,
      );
    final workspace = state.workspaces[workspaceKey];
    final workspaces = Map<String, WorkspaceRecord>.from(state.workspaces);
    if (workspace != null) {
      workspaces[workspaceKey] = workspace.copyWith(
        displayName: _workspaceDisplayName(workspace.url, sessionInfo),
        lastSessionId: sessionId,
        lastOpenedAt: now,
      );
    }
    state = state.copyWith(
      workspaces: workspaces,
      sessions: sessions,
      liveWorkspaceKey: workspaceKey,
      liveSessionId: sessionId,
      selectedWorkspaceKey:
          selectionFollowedLive ? workspaceKey : state.selectedWorkspaceKey,
      selectedSessionId:
          selectionFollowedLive ? sessionId : state.selectedSessionId,
    );
    _dirtyWorkspaces.add(workspaceKey);
    _dirtySessions.add(sessionKey);
    if (selectionFollowedLive) {
      _selectionDirty = true;
    }
    _scheduleFlush();
    if (selectionFollowedLive) {
      await _ensureSnapshotLoaded(workspaceKey, sessionId);
    }
  }

  Future<void> observeLiveSession(
    String sessionId, {
    required String previousSessionId,
    proto.SessionInfoData? sessionInfo,
  }) async {
    await registerLiveSession(
      sessionId,
      sessionInfo,
      lastEventId: state
          .sessions[_sessionCacheKey(
              state.liveWorkspaceKey ?? state.selectedWorkspaceKey ?? '',
              sessionId)]
          ?.lastEventId,
    );
    if (previousSessionId.isEmpty || previousSessionId == sessionId) return;
  }

  Future<void> updateLiveCursor(String sessionId, String lastEventId) async {
    if (sessionId.isEmpty) return;
    await initialize();
    if (!ref.mounted) return;
    final workspaceKey = state.liveWorkspaceKey ?? state.selectedWorkspaceKey;
    if (workspaceKey == null || workspaceKey.isEmpty) return;
    final key = _sessionCacheKey(workspaceKey, sessionId);
    final record = state.sessions[key];
    if (record == null) return;
    final updated = Map<String, CachedSessionRecord>.from(state.sessions)
      ..[key] = record.copyWith(
        lastEventId: lastEventId,
        lastUpdatedAt: DateTime.now(),
      );
    state = state.copyWith(sessions: updated);
    _dirtySessions.add(key);
    _scheduleFlush();
  }

  Future<void> captureLiveProjection({
    required List<ChatMessage> messages,
    required Map<String, SubagentInfo> subagents,
    required proto.SessionInfoData? sessionInfo,
    required String agentStatus,
    required String agentStatusMessage,
    required String lastEventId,
  }) async {
    await initialize();
    if (!ref.mounted) return;
    final workspaceKey = state.liveWorkspaceKey;
    final sessionId = state.liveSessionId;
    if (workspaceKey == null ||
        workspaceKey.isEmpty ||
        sessionId == null ||
        sessionId.isEmpty) {
      return;
    }
    final snapshot = CachedSessionSnapshot(
      messages: List<ChatMessage>.from(messages),
      subagents: Map<String, SubagentInfo>.from(subagents),
      sessionInfo: sessionInfo,
      agentStatus: _normalizedCachedAgentStatus(agentStatus),
      agentStatusMessage: agentStatusMessage,
      lastEventId: lastEventId,
    );
    final snapshotKey = _sessionCacheKey(workspaceKey, sessionId);
    final snapshots = Map<String, CachedSessionSnapshot>.from(state.snapshots)
      ..[snapshotKey] = snapshot;
    final now = DateTime.now();
    final sessions = Map<String, CachedSessionRecord>.from(state.sessions)
      ..[snapshotKey] = (state.sessions[snapshotKey] ??
              CachedSessionRecord(
                workspaceKey: workspaceKey,
                sessionId: sessionId,
                title: _sessionTitle(sessionInfo, sessionId),
                model: sessionInfo?.model ?? '',
                provider: sessionInfo?.provider ?? '',
                mode: sessionInfo?.mode ?? '',
                version: sessionInfo?.version ?? '',
                lastEventId: lastEventId,
                lastUpdatedAt: now,
              ))
          .copyWith(
        title: _sessionTitle(sessionInfo, sessionId),
        model: sessionInfo?.model ?? state.sessions[snapshotKey]?.model,
        provider:
            sessionInfo?.provider ?? state.sessions[snapshotKey]?.provider,
        mode: sessionInfo?.mode ?? state.sessions[snapshotKey]?.mode,
        version: sessionInfo?.version ?? state.sessions[snapshotKey]?.version,
        lastEventId: lastEventId,
        lastUpdatedAt: now,
      );
    final workspaces = Map<String, WorkspaceRecord>.from(state.workspaces);
    final workspace = workspaces[workspaceKey];
    if (workspace != null) {
      workspaces[workspaceKey] = workspace.copyWith(
        displayName: _workspaceDisplayName(workspace.url, sessionInfo),
        lastSessionId: sessionId,
        lastOpenedAt: now,
      );
    }
    state = state.copyWith(
      workspaces: workspaces,
      sessions: sessions,
      snapshots: snapshots,
    );
    _dirtyWorkspaces.add(workspaceKey);
    _dirtySessions.add(snapshotKey);
    _dirtySnapshots.add(snapshotKey);
    _scheduleFlush();
  }

  Future<void> flushNow() async {
    _flushTimer?.cancel();
    _flushTimer = null;
    if (!ref.mounted) return;
    await _flushDirtyState();
    if (!ref.mounted) return;
    if (_selectionDirty) {
      await _persistIndex();
    }
  }

  List<WorkspaceRecord> sortedWorkspaces() {
    final items = state.workspaces.values.toList()
      ..sort((a, b) => b.lastOpenedAt.compareTo(a.lastOpenedAt));
    return items;
  }

  List<CachedSessionRecord> sessionsForWorkspace(String workspaceKey) {
    final items = state.sessions.values
        .where((record) => record.workspaceKey == workspaceKey)
        .toList()
      ..sort((a, b) => b.lastUpdatedAt.compareTo(a.lastUpdatedAt));
    return items;
  }

  CachedSessionSnapshot? snapshotFor(String workspaceKey, String sessionId) {
    final key = _sessionCacheKey(workspaceKey, sessionId);
    final cached = state.snapshots[key];
    if (cached != null) {
      return cached;
    }
    final snapshot = _store?.loadSnapshot(workspaceKey, sessionId);
    if (snapshot == null) {
      return null;
    }
    state = state.copyWith(
      snapshots: Map<String, CachedSessionSnapshot>.from(state.snapshots)
        ..[key] = snapshot,
    );
    return snapshot;
  }

  Future<void> _ensureSnapshotLoaded(
      String workspaceKey, String sessionId) async {
    await initialize();
    if (!ref.mounted) return;
    snapshotFor(workspaceKey, sessionId);
  }

  void _scheduleFlush() {
    _flushTimer?.cancel();
    _flushTimer = Timer(
      const Duration(milliseconds: 350),
      () => unawaited(flushNow()),
    );
  }

  Future<void> _flushDirtyState() async {
    if (_dirtySnapshots.isEmpty &&
        _dirtySessions.isEmpty &&
        _dirtyWorkspaces.isEmpty) {
      return;
    }
    await initialize();
    if (!ref.mounted) return;
    if (_store == null) {
      return;
    }
    final pendingWorkspaces = List<String>.from(_dirtyWorkspaces);
    final pendingWorkspaceRecords = <WorkspaceRecord>[];
    for (final key in pendingWorkspaces) {
      final workspace = state.workspaces[key];
      if (workspace == null) continue;
      pendingWorkspaceRecords.add(workspace);
    }
    final pendingSessions = List<String>.from(_dirtySessions);
    final pendingSessionRecords = <CachedSessionRecord>[];
    for (final key in pendingSessions) {
      final session = state.sessions[key];
      if (session == null) continue;
      pendingSessionRecords.add(session);
    }
    final pendingSnapshotKeys = List<String>.from(_dirtySnapshots);
    final pendingSnapshotWrites = <_SnapshotWrite>[];
    for (final key in pendingSnapshotKeys) {
      final snapshot = state.snapshots[key];
      if (snapshot == null) continue;
      final split = key.split('::');
      if (split.length != 2) continue;
      pendingSnapshotWrites.add(
        _SnapshotWrite(
          workspaceKey: split[0],
          sessionId: split[1],
          snapshot: snapshot,
        ),
      );
    }
    _dirtyWorkspaces.clear();
    _dirtySessions.clear();
    _dirtySnapshots.clear();
    try {
      _store!.writeBatch(
        workspaces: pendingWorkspaceRecords,
        sessions: pendingSessionRecords,
        snapshots: pendingSnapshotWrites,
      );
    } catch (error, stackTrace) {
      _dirtyWorkspaces.addAll(pendingWorkspaces);
      _dirtySessions.addAll(pendingSessions);
      _dirtySnapshots.addAll(pendingSnapshotKeys);
      _reportWorkspaceCacheError(
        'flush workspace cache',
        error,
        stackTrace,
      );
    }
  }

  Future<void> _persistIndex() async {
    _prefs ??= await SharedPreferences.getInstance();
    if (!ref.mounted) return;
    final selectedWorkspace = state.selectedWorkspaceKey == null
        ? null
        : state.workspaces[state.selectedWorkspaceKey!];
    final payload = {
      'selected_workspace_key': state.selectedWorkspaceKey,
      'selected_session_id': state.selectedSessionId,
      _workspaceCacheIndexSelectedWorkspaceUrlKey: selectedWorkspace?.url,
    };
    try {
      await _prefs!.setString(_workspaceCacheIndexKey, jsonEncode(payload));
      _selectionDirty = false;
    } catch (error, stackTrace) {
      _reportWorkspaceCacheError(
        'persist workspace cache index',
        error,
        stackTrace,
      );
    }
  }
}

final displayedMessagesProvider = Provider<List<ChatMessage>>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  if (_isViewingLive(cache)) {
    return ref.watch(chatProvider);
  }
  final workspaceKey = cache.selectedWorkspaceKey;
  final sessionId = cache.selectedSessionId;
  if (workspaceKey == null ||
      workspaceKey.isEmpty ||
      sessionId == null ||
      sessionId.isEmpty) {
    return const [];
  }
  return ref
          .watch(workspaceCacheProvider.notifier)
          .snapshotFor(workspaceKey, sessionId)
          ?.messages ??
      const [];
});

final displayedSubagentProvider = Provider<Map<String, SubagentInfo>>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  if (_isViewingLive(cache)) {
    return ref.watch(subagentProvider);
  }
  final workspaceKey = cache.selectedWorkspaceKey;
  final sessionId = cache.selectedSessionId;
  if (workspaceKey == null ||
      workspaceKey.isEmpty ||
      sessionId == null ||
      sessionId.isEmpty) {
    return const {};
  }
  return ref
          .watch(workspaceCacheProvider.notifier)
          .snapshotFor(workspaceKey, sessionId)
          ?.subagents ??
      const {};
});

final displayedSessionInfoProvider = Provider<proto.SessionInfoData?>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  if (_isViewingLive(cache)) {
    return ref.watch(sessionInfoProvider);
  }
  final workspaceKey = cache.selectedWorkspaceKey;
  final sessionId = cache.selectedSessionId;
  if (workspaceKey == null ||
      workspaceKey.isEmpty ||
      sessionId == null ||
      sessionId.isEmpty) {
    return null;
  }
  return ref
      .watch(workspaceCacheProvider.notifier)
      .snapshotFor(workspaceKey, sessionId)
      ?.sessionInfo;
});

final displayedAgentStatusProvider = Provider<String>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  if (_isViewingLive(cache)) {
    return _normalizedCachedAgentStatus(ref.watch(agentStatusProvider));
  }
  final workspaceKey = cache.selectedWorkspaceKey;
  final sessionId = cache.selectedSessionId;
  if (workspaceKey == null ||
      workspaceKey.isEmpty ||
      sessionId == null ||
      sessionId.isEmpty) {
    return 'idle';
  }
  return _normalizedCachedAgentStatus(ref
          .watch(workspaceCacheProvider.notifier)
          .snapshotFor(workspaceKey, sessionId)
          ?.agentStatus ??
      'idle');
});

final displayedAgentStatusMessageProvider = Provider<String>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  if (_isViewingLive(cache)) {
    return ref.watch(agentStatusMessageProvider);
  }
  final workspaceKey = cache.selectedWorkspaceKey;
  final sessionId = cache.selectedSessionId;
  if (workspaceKey == null ||
      workspaceKey.isEmpty ||
      sessionId == null ||
      sessionId.isEmpty) {
    return '';
  }
  return ref
          .watch(workspaceCacheProvider.notifier)
          .snapshotFor(workspaceKey, sessionId)
          ?.agentStatusMessage ??
      '';
});

final isHistoricalViewProvider = Provider<bool>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  final selectedWorkspaceKey = cache.selectedWorkspaceKey;
  final selectedSessionId = cache.selectedSessionId;
  if (selectedWorkspaceKey == null ||
      selectedWorkspaceKey.isEmpty ||
      selectedSessionId == null ||
      selectedSessionId.isEmpty) {
    return false;
  }
  return !_isViewingLive(cache);
});

final canSendMessagesProvider = Provider<bool>((ref) {
  final conn = ref.watch(connectionProvider);
  return conn.status == ConnectionStatus.connected &&
      !ref.watch(isHistoricalViewProvider);
});

bool _isViewingLive(WorkspaceCacheState state) {
  final workspaceKey = state.selectedWorkspaceKey;
  final sessionId = state.selectedSessionId;
  if (workspaceKey == null ||
      workspaceKey.isEmpty ||
      sessionId == null ||
      sessionId.isEmpty) {
    return true;
  }
  return workspaceKey == state.liveWorkspaceKey &&
      sessionId == state.liveSessionId;
}

String _normalizedCachedAgentStatus(String status) {
  switch (status) {
    case 'busy':
    case 'running':
    case 'thinking':
    case 'waiting':
    case 'error':
      return 'busy';
    default:
      return 'idle';
  }
}

String _workspaceKeyForUrl(String url) =>
    base64UrlEncode(utf8.encode(url)).replaceAll('=', '');

String _sessionCacheKey(String workspaceKey, String sessionId) =>
    '$workspaceKey::$sessionId';

String _snapshotStorageKey(String sessionKey) =>
    '$_workspaceSnapshotPrefix${base64UrlEncode(utf8.encode(sessionKey)).replaceAll('=', '')}';

Map<String, dynamic> _decodeJsonObjectOrEmpty(String? raw, String context) {
  if (raw == null || raw.isEmpty) {
    return const <String, dynamic>{};
  }
  try {
    final decoded = jsonDecode(raw);
    if (decoded is Map<String, dynamic>) {
      return decoded;
    }
    if (decoded is Map) {
      return Map<String, dynamic>.from(decoded);
    }
  } catch (error, stackTrace) {
    _reportWorkspaceCacheError(context, error, stackTrace);
  }
  return const <String, dynamic>{};
}

void _reportWorkspaceCacheError(
  String context,
  Object error,
  StackTrace stackTrace,
) {
  debugPrint('[workspace_cache] $context: $error');
  FlutterError.reportError(
    FlutterErrorDetails(
      exception: error,
      stack: stackTrace,
      library: 'ggcode_mobile.workspace_cache',
      context: ErrorDescription(context),
    ),
  );
}

Map<String, dynamic>? _sessionInfoToJson(proto.SessionInfoData? info) {
  if (info == null) return null;
  return {
    'workspace': info.workspace,
    'model': info.model,
    'provider': info.provider,
    'mode': info.mode,
    'version': info.version,
    'language': info.language,
    'theme': info.theme,
  };
}

proto.SessionInfoData? _sessionInfoFromJson(Map<String, dynamic>? json) {
  if (json == null) return null;
  return proto.SessionInfoData.fromJson(json);
}

String _workspaceDisplayName(String url, proto.SessionInfoData? sessionInfo) {
  final workspace = sessionInfo?.workspace ?? '';
  if (workspace.isNotEmpty) {
    final parts = workspace.split('/');
    return parts.isNotEmpty ? parts.last : workspace;
  }
  final uri = Uri.tryParse(url);
  return uri?.host.isNotEmpty == true ? uri!.host : 'Workspace';
}

String _sessionTitle(proto.SessionInfoData? sessionInfo, String sessionId) {
  final workspace = sessionInfo?.workspace ?? '';
  final label = workspace.isNotEmpty ? workspace.split('/').last : 'Session';
  final shortId = sessionId.length > 8 ? sessionId.substring(0, 8) : sessionId;
  return '$label · $shortId';
}
