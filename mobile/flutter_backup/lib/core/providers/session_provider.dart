import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../connection_service.dart';
import '../models/protocol.dart' as proto;

// ---- Connection Service Provider ----

final connectionProvider = StateNotifierProvider<ConnectionNotifier, ConnectionState>(
  (ref) => ConnectionNotifier(),
);

class ConnectionState {
  final ConnectionStatus status;
  final String? url;
  final String? error;

  ConnectionState({required this.status, this.url, this.error});

  ConnectionState copyWith({ConnectionStatus? status, String? url, String? error}) =>
      ConnectionState(
        status: status ?? this.status,
        url: url ?? this.url,
        error: error ?? this.error,
      );
}

class ConnectionNotifier extends StateNotifier<ConnectionState> {
  ConnectionService? _service;

  ConnectionNotifier() : super(ConnectionState(status: ConnectionStatus.disconnected));

  Future<void> connect(String url) async {
    state = state.copyWith(status: ConnectionStatus.connecting, url: url);
    _service = ConnectionService(url);

    _service!.messages.listen(
      (msg) => _handleMessage(msg),
      onError: (e) {
        state = state.copyWith(status: ConnectionStatus.disconnected, error: e.toString());
      },
      onDone: () {
        state = state.copyWith(status: ConnectionStatus.disconnected);
      },
    );

    try {
      await _service!.connect();
      state = state.copyWith(status: ConnectionStatus.connected);
      _saveUrl(url);
    } catch (e) {
      state = state.copyWith(status: ConnectionStatus.disconnected, error: e.toString());
    }
  }

  void disconnect() {
    _service?.disconnect();
    state = state.copyWith(status: ConnectionStatus.disconnected);
  }

  void send(Map<String, dynamic> data) {
    _service?.send(data);
  }

  void _handleMessage(proto.WsMessage msg) {
    // Handled by individual providers via ref.read
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
  final String? toolName;
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
    this.toolName,
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
        toolName: toolName,
        toolDetail: toolDetail,
        toolResult: toolResult ?? this.toolResult,
        isToolError: isToolError ?? this.isToolError,
        time: time,
      );
}

final chatProvider = StateNotifierProvider<ChatNotifier, List<ChatMessage>>(
  (ref) => ChatNotifier(ref),
);

class ChatNotifier extends StateNotifier<List<ChatMessage>> {
  final Ref _ref;
  int _msgCounter = 0;

  ChatNotifier(this._ref) : super([]);

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
    _ref.read(connectionProvider.notifier).send({
      'type': 'message',
      'data': {'text': text},
    });
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

  void handleToolCall(proto.ToolCallData data) {
    state = [
      ...state,
      ChatMessage(
        id: 'tool-${_msgCounter++}',
        toolName: data.toolName,
        toolDetail: data.detail,
        text: '${data.toolName}(${data.detail})',
        time: DateTime.now(),
      ),
    ];
  }

  void handleToolResult(proto.ToolResultData data) {
    // Find the last tool_call with this name and append result
    final idx = state.lastIndexWhere((m) => m.toolName == data.toolName && m.toolResult == null);
    if (idx >= 0) {
      final msg = state[idx];
      state = [
        for (int i = 0; i < state.length; i++)
          if (i == idx) msg.copyWith(toolResult: data.result, isToolError: data.isError) else state[i],
      ];
    }
  }

  SubagentInfo? _findSubagent(String id) {
    return _ref.read(subagentProvider)[id];
  }
}

// ---- Agent Status Provider ----

final agentStatusProvider = StateProvider<String>((ref) => 'idle');
final agentStatusMessageProvider = StateProvider<String>((ref) => '');

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
}

final subagentProvider = StateProvider<Map<String, SubagentInfo>>((ref) => {});

// ---- Approval Provider ----

class ApprovalInfo {
  final String id;
  final String toolName;
  final String input;

  ApprovalInfo({required this.id, required this.toolName, required this.input});
}

final approvalProvider = StateProvider<ApprovalInfo?>((ref) => null);

// ---- Ask User Provider ----

