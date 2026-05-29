import 'dart:async';
import 'dart:developer';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:uuid/uuid.dart';

import '../connection_service.dart';
export '../connection_service.dart' show ConnectionStatus, normalizeTunnelUrl;
import '../l10n/app_localizations.dart';
import '../models/protocol.dart' as proto;
import '../theme/app_theme.dart';

import 'chat_provider.dart';
import 'ui_providers.dart';
import 'workspace_cache.dart';

final connectionProvider =
    NotifierProvider<ConnectionNotifier, TunnelConnectionState>(
  ConnectionNotifier.new,
);

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
  static const _resumeRoomIdKey = 'ggcode_tunnel_room_id';
  static const _resumeRenewTokenKey = 'ggcode_tunnel_renew_token';

  String _clientId = '';
  String _sessionId = '';
  String _lastAppliedEventId = '';
  String _shareRoomId = '';
  String _shareRenewToken = '';
  bool _hasAuthoritativeProjection = false;
  final List<String> _recentEventIds = <String>[];
  final Set<String> _recentEventSet = <String>{};
  Future<void>? _connectInFlight;
  String? _connectInFlightUrl;
  bool _awaitingSnapshotProjection = false;

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

  ConnectionService createConnectionService(
          ShareConnectionDescriptor descriptor) =>
      ConnectionService(descriptor: descriptor);

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

    var descriptor = ShareConnectionDescriptor.parse(url);
    state = state.copyWith(
        status: ConnectionStatus.connecting,
        url: descriptor.publicUrl,
        error: null);

    if (descriptor.cryptoMaterial.isEmpty) {
      state = state.copyWith(
          status: ConnectionStatus.disconnected,
          error: 'Invalid URL: missing crypto material');
      return;
    }

    // Snapshot current UI state before loading persisted values.
    final uiSessionId = _sessionId;
    final uiHasContent = ref.read(sessionInfoProvider) != null;

    await _loadResumeState();
    if (descriptor.isV2 &&
        _shareRoomId.isNotEmpty &&
        _shareRoomId == descriptor.roomId &&
        _shareRenewToken.isNotEmpty) {
      descriptor = descriptor.copyWith(renewToken: _shareRenewToken);
    }
    service = createConnectionService(descriptor);

    // _loadResumeState overwrites _sessionId/_lastAppliedEventId from prefs.
    final savedSessionId = _sessionId;

    if (uiHasContent && uiSessionId == savedSessionId) {
      // UI already renders this session — keep it. Ordinal dedup will skip
      // relay replay events we already have rendered.
    } else {
      // Different session or fresh connect — clear UI, restore from SQLite.
      _clearUiProjection();
      _sessionId = savedSessionId;
      _lastAppliedEventId =
          savedSessionId.isNotEmpty ? _lastAppliedEventId : '';
      _awaitingSnapshotProjection = false;
      _recentEventIds.clear();
      _recentEventSet.clear();
      _restoreProjectionFromCache(
        adoptCursor: false,
        seedCursorIfUnset: true,
      );
    }

    // Listen to connection status changes
    service!.statusStream.listen(
      (status) {
        state = state.copyWith(status: status);
        if (status == ConnectionStatus.connected) {
          _saveUrl(service!.publicUrl);
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

    service!.metadataStream.listen((metadata) {
      if (metadata.roomId.isNotEmpty) {
        _shareRoomId = metadata.roomId;
      }
      if (metadata.renewToken.isNotEmpty) {
        _shareRenewToken = metadata.renewToken;
        _persistResumeState();
      }
      if (metadata.notice.isNotEmpty) {
        debugPrint('[connection] relay notice: ${metadata.notice}');
      }
    });

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
        log('[tunnel] active_session: session=$sessionId currentSession=$_sessionId lastEvent=$_lastAppliedEventId');
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
        final sessionId =
            msg.sessionId ?? msg.data?['session_id'] as String? ?? '';
        _sessionId = sessionId;
        // Always restore local snapshot first — relay replay will skip
        // already-cached events via ordinal dedup.
        _restoreSessionProjectionIfAvailable(sessionId);
        unawaited(ref.read(workspaceCacheProvider.notifier).registerLiveSession(
            sessionId, ref.read(sessionInfoProvider),
            lastEventId: _lastAppliedEventId));
        _restoreCachedAgentStatus(sessionId: sessionId);
        _persistResumeState();
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
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
        }
        unawaited(ref.read(workspaceCacheProvider.notifier).registerLiveSession(
              _sessionId.isNotEmpty ? _sessionId : (msg.sessionId ?? ''),
              data,
              lastEventId: _lastAppliedEventId,
            ));
        break;

      case 'activity':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.ActivityData.fromJson(msg.data!);
          _setAgentActivity(data.activity);
        }
        _markEventApplied(msg);
        break;

      case 'user_message':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        final msgId = msg.data?['id'] as String? ?? msg.streamId;
        if (msgId != null && msgId.isNotEmpty) {
          chatNotifier.finalizeStreaming(msgId);
        }
        _markEventApplied(msg);
        break;

      case 'reasoning_done':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        final msgId = msg.data?['id'] as String? ?? msg.streamId;
        if (msgId != null && msgId.isNotEmpty) {
          chatNotifier.finalizeReasoning(msgId);
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'status':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          chatNotifier
              .handleToolResult(proto.ToolResultData.fromJson(msg.data!));
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'approval_request':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.ApprovalRequestData.fromJson(msg.data!);
          ref.read(approvalProvider.notifier).set(ApprovalInfo(
              id: data.id, toolName: data.toolName, input: data.input));
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'approval_result':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.AskUserRequestData.fromJson(msg.data!);
          // Build a human-readable summary of the questions
          final detail = data.questions.map(describeAskUserQuestion).join('\n');
          final amsgId = msg.eventId ?? newAskUserMessageId();
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.AskUserResponseData.fromJson(msg.data!);
          final askUser = ref.read(askUserProvider);
          if (askUser != null && askUser.id == data.id) {
            if (askUser.msgId.isNotEmpty) {
              chatNotifier.updateAskUserAnswer(
                askUser.msgId,
                summarizeAskUserResponse(
                    askUser.questions, data.answers, data.status),
              );
            }
            ref.read(askUserProvider.notifier).set(null);
          }
        }
        _markEventApplied(msg);
        break;

      case 'subagent_spawn':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.SubagentReasoningData.fromJson(msg.data!);
          final reasoningId = '${data.agentId}-${data.id}';
          chatNotifier.finalizeReasoning(reasoningId);
        }
        _markEventApplied(msg);
        break;

      case 'subagent_status':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.SubagentCompleteData.fromJson(msg.data!);
          chatNotifier.finalizePendingReasoning(
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
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
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
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
      _clientId = Uuid().v4();
      await prefs.setString(_resumeClientIdKey, _clientId);
    }
    _sessionId = prefs.getString(_resumeSessionIdKey) ?? _sessionId;
    _lastAppliedEventId =
        prefs.getString(_resumeEventIdKey) ?? _lastAppliedEventId;
    _shareRoomId = prefs.getString(_resumeRoomIdKey) ?? _shareRoomId;
    _shareRenewToken =
        prefs.getString(_resumeRenewTokenKey) ?? _shareRenewToken;
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
      log('[tunnel] _restoreSessionProjectionIfAvailable: SKIP session=$sessionId canRestore=${_canRestoreSessionProjection()} hasAuth=$_hasAuthoritativeProjection subs=${ref.read(subagentProvider).isEmpty} info=${ref.read(sessionInfoProvider)}');
      return;
    }
    log('[tunnel] _restoreSessionProjectionIfAvailable: restoring session=$sessionId');
    unawaited(
      ref
          .read(workspaceCacheProvider.notifier)
          .attachSessionToActiveWorkspace(sessionId)
          .then((restored) {
        log('[tunnel] attachSession returned: restored=$restored canRestore=${_canRestoreSessionProjection()} lastEvent=$_lastAppliedEventId');
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
    if (_shareRoomId.isNotEmpty) {
      await prefs.setString(_resumeRoomIdKey, _shareRoomId);
    } else {
      await prefs.remove(_resumeRoomIdKey);
    }
    if (_shareRenewToken.isNotEmpty) {
      await prefs.setString(_resumeRenewTokenKey, _shareRenewToken);
    } else {
      await prefs.remove(_resumeRenewTokenKey);
    }
  }

  Future<void> _clearPersistedResumeState([SharedPreferences? prefs]) async {
    final store = prefs ?? await SharedPreferences.getInstance();
    await store.remove(_resumeSessionIdKey);
    await store.remove(_resumeEventIdKey);
    await store.remove(_resumeRoomIdKey);
    await store.remove(_resumeRenewTokenKey);
    _shareRoomId = '';
    _shareRenewToken = '';
  }
}

// ---- Message delivery status for ack tracking ----
