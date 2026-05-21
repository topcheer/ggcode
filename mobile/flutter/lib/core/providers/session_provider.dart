import 'dart:async';
import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:uuid/uuid.dart';

import '../connection_service.dart';
export '../connection_service.dart' show ConnectionStatus;
import '../crypto.dart';
import '../models/protocol.dart' as proto;

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
    await connectWorkspace(workspaceKey, clearState: false);
  }

  Future<void> connectWorkspace(String workspaceKey,
      {bool clearState = true}) async {
    final cache = ref.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    final url = cache.urlForWorkspace(workspaceKey);
    if (url == null || url.isEmpty) return;
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

    // Listen to connection status changes
    service!.statusStream.listen(
      (status) {
        state = state.copyWith(status: status);
        if (status == ConnectionStatus.connected) {
          _saveUrl(url);
          final hasProjection = ref.read(chatProvider).isNotEmpty ||
              ref.read(subagentProvider).isNotEmpty;
          service?.sendResumeHello(
            clientId: _clientId,
            sessionId: _sessionId,
            lastEventId: hasProjection ? _lastAppliedEventId : null,
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
      case 'resume_ack':
        final resumeMode = msg.data?['resume_mode'] as String? ?? 'incremental';
        final sessionId =
            msg.sessionId ?? msg.data?['session_id'] as String? ?? '';
        _sessionId = sessionId;
        _awaitingReplay = false;
        if (resumeMode == 'full_history') {
          chatNotifier.clearMessages();
          ref.read(subagentProvider.notifier).clear();
          _lastAppliedEventId = '';
          _recentEventIds.clear();
          _recentEventSet.clear();
        }
        unawaited(ref
            .read(workspaceCacheProvider.notifier)
            .registerLiveSession(sessionId, ref.read(sessionInfoProvider),
                lastEventId: _lastAppliedEventId));
        _persistResumeState();
        break;

      case 'resume_miss':
        _awaitingReplay = false;
        break;

      case 'snapshot_reset':
        chatNotifier.clearMessages();
        ref.read(subagentProvider.notifier).clear();
        _lastAppliedEventId = '';
        _recentEventIds.clear();
        _recentEventSet.clear();
        if (msg.sessionId != null && msg.sessionId!.isNotEmpty) {
          _sessionId = msg.sessionId!;
        }
        unawaited(ref
            .read(workspaceCacheProvider.notifier)
            .registerLiveSession(_sessionId, ref.read(sessionInfoProvider),
                lastEventId: _lastAppliedEventId));
        _persistResumeState();
        break;

      case 'session_info':
        if (!_shouldApplyEvent(msg)) break;
        final data = proto.SessionInfoData.fromJson(msg.data!);
        ref.read(sessionInfoProvider.notifier).set(data);
        ref.read(currentModeProvider.notifier).set(data.mode);
        _markEventApplied(msg);
        unawaited(ref
            .read(workspaceCacheProvider.notifier)
            .registerLiveSession(
              _sessionId.isNotEmpty ? _sessionId : (msg.sessionId ?? ''),
              data,
              lastEventId: _lastAppliedEventId,
            ));
        break;

      case 'user_message':
        if (!_shouldApplyEvent(msg)) break;
        if (msg.data != null) {
          final text = msg.data!['text'] as String? ?? '';
          if (text.isNotEmpty) {
            chatNotifier.addRemoteUserMessage(
              text,
              messageId: msg.eventId ??
                  'remote-user-${DateTime.now().millisecondsSinceEpoch}',
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
          ref.read(agentStatusProvider.notifier).set(status);
          ref.read(agentStatusMessageProvider.notifier).set(message);
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
    ref.read(agentStatusProvider.notifier).set('idle');
    ref.read(agentStatusMessageProvider.notifier).set('');
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
      _recentEventIds.clear();
      _recentEventSet.clear();
      _lastAppliedEventId = '';
      _sessionId = sessionId;
      unawaited(ref
          .read(workspaceCacheProvider.notifier)
          .observeLiveSession(sessionId,
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
        return false;
      }
      if (next > last + 1 && !_awaitingReplay) {
        _awaitingReplay = true;
        service?.requestReplayFrom(
          clientId: _clientId,
          sessionId: _sessionId,
          lastEventId: _lastAppliedEventId,
        );
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
    this.isToolError = false,
    required this.time,
  });

  ChatMessage copyWith({
    String? text,
    bool? streaming,
    String? toolResult,
    bool? isToolError,
  }) =>
      ChatMessage(
        id: id,
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
        isToolError: json['is_tool_error'] as bool? ?? false,
        time: DateTime.tryParse(json['time'] as String? ?? '') ?? DateTime.now(),
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
            msg.copyWith(toolResult: result, isToolError: isError)
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
            msg.copyWith(toolResult: data.result, isToolError: data.isError)
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
            msg.copyWith(toolResult: answer, isToolError: false)
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