class AskUserInfo {
  final String id;
  final String title;
  final List<proto.AskUserQuestion> questions;

  AskUserInfo({required this.id, required this.title, required this.questions});
}

final askUserProvider = StateProvider<AskUserInfo?>((ref) => null);

// ---- Session Info Provider ----

final sessionInfoProvider = StateProvider<proto.SessionInfoData?>((ref) => null);

// ---- Current mode provider ----

final currentModeProvider = StateProvider<String>((ref) => 'supervised');

// ---- Message Dispatcher ----

final messageDispatcherProvider = Provider<Function>((ref) {
  return (proto.WsMessage msg) {
    final chatNotifier = ref.read(chatProvider.notifier);

    switch (msg.type) {
      case 'session_info':
        final data = proto.SessionInfoData.fromJson(msg.data!);
        ref.read(sessionInfoProvider.notifier).state = data;
        ref.read(currentModeProvider.notifier).state = data.mode;
        break;

      case 'text':
        final data = proto.TextData.fromJson(msg.data!);
        chatNotifier.handleTextChunk(data);
        break;

      case 'status':
        final data = proto.StatusData.fromJson(msg.data!);
        ref.read(agentStatusProvider.notifier).state = data.status;
        ref.read(agentStatusMessageProvider.notifier).state = data.message;
        break;

      case 'tool_call':
        final data = proto.ToolCallData.fromJson(msg.data!);
        chatNotifier.handleToolCall(data);
        break;

      case 'tool_result':
        final data = proto.ToolResultData.fromJson(msg.data!);
        chatNotifier.handleToolResult(data);
        break;

      case 'approval_request':
        final data = proto.ApprovalRequestData.fromJson(msg.data!);
        ref.read(approvalProvider.notifier).state =
            ApprovalInfo(id: data.id, toolName: data.toolName, input: data.input);
        break;

      case 'ask_user_request':
        final data = proto.AskUserRequestData.fromJson(msg.data!);
        ref.read(askUserProvider.notifier).state =
            AskUserInfo(id: data.id, title: data.title, questions: data.questions);
        break;

      case 'subagent_spawn':
        final data = proto.SubagentSpawnData.fromJson(msg.data!);
        final agents = Map<String, SubagentInfo>.from(ref.read(subagentProvider));
        agents[data.agentId] = SubagentInfo(
          agentId: data.agentId,
          name: data.name,
          task: data.task,
          color: data.color,
          parentId: data.parentId,
        );
        ref.read(subagentProvider.notifier).state = agents;
        break;

      case 'subagent_text':
        final data = proto.SubagentTextData.fromJson(msg.data!);
        chatNotifier.handleSubagentText(data);
        break;

      case 'subagent_status':
        final data = proto.SubagentStatusData.fromJson(msg.data!);
        final agents = Map<String, SubagentInfo>.from(ref.read(subagentProvider));
        if (agents.containsKey(data.agentId)) {
          agents[data.agentId] = agents[data.agentId]!.copyWith(status: data.status);
          ref.read(subagentProvider.notifier).state = agents;
        }
        break;

      case 'subagent_complete':
        final data = proto.SubagentCompleteData.fromJson(msg.data!);
        final agents = Map<String, SubagentInfo>.from(ref.read(subagentProvider));
        if (agents.containsKey(data.agentId)) {
          agents[data.agentId] = agents[data.agentId]!.copyWith(
            status: 'completed',
            completed: true,
            success: data.success,
            summary: data.summary,
          );
          ref.read(subagentProvider.notifier).state = agents;
        }
        // Auto-dismiss after 3 seconds
        Future.delayed(const Duration(seconds: 3), () {
          final current = Map<String, SubagentInfo>.from(ref.read(subagentProvider));
          current.remove(data.agentId);
          ref.read(subagentProvider.notifier).state = current;
        });
        break;

      case 'error':
        final data = proto.ErrorData.fromJson(msg.data!);
        // Show error in chat
        chatNotifier.addUserMessage(''); // trigger UI update
        break;
    }
  };
});
