import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/protocol.dart' as proto;
import '../models/tool_display.dart';
import 'connection_provider.dart';
import 'ui_providers.dart';

const _reasoningKind = 'reasoning';
const _redactedReasoningPlaceholder = 'Reasoning hidden by model.';

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
  final List<String> imageThumbnails; // base64 data URLs for display
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
    this.imageThumbnails = const [],
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
    List<String>? imageThumbnails,
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
        imageThumbnails: imageThumbnails ?? this.imageThumbnails,
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

  /// Replace all messages with cached messages from a session snapshot.
  /// Called when switching to a session that has cached data.
  void loadCachedMessages(List<ChatMessage> cached) {
    state = List.from(cached);
  }

  /// Send a user message with ack tracking.
  /// Generates a message_id, adds the message in 'sending' status,
  /// and sets a 5s timeout to mark as failed if no relay_ack arrives.
  void addUserMessage(String text, {List<proto.MessageImage> images = const []}) {
    final messageId =
        'user-${_msgCounter++}-${DateTime.now().millisecondsSinceEpoch}';

    // Build thumbnail data URLs for display in the bubble.
    final thumbnails = images
        .map((img) => 'data:${img.mime};base64,${img.data}')
        .toList();

    final msg = ChatMessage(
      id: messageId,
      isUser: true,
      text: text,
      imageThumbnails: thumbnails,
      time: DateTime.now(),
      status: MessageStatus.sending,
    );
    state = [...state, msg];

    // Send with message_id for ack tracking.
    ref.read(connectionProvider.notifier).send({
      'type': 'message',
      'message_id': messageId,
      'data': {
        'text': text,
        'message_id': messageId,
        if (images.isNotEmpty)
          'images': images.map((e) => e.toJson()).toList(),
      },
    });

    // 5s timeout: if no relay_ack by then, assume delivered via TCP.
    // This handles the case where an older relay doesn't send relay_ack.
    // Only mark as truly failed if the connection itself is broken.
    Future.delayed(const Duration(seconds: 5), () {
      if (!ref.mounted) return;
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
    finalizePendingReasoning(sourceId: null, collapse: true);
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

  bool bindRemoteUserMessage(
    String text, {
    required String remoteMessageId,
    String localMessageId = '',
  }) {
    var idx = -1;
    final normalizedLocalMessageId = localMessageId.trim();
    if (normalizedLocalMessageId.isNotEmpty) {
      idx = state.lastIndexWhere(
        (m) =>
            m.isUser &&
            m.sourceId == null &&
            m.toolName == null &&
            m.id == normalizedLocalMessageId &&
            m.id.startsWith('user-'),
      );
    }
    if (idx < 0) {
      idx = state.lastIndexWhere(
        (m) =>
            m.isUser &&
            m.sourceId == null &&
            m.toolName == null &&
            m.text == text &&
            m.id.startsWith('user-'),
      );
    }
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
    finalizePendingReasoning(sourceId: null, collapse: true);
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
    finalizePendingReasoning(sourceId: data.agentId, collapse: true);
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
    finalizePendingReasoning(sourceId: agentId, collapse: true);
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
    String displayName = '',
    String detail = '',
    String sourceName = '',
    String sourceColor = '',
    required String result,
    String summary = '',
    String payload = '',
    String payloadMode = '',
    required bool isError,
  }) {
    finalizePendingReasoning(sourceId: agentId, collapse: true);
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
    } else {
      final resolvedName = displayName.isNotEmpty
          ? displayName
          : toolCallDisplayName(toolName, '');
      state = [
        ...state,
        ChatMessage(
          id: '$agentId-tool-result-${toolId.isNotEmpty ? toolId : _msgCounter++}',
          sourceId: agentId,
          sourceName: sourceName,
          sourceColor: sourceColor,
          toolId: toolId,
          toolName: toolName,
          toolDisplayName: displayName,
          toolDetail: detail,
          text: resolvedName,
          toolResult: _resolvedToolSummary(toolName, result, summary, isError),
          toolPayload:
              _resolvedToolPayload(toolName, result, payload, payloadMode),
          toolPayloadMode: payloadMode,
          toolCompleted: true,
          isToolError: isError,
          time: DateTime.now(),
        ),
      ];
    }
  }

  void handleToolCall(proto.ToolCallData data, {String? messageId}) {
    finalizePendingReasoning(sourceId: null, collapse: true);
    // Server pushes raw toolName + args; we compute displayName locally
    final displayName = data.displayName.isNotEmpty
        ? data.displayName
        : toolCallDisplayName(data.toolName, data.args);
    final detail = data.detail.isNotEmpty
        ? data.detail
        : toolCallDetail(data.toolName, data.args);
    state = [
      ...state,
      ChatMessage(
        id: messageId ??
            'tool-${data.toolId.isNotEmpty ? data.toolId : _msgCounter++}',
        toolId: data.toolId,
        toolName: data.toolName,
        toolDisplayName: displayName,
        toolDetail: detail,
        text: displayName,
        time: DateTime.now(),
      ),
    ];
  }

  void handleToolResult(proto.ToolResultData data) {
    finalizePendingReasoning(sourceId: null, collapse: true);
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

  void finalizePendingReasoning({
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

  void finalizeStreamingMessagesForSource(String sourceId) {
    var changed = false;
    final next = [
      for (final msg in state)
        if (msg.sourceId == sourceId && msg.streaming)
          () {
            changed = true;
            return msg.copyWith(streaming: false);
          }()
        else
          msg,
    ];
    if (changed) {
      state = next;
    }
  }

  void addErrorMessage(String message, {String? messageId}) {
    finalizePendingReasoning(sourceId: null, collapse: true);
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
