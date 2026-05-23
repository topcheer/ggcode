import 'dart:async';
import 'dart:collection';
import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:uuid/uuid.dart';

import '../connection_service.dart';
export '../connection_service.dart' show ConnectionStatus;
import '../crypto.dart';
import '../l10n/app_localizations.dart';
import '../models/protocol.dart' as proto;
import '../theme/app_theme.dart';

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
  bool _awaitingReplay = false;
  final SplayTreeMap<int, proto.WsMessage> _pendingReplayEvents =
      SplayTreeMap<int, proto.WsMessage>();
  final List<String> _recentEventIds = <String>[];
  final Set<String> _recentEventSet = <String>{};

  @override
  TunnelConnectionState build() =>
      TunnelConnectionState(status: ConnectionStatus.disconnected);

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

  Future<void> connect(String url, {bool clearState = true}) async {
    url = normalizeTunnelUrl(url);
    final cache = ref.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    await cache.activateWorkspaceUrl(url);

    // Disconnect previous if any
    if (service != null) {
      service!.dispose();
      service = null;
    }

    // Clear previous session state (skip on reconnect from background)
    if (clearState) {
      _clearUiProjection();
      _lastAppliedEventId = '';
      _awaitingReplay = false;
      _pendingReplayEvents.clear();
      _recentEventIds.clear();
      _recentEventSet.clear();
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
    service = ConnectionService(url: url, crypto: crypto);
    await _loadResumeState();
    if (!clearState && _hasEmptyUiProjection()) {
      _restoreProjectionFromCache(adoptCursor: false);
    }
    if (clearState) {
      if (!_restoreProjectionFromCache()) {
        _sessionId = '';
        _lastAppliedEventId = '';
      }
      _awaitingReplay = false;
      _pendingReplayEvents.clear();
      _recentEventIds.clear();
      _recentEventSet.clear();
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
            lastEventId:
                _lastAppliedEventId.isNotEmpty ? _lastAppliedEventId : null,
          );
        }
      },
    );

    // Listen to error messages
    service!.errorStream.listen(
      (error) {
        state = state.copyWith(error: error);
      },
    );

    // Listen to messages from server
    service!.messageStream.listen(
      (msg) => _dispatchMessage(msg),
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
    _awaitingReplay = false;
    _pendingReplayEvents.clear();
    _recentEventIds.clear();
    _recentEventSet.clear();
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_resumeSessionIdKey);
    await prefs.remove(_resumeEventIdKey);
    await ref.read(workspaceCacheProvider.notifier).clearSelection();
    state = TunnelConnectionState(status: ConnectionStatus.disconnected);
  }

  void send(Map<String, dynamic> data) {
    final msg = proto.WsMessage(
        type: data['type'] as String? ?? 'message',
        data: data['data'] as Map<String, dynamic>?);
    service?.sendEncrypted(msg);
  }

  String? _extractToken(String url) {
    final uri = Uri.tryParse(url);
    return uri?.queryParameters['token'];
  }

  void _dispatchMessage(proto.WsMessage msg) {
    final chatNotifier = ref.read(chatProvider.notifier);

    switch (msg.type) {
      case 'active_session':
        final sessionId =
            msg.sessionId ?? msg.data?['session_id'] as String? ?? '';
        if (sessionId.isEmpty) break;
        if (_sessionId.isNotEmpty && _sessionId != sessionId) {
          _clearUiProjection();
          _lastAppliedEventId = '';
          _pendingReplayEvents.clear();
          _awaitingReplay = false;
          _recentEventIds.clear();
          _recentEventSet.clear();
        }
        _sessionId = sessionId;
        unawaited(ref.read(workspaceCacheProvider.notifier).registerLiveSession(
            sessionId, ref.read(sessionInfoProvider),
            lastEventId: _lastAppliedEventId));
        _persistResumeState();
        break;

      case 'resume_ack':
        final resumeMode = msg.data?['resume_mode'] as String? ?? 'incremental';
        final sessionId =
            msg.sessionId ?? msg.data?['session_id'] as String? ?? '';
        _sessionId = sessionId;
        _awaitingReplay = _pendingReplayEvents.isNotEmpty;
        if (resumeMode == 'full_history') {
          chatNotifier.clearMessages();
          ref.read(subagentProvider.notifier).clear();
          _lastAppliedEventId = '';
          _pendingReplayEvents.clear();
          _awaitingReplay = false;
          _recentEventIds.clear();
          _recentEventSet.clear();
        }
        unawaited(ref.read(workspaceCacheProvider.notifier).registerLiveSession(
            sessionId, ref.read(sessionInfoProvider),
            lastEventId: _lastAppliedEventId));
        _restoreCachedAgentStatus(sessionId: sessionId);
        _persistResumeState();
        break;

      case 'resume_miss':
        _pendingReplayEvents.clear();
        _awaitingReplay = false;
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
        chatNotifier.clearMessages();
        ref.read(subagentProvider.notifier).clear();
        _lastAppliedEventId = '';
        _pendingReplayEvents.clear();
        _awaitingReplay = false;
        _recentEventIds.clear();
        _recentEventSet.clear();
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
        if (!_shouldApplyEvent(msg)) break;
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
        unawaited(ref.read(workspaceCacheProvider.notifier).registerLiveSession(
              _sessionId.isNotEmpty ? _sessionId : (msg.sessionId ?? ''),
              data,
              lastEventId: _lastAppliedEventId,
            ));
        break;

      case 'user_message':
        if (!_shouldApplyEvent(msg)) break;
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
              );
            }
          }
        }
        _markEventApplied(msg);
        break;

      case 'system_message':
        if (!_shouldApplyEvent(msg)) break;
        if (msg.data != null) {
          final data = proto.MessageData.fromJson(msg.data!);
          final displayText =
              data.displayText.isNotEmpty ? data.displayText : data.text;
          if (displayText.isNotEmpty) {
            chatNotifier.addRemoteSystemMessage(
              displayText,
              messageId: msg.eventId ??
                  'remote-system-${DateTime.now().millisecondsSinceEpoch}',
            );
          }
        }
        _markEventApplied(msg);
        break;

      case 'text':
      case 'stream_text':
        if (!_shouldApplyEvent(msg)) break;
        if (msg.data != null) {
          final text = msg.data!['text'] as String? ??
              msg.data!['chunk'] as String? ??
              '';
          final msgId = msg.data!['id'] as String? ??
              'msg-${DateTime.now().millisecondsSinceEpoch}';
          final done = msg.data!['done'] as bool? ?? false;
          chatNotifier.handleTextChunk(
              proto.TextData(id: msgId, chunk: text, done: done));
        }
        _markEventApplied(msg);
        break;

      case 'stream_start':
        break;

      case 'stream_end':
      case 'text_done':
        if (!_shouldApplyEvent(msg)) break;
        final msgId = msg.data?['id'] as String? ?? msg.streamId;
        if (msgId != null && msgId.isNotEmpty) {
          chatNotifier.finalizeStreaming(msgId);
        }
        _markEventApplied(msg);
        break;

      case 'status':
        if (!_shouldApplyEvent(msg)) break;
        if (msg.data != null) {
          final status = msg.data!['status'] as String? ?? 'idle';
          final message = msg.data!['message'] as String? ?? '';
          _setAgentStatus(status, message);
        }
        _markEventApplied(msg);
        break;

      case 'tool_call':
        if (!_shouldApplyEvent(msg)) break;
        if (msg.data != null) {
          chatNotifier.handleToolCall(
            proto.ToolCallData.fromJson(msg.data!),
            messageId: msg.eventId ??
                msg.streamId ??
                'tool-${DateTime.now().millisecondsSinceEpoch}',
          );
        }
        _markEventApplied(msg);
        break;

      case 'tool_result':
        if (!_shouldApplyEvent(msg)) break;
        if (msg.data != null) {
          chatNotifier
              .handleToolResult(proto.ToolResultData.fromJson(msg.data!));
        }
        _markEventApplied(msg);
        break;

      case 'approval_request':
        if (!_shouldApplyEvent(msg)) break;
        if (msg.data != null) {
          final data = proto.ApprovalRequestData.fromJson(msg.data!);
          ref.read(approvalProvider.notifier).set(ApprovalInfo(
              id: data.id, toolName: data.toolName, input: data.input));
        }
        _markEventApplied(msg);
        break;

      case 'approval_result':
        if (!_shouldApplyEvent(msg)) break;
        if (msg.data != null) {
          final data = proto.ApprovalResultData.fromJson(msg.data!);
          final approval = ref.read(approvalProvider);
          if (approval != null && approval.id == data.id) {
            ref.read(approvalProvider.notifier).set(null);
          }
        }
        _markEventApplied(msg);
        break;

      case 'ask_user_request':
        if (!_shouldApplyEvent(msg)) break;
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
        break;

      case 'ask_user_response':
        if (!_shouldApplyEvent(msg)) break;
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
        if (!_shouldApplyEvent(msg)) break;
        if (msg.data != null) {
          final data = proto.SubagentSpawnData.fromJson(msg.data!);
          final agents =
              Map<String, SubagentInfo>.from(ref.read(subagentProvider));
          agents[data.agentId] = SubagentInfo(
            agentId: data.agentId,
            name: data.name,
            task: data.task,
            color: data.color,
            parentId: data.parentId,
          );
          ref.read(subagentProvider.notifier).set(agents);
        }
        _markEventApplied(msg);
        break;

      case 'subagent_text':
        if (!_shouldApplyEvent(msg)) break;
        if (msg.data != null) {
          chatNotifier
              .handleSubagentText(proto.SubagentTextData.fromJson(msg.data!));
        }
        _markEventApplied(msg);
        break;

      case 'subagent_status':
        if (!_shouldApplyEvent(msg)) break;
        if (msg.data != null) {
          final data = proto.SubagentStatusData.fromJson(msg.data!);
          final agents =
              Map<String, SubagentInfo>.from(ref.read(subagentProvider));
          if (agents.containsKey(data.agentId)) {
            agents[data.agentId] =
                agents[data.agentId]!.copyWith(status: data.status);
            ref.read(subagentProvider.notifier).set(agents);
          }
        }
        _markEventApplied(msg);
        break;

      case 'subagent_complete':
        if (!_shouldApplyEvent(msg)) break;
        if (msg.data != null) {
          final data = proto.SubagentCompleteData.fromJson(msg.data!);
          final agents =
              Map<String, SubagentInfo>.from(ref.read(subagentProvider));
          if (agents.containsKey(data.agentId)) {
            agents[data.agentId] = agents[data.agentId]!.copyWith(
              status: 'completed',
              completed: true,
              success: data.success,
              summary: data.summary,
            );
            ref.read(subagentProvider.notifier).set(agents);
          }
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
        if (!_shouldApplyEvent(msg)) break;
        if (msg.data != null) {
          final data = proto.SubagentToolCallData.fromJson(msg.data!);
          final agents = ref.read(subagentProvider);
          final agent = agents[data.agentId];
          final chatNotifier = ref.read(chatProvider.notifier);
          chatNotifier.addSubagentToolCall(
            messageId: msg.eventId ?? data.toolId,
            agentId: data.agentId,
            toolId: data.toolId,
            toolName: data.toolName,
            displayName: data.displayName,
            args: data.args,
            detail: data.detail,
            sourceName: agent?.name ?? data.agentId,
            sourceColor: agent?.color ?? '#4CAF50',
          );
        }
        _markEventApplied(msg);
        break;

      case 'subagent_tool_result':
        if (!_shouldApplyEvent(msg)) break;
        if (msg.data != null) {
          final data = proto.SubagentToolResultData.fromJson(msg.data!);
          final chatNotifier = ref.read(chatProvider.notifier);
          chatNotifier.updateSubagentToolResult(
            agentId: data.agentId,
            toolId: data.toolId,
            toolName: data.toolName,
            result: data.result,
            isError: data.isError,
          );
        }
        _markEventApplied(msg);
        break;

      case 'error':
        if (!_shouldApplyEvent(msg)) break;
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

      case 'sharing_stopped':
        _clearUiProjection();
        service?.disconnect();
        ref.read(workspaceCacheProvider.notifier).markDisconnected();
        state = state.copyWith(status: ConnectionStatus.disconnected);
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
  }

  void _setAgentStatus(String status, String message) {
    ref.read(agentStatusProvider.notifier).set(status);
    ref.read(agentStatusMessageProvider.notifier).set(message);
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
    _setAgentStatus(snapshot.agentStatus, snapshot.agentStatusMessage);
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

  bool _shouldApplyEvent(proto.WsMessage msg) {
    final eventId = msg.eventId;
    final sessionId = msg.sessionId ?? _sessionId;
    if (sessionId.isNotEmpty &&
        _sessionId.isNotEmpty &&
        sessionId != _sessionId) {
      final previousSessionId = _sessionId;
      ref.read(chatProvider.notifier).clearMessages();
      ref.read(subagentProvider.notifier).clear();
      _pendingReplayEvents.clear();
      _awaitingReplay = false;
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
    if (_recentEventSet.contains(eventId)) {
      return false;
    }
    final next = _parseEventOrdinal(eventId);
    final last = _parseEventOrdinal(_lastAppliedEventId);
    if (last != null && next != null) {
      if (next <= last) {
        _pendingReplayEvents.remove(next);
        return false;
      }
      if (next > last + 1) {
        _pendingReplayEvents[next] = msg;
        if (!_awaitingReplay) {
          _awaitingReplay = true;
          service?.requestReplayFrom(
            clientId: _clientId,
            sessionId: _sessionId,
            lastEventId: _lastAppliedEventId,
          );
        }
        return false;
      }
    }
    return true;
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
    _awaitingReplay = false;
    _lastAppliedEventId = eventId;
    final ordinal = _parseEventOrdinal(eventId);
    if (ordinal != null) {
      _pendingReplayEvents.remove(ordinal);
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
    _drainBufferedReplayEvents();
  }

  int? _parseEventOrdinal(String? eventId) {
    if (eventId == null || eventId.isEmpty) return null;
    final idx = eventId.lastIndexOf('-');
    final raw = idx >= 0 ? eventId.substring(idx + 1) : eventId;
    return int.tryParse(raw);
  }

  void _drainBufferedReplayEvents() {
    while (true) {
      final last = _parseEventOrdinal(_lastAppliedEventId);
      if (last == null) {
        break;
      }
      final pending = _pendingReplayEvents.remove(last + 1);
      if (pending == null) {
        break;
      }
      _dispatchMessage(pending);
    }
    _awaitingReplay = _pendingReplayEvents.isNotEmpty;
  }

  bool _hasEmptyUiProjection() {
    return ref.read(chatProvider).isEmpty &&
        ref.read(subagentProvider).isEmpty &&
        ref.read(sessionInfoProvider) == null;
  }

  bool _restoreProjectionFromCache({bool adoptCursor = true}) {
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
    _setAgentStatus(snapshot.agentStatus, snapshot.agentStatusMessage);

    final sessionKey = _sessionCacheKey(workspaceKey, sessionId);
    final record = cacheState.sessions[sessionKey];
    if (adoptCursor) {
      _sessionId = sessionId;
      _lastAppliedEventId = record?.lastEventId ?? '';
    }
    return true;
  }

  void handleIncomingForTest(proto.WsMessage msg) {
    _dispatchMessage(msg);
  }

  bool restoreProjectionFromCacheForTest({bool adoptCursor = true}) {
    return _restoreProjectionFromCache(adoptCursor: adoptCursor);
  }

  void _persistResumeState() {
    SharedPreferences.getInstance().then((prefs) {
      prefs.setString(_resumeClientIdKey, _clientId);
      prefs.setString(_resumeSessionIdKey, _sessionId);
      prefs.setString(_resumeEventIdKey, _lastAppliedEventId);
    });
  }
}

// ---- Chat Messages Provider ----

class ChatMessage {
  final String id;
  final String? sourceId; // null = main agent
  final String? sourceName;
  final String? sourceColor;
  final bool isUser;
  final String text;
  final bool streaming;
  final String? toolId;
  final String? toolName;
  final String? toolDisplayName;
  final String? toolDetail;
  final String? toolResult;
  final bool toolCompleted;
  final bool isToolError;
  final DateTime time;

  ChatMessage({
    required this.id,
    this.sourceId,
    this.sourceName,
    this.sourceColor,
    this.isUser = false,
    this.text = '',
    this.streaming = false,
    this.toolId,
    this.toolName,
    this.toolDisplayName,
    this.toolDetail,
    this.toolResult,
    this.toolCompleted = false,
    this.isToolError = false,
    required this.time,
  });

  ChatMessage copyWith({
    String? id,
    String? text,
    bool? streaming,
    String? toolResult,
    bool? toolCompleted,
    bool? isToolError,
  }) =>
      ChatMessage(
        id: id ?? this.id,
        sourceId: sourceId,
        sourceName: sourceName,
        sourceColor: sourceColor,
        isUser: isUser,
        text: text ?? this.text,
        streaming: streaming ?? this.streaming,
        toolId: toolId,
        toolName: toolName,
        toolDisplayName: toolDisplayName,
        toolDetail: toolDetail,
        toolResult: toolResult ?? this.toolResult,
        toolCompleted: toolCompleted ?? this.toolCompleted,
        isToolError: isToolError ?? this.isToolError,
        time: time,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'source_id': sourceId,
        'source_name': sourceName,
        'source_color': sourceColor,
        'is_user': isUser,
        'text': text,
        'streaming': streaming,
        'tool_id': toolId,
        'tool_name': toolName,
        'tool_display_name': toolDisplayName,
        'tool_detail': toolDetail,
        'tool_result': toolResult,
        'tool_completed': toolCompleted,
        'is_tool_error': isToolError,
        'time': time.toIso8601String(),
      };

  factory ChatMessage.fromJson(Map<String, dynamic> json) => ChatMessage(
        id: json['id'] as String? ?? '',
        sourceId: json['source_id'] as String?,
        sourceName: json['source_name'] as String?,
        sourceColor: json['source_color'] as String?,
        isUser: json['is_user'] as bool? ?? false,
        text: json['text'] as String? ?? '',
        streaming: json['streaming'] as bool? ?? false,
        toolId: json['tool_id'] as String?,
        toolName: json['tool_name'] as String?,
        toolDisplayName: json['tool_display_name'] as String?,
        toolDetail: json['tool_detail'] as String?,
        toolResult: json['tool_result'] as String?,
        toolCompleted: json['tool_completed'] as bool? ?? false,
        isToolError: json['is_tool_error'] as bool? ?? false,
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

  void addUserMessage(String text) {
    state = [
      ...state,
      ChatMessage(
        id: 'user-${_msgCounter++}',
        isUser: true,
        text: text,
        time: DateTime.now(),
      ),
    ];
    ref.read(connectionProvider.notifier).send({
      'type': 'message',
      'data': {'text': text},
    });
  }

  void addRemoteUserMessage(String text, {String? messageId}) {
    state = [
      ...state,
      ChatMessage(
        id: messageId ?? 'remote-user-${_msgCounter++}',
        isUser: true,
        text: text,
        time: DateTime.now(),
      ),
    ];
  }

  void addRemoteSystemMessage(String text, {String? messageId}) {
    state = [
      ...state,
      ChatMessage(
        id: messageId ?? 'remote-system-${_msgCounter++}',
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
    final idx = state.indexWhere((m) => m.id == data.id);
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
          id: data.id,
          text: data.chunk,
          streaming: !data.done,
          time: DateTime.now(),
        ),
      ];
    }
  }

  void handleSubagentText(proto.SubagentTextData data) {
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
    required bool isError,
  }) {
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
              toolResult: result,
              toolCompleted: true,
              isToolError: isError,
            )
          else
            state[i],
      ];
    }
  }

  void handleToolCall(proto.ToolCallData data, {String? messageId}) {
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
              toolResult: data.result,
              toolCompleted: true,
              isToolError: data.isError,
            )
          else
            state[i],
      ];
    }
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

  void addErrorMessage(String message, {String? messageId}) {
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

  const CachedSessionSnapshot({
    required this.messages,
    required this.subagents,
    required this.sessionInfo,
    this.agentStatus = 'idle',
    this.agentStatusMessage = '',
  });

  Map<String, dynamic> toJson() => {
        'messages': messages.map((m) => m.toJson()).toList(),
        'subagents': subagents.map((k, v) => MapEntry(k, v.toJson())),
        'session_info': _sessionInfoToJson(sessionInfo),
        'agent_status': agentStatus,
        'agent_status_message': agentStatusMessage,
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
      agentStatus: json['agent_status'] as String? ?? 'idle',
      agentStatusMessage: json['agent_status_message'] as String? ?? '',
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
const _workspaceSnapshotPrefix = 'ggcode_workspace_snapshot_v1_';

final workspaceCacheProvider =
    NotifierProvider<WorkspaceCacheNotifier, WorkspaceCacheState>(
  WorkspaceCacheNotifier.new,
);

class WorkspaceCacheNotifier extends Notifier<WorkspaceCacheState> {
  static const _indexKey = 'ggcode_workspace_cache_v1';
  static const _maxWorkspaces = 10;
  static const _maxSessionsPerWorkspace = 20;
  static const _maxMessagesPerSession = 300;

  SharedPreferences? _prefs;
  bool _initializing = false;
  Timer? _flushTimer;
  final Set<String> _dirtySnapshots = <String>{};

  @override
  WorkspaceCacheState build() => const WorkspaceCacheState(
        initialized: false,
        workspaces: {},
        sessions: {},
        snapshots: {},
      );

  Future<void> initialize() async {
    if (state.initialized || _initializing) return;
    _initializing = true;
    try {
      _prefs ??= await SharedPreferences.getInstance();
      final raw = _prefs!.getString(_indexKey);
      if (raw == null || raw.isEmpty) {
        state = state.copyWith(initialized: true);
        return;
      }
      final json = jsonDecode(raw) as Map<String, dynamic>;
      final workspaces = <String, WorkspaceRecord>{};
      for (final item in json['workspaces'] as List<dynamic>? ?? const []) {
        final record =
            WorkspaceRecord.fromJson(Map<String, dynamic>.from(item));
        if (record.key.isNotEmpty) {
          workspaces[record.key] = record;
        }
      }
      final sessions = <String, CachedSessionRecord>{};
      for (final item in json['sessions'] as List<dynamic>? ?? const []) {
        final record =
            CachedSessionRecord.fromJson(Map<String, dynamic>.from(item));
        if (record.workspaceKey.isNotEmpty && record.sessionId.isNotEmpty) {
          sessions[_sessionCacheKey(record.workspaceKey, record.sessionId)] =
              record;
        }
      }
      state = WorkspaceCacheState(
        initialized: true,
        workspaces: workspaces,
        sessions: sessions,
        snapshots: {},
        selectedWorkspaceKey: json['selected_workspace_key'] as String?,
        selectedSessionId: json['selected_session_id'] as String?,
      );
      final workspaceKey = state.selectedWorkspaceKey;
      final sessionId = state.selectedSessionId;
      if (workspaceKey != null &&
          workspaceKey.isNotEmpty &&
          sessionId != null &&
          sessionId.isNotEmpty) {
        await _ensureSnapshotLoaded(workspaceKey, sessionId);
      }
    } finally {
      _initializing = false;
    }
  }

  String? urlForWorkspace(String workspaceKey) =>
      state.workspaces[workspaceKey]?.url;

  Future<void> activateWorkspaceUrl(String url) async {
    await initialize();
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
      workspaces: _prunedWorkspaces(workspaces),
      selectedWorkspaceKey: key,
      selectedSessionId: selectedSessionId,
      liveWorkspaceKey: key,
      liveSessionId: state.liveWorkspaceKey == key ? state.liveSessionId : null,
    );
    await _persistIndex();
    if (selectedSessionId != null && selectedSessionId.isNotEmpty) {
      await _ensureSnapshotLoaded(key, selectedSessionId);
    }
  }

  Future<void> clearSelection() async {
    await initialize();
    state = state.copyWith(
      selectedWorkspaceKey: null,
      selectedSessionId: null,
      liveWorkspaceKey: null,
      liveSessionId: null,
    );
    await _persistIndex();
  }

  void markDisconnected() {
    state = state.copyWith(liveWorkspaceKey: null, liveSessionId: null);
  }

  Future<void> selectSession(String workspaceKey, String sessionId) async {
    await initialize();
    state = state.copyWith(
      selectedWorkspaceKey: workspaceKey,
      selectedSessionId: sessionId,
    );
    await _ensureSnapshotLoaded(workspaceKey, sessionId);
    await _persistIndex();
  }

  Future<void> registerLiveSession(
    String sessionId,
    proto.SessionInfoData? sessionInfo, {
    String? lastEventId,
  }) async {
    if (sessionId.isEmpty) return;
    await initialize();
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
      workspaces: _prunedWorkspaces(workspaces),
      sessions: _prunedSessions(sessions),
      liveWorkspaceKey: workspaceKey,
      liveSessionId: sessionId,
      selectedWorkspaceKey:
          selectionFollowedLive ? workspaceKey : state.selectedWorkspaceKey,
      selectedSessionId:
          selectionFollowedLive ? sessionId : state.selectedSessionId,
    );
    await _persistIndex();
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
    final workspaceKey = state.liveWorkspaceKey;
    final sessionId = state.liveSessionId;
    if (workspaceKey == null ||
        workspaceKey.isEmpty ||
        sessionId == null ||
        sessionId.isEmpty) {
      return;
    }
    final trimmedMessages = messages.length <= _maxMessagesPerSession
        ? messages
        : messages.sublist(messages.length - _maxMessagesPerSession);
    final snapshot = CachedSessionSnapshot(
      messages: List<ChatMessage>.from(trimmedMessages),
      subagents: Map<String, SubagentInfo>.from(subagents),
      sessionInfo: sessionInfo,
      agentStatus: agentStatus,
      agentStatusMessage: agentStatusMessage,
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
      workspaces: _prunedWorkspaces(workspaces),
      sessions: _prunedSessions(sessions),
      snapshots: snapshots,
    );
    _dirtySnapshots.add(snapshotKey);
    _scheduleFlush();
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

  CachedSessionSnapshot? snapshotFor(String workspaceKey, String sessionId) =>
      state.snapshots[_sessionCacheKey(workspaceKey, sessionId)];

  Future<void> _ensureSnapshotLoaded(
      String workspaceKey, String sessionId) async {
    final key = _sessionCacheKey(workspaceKey, sessionId);
    if (state.snapshots.containsKey(key)) return;
    _prefs ??= await SharedPreferences.getInstance();
    final raw = _prefs!.getString(_snapshotStorageKey(key));
    if (raw == null || raw.isEmpty) return;
    final snapshot =
        CachedSessionSnapshot.fromJson(jsonDecode(raw) as Map<String, dynamic>);
    state = state.copyWith(
      snapshots: Map<String, CachedSessionSnapshot>.from(state.snapshots)
        ..[key] = snapshot,
    );
  }

  void _scheduleFlush() {
    _flushTimer?.cancel();
    _flushTimer = Timer(const Duration(milliseconds: 350), () async {
      await _flushDirtySnapshots();
      await _persistIndex();
    });
  }

  Future<void> _flushDirtySnapshots() async {
    if (_dirtySnapshots.isEmpty) return;
    _prefs ??= await SharedPreferences.getInstance();
    final pending = List<String>.from(_dirtySnapshots);
    _dirtySnapshots.clear();
    for (final key in pending) {
      final snapshot = state.snapshots[key];
      if (snapshot == null) continue;
      await _prefs!.setString(
        _snapshotStorageKey(key),
        jsonEncode(snapshot.toJson()),
      );
    }
  }

  Future<void> _persistIndex() async {
    _prefs ??= await SharedPreferences.getInstance();
    final orderedSessions = state.sessions.values.toList()
      ..sort((a, b) => b.lastUpdatedAt.compareTo(a.lastUpdatedAt));
    final payload = {
      'workspaces':
          sortedWorkspaces().map((workspace) => workspace.toJson()).toList(),
      'sessions': orderedSessions.map((session) => session.toJson()).toList(),
      'selected_workspace_key': state.selectedWorkspaceKey,
      'selected_session_id': state.selectedSessionId,
    };
    await _prefs!.setString(_indexKey, jsonEncode(payload));
  }

  Map<String, WorkspaceRecord> _prunedWorkspaces(
      Map<String, WorkspaceRecord> workspaces) {
    final ordered = workspaces.values.toList()
      ..sort((a, b) => b.lastOpenedAt.compareTo(a.lastOpenedAt));
    final keep = ordered.take(_maxWorkspaces).toList();
    return <String, WorkspaceRecord>{
      for (final workspace in keep) workspace.key: workspace,
    };
  }

  Map<String, CachedSessionRecord> _prunedSessions(
      Map<String, CachedSessionRecord> sessions) {
    final result = <String, CachedSessionRecord>{};
    final grouped = <String, List<CachedSessionRecord>>{};
    for (final record in sessions.values) {
      grouped
          .putIfAbsent(record.workspaceKey, () => <CachedSessionRecord>[])
          .add(record);
    }
    for (final entry in grouped.entries) {
      entry.value.sort((a, b) => b.lastUpdatedAt.compareTo(a.lastUpdatedAt));
      for (final record in entry.value.take(_maxSessionsPerWorkspace)) {
        result[_sessionCacheKey(record.workspaceKey, record.sessionId)] =
            record;
      }
    }
    return result;
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
    return ref.watch(agentStatusProvider);
  }
  final workspaceKey = cache.selectedWorkspaceKey;
  final sessionId = cache.selectedSessionId;
  if (workspaceKey == null ||
      workspaceKey.isEmpty ||
      sessionId == null ||
      sessionId.isEmpty) {
    return 'idle';
  }
  return ref
          .watch(workspaceCacheProvider.notifier)
          .snapshotFor(workspaceKey, sessionId)
          ?.agentStatus ??
      'idle';
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

String _workspaceKeyForUrl(String url) =>
    base64UrlEncode(utf8.encode(url)).replaceAll('=', '');

String _sessionCacheKey(String workspaceKey, String sessionId) =>
    '$workspaceKey::$sessionId';

String _snapshotStorageKey(String sessionKey) =>
    '$_workspaceSnapshotPrefix${base64UrlEncode(utf8.encode(sessionKey)).replaceAll('=', '')}';

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
