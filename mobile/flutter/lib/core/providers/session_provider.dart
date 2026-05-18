import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:uuid/uuid.dart';

import '../connection_service.dart';
import '../models/protocol.dart' as proto;

// ---- Connection Service Provider ----

final connectionProvider = Provider<ConnectionService>((ref) {
  final service = ConnectionService();
  ref.onDispose(() => service.dispose());
  return service;
});

// ---- Connection Status Provider ----

final connectionStatusProvider = StreamProvider<ConnectionStatus>((ref) {
  final service = ref.watch(connectionProvider);
  return service.statusStream;
});

// ---- Message Stream Provider ----

final messageStreamProvider = StreamProvider<proto.WsMessage>((ref) {
  final service = ref.watch(connectionProvider);
  return service.messageStream;
});

// ---- Session Info Provider ----

final sessionInfoProvider = StateProvider<proto.SessionInfo?>((ref) => null);

// ---- Agent Status Provider ----

final agentStatusProvider = StateProvider<proto.AgentStatus>((ref) =>
    proto.AgentStatus(status: 'idle', message: ''));

// ---- Chat Messages ----

enum MessageType {
  user,
  agent,
  toolCall,
  toolResult,
}

class ChatMessage {
  final String id;
  final MessageType type;
  final String? text;
  final proto.ToolCall? toolCall;
  final proto.ToolResult? toolResult;
  final DateTime timestamp;

  ChatMessage({
    required this.id,
    required this.type,
    this.text,
    this.toolCall,
    this.toolResult,
    DateTime? timestamp,
  }) : timestamp = timestamp ?? DateTime.now();
}

class ChatState {
  final List<ChatMessage> messages;
  final Map<String, String> streamingBuffers; // msgId -> accumulated text

  ChatState({
    required this.messages,
    required this.streamingBuffers,
  });

  factory ChatState.initial() => ChatState(
        messages: [],
        streamingBuffers: {},
      );

  ChatState copyWith({
    List<ChatMessage>? messages,
    Map<String, String>? streamingBuffers,
  }) =>
      ChatState(
        messages: messages ?? this.messages,
        streamingBuffers: streamingBuffers ?? this.streamingBuffers,
      );
}

class ChatNotifier extends StateNotifier<ChatState> {
  ChatNotifier() : super(ChatState.initial());

  static const _uuid = Uuid();

  void addUserMessage(String text) {
    final msg = ChatMessage(
      id: _uuid.v4(),
      type: MessageType.user,
      text: text,
    );
    state = state.copyWith(
      messages: [...state.messages, msg],
    );
  }

  void startAgentMessage(String id) {
    state = state.copyWith(
      streamingBuffers: {...state.streamingBuffers, id: ''},
    );
  }

  void appendTextChunk(String id, String chunk) {
    final buffers = Map<String, String>.from(state.streamingBuffers);
    buffers[id] = (buffers[id] ?? '') + chunk;
    state = state.copyWith(streamingBuffers: buffers);
  }

  void finishAgentMessage(String id) {
    final text = state.streamingBuffers[id] ?? '';
    final buffers = Map<String, String>.from(state.streamingBuffers);
    buffers.remove(id);

    if (text.isNotEmpty) {
      final msg = ChatMessage(
        id: id,
        type: MessageType.agent,
        text: text,
      );
      state = state.copyWith(
        messages: [...state.messages, msg],
        streamingBuffers: buffers,
      );
    } else {
      state = state.copyWith(streamingBuffers: buffers);
    }
  }

  void addToolCall(proto.ToolCall toolCall) {
    final msg = ChatMessage(
      id: _uuid.v4(),
      type: MessageType.toolCall,
      toolCall: toolCall,
    );
    state = state.copyWith(messages: [...state.messages, msg]);
  }

  void addToolResult(proto.ToolResult toolResult) {
    final msg = ChatMessage(
      id: _uuid.v4(),
      type: MessageType.toolResult,
      toolResult: toolResult,
    );
    state = state.copyWith(messages: [...state.messages, msg]);
  }

  void clear() {
    state = ChatState.initial();
  }

  String getStreamingText(String id) => state.streamingBuffers[id] ?? '';
}

final chatProvider = StateNotifierProvider<ChatNotifier, ChatState>((ref) {
  return ChatNotifier();
});

// ---- Approval Request Provider ----

final pendingApprovalProvider =
    StateProvider<proto.ApprovalRequest?>((ref) => null);

// ---- Connection History ----

final connectionHistoryProvider =
    StateNotifierProvider<ConnectionHistoryNotifier, List<String>>((ref) {
  return ConnectionHistoryNotifier();
});

class ConnectionHistoryNotifier extends StateNotifier<List<String>> {
  static const _key = 'ggcode_connection_history';

  ConnectionHistoryNotifier() : super([]) {
    _load();
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    final list = prefs.getStringList(_key) ?? [];
    state = list;
  }

  Future<void> add(String url) async {
    final updated = [url, ...state.where((u) => u != url)];
    if (updated.length > 10) updated.removeRange(10, updated.length);
    state = updated;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setStringList(_key, updated);
  }

  Future<void> remove(String url) async {
    final updated = state.where((u) => u != url).toList();
    state = updated;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setStringList(_key, updated);
  }
}

// ---- Current mode provider ----

final currentModeProvider = StateProvider<String>((ref) => 'supervised');
