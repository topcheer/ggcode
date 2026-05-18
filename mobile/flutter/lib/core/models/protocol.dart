import 'dart:convert';

/// Base class for all WebSocket messages
class WsMessage {
  final String type;
  final Map<String, dynamic>? data;

  WsMessage({required this.type, this.data});

  String toJson() => jsonEncode({'type': type, 'data': data});

  factory WsMessage.fromString(String raw) {
    final decoded = jsonDecode(raw) as Map<String, dynamic>;
    return WsMessage(
      type: decoded['type'] as String,
      data: decoded['data'] as Map<String, dynamic>?,
    );
  }
}

// ---- Client → Server commands ----

class ClientMessage {
  static WsMessage chat(String text) =>
      WsMessage(type: 'message', data: {'text': text});

  static WsMessage approvalResponse(String id, String decision) =>
      WsMessage(type: 'approval_response', data: {
        'id': id,
        'decision': decision,
      });

  static WsMessage interrupt() =>
      WsMessage(type: 'interrupt');

  static WsMessage modeChange(String mode) =>
      WsMessage(type: 'mode_change', data: {'mode': mode});

  static WsMessage pong() =>
      WsMessage(type: 'pong');
}

// ---- Server → Client events ----

class SessionInfo {
  final String workspace;
  final String model;
  final String provider;
  final String mode;
  final String version;

  SessionInfo({
    required this.workspace,
    required this.model,
    required this.provider,
    required this.mode,
    required this.version,
  });

  factory SessionInfo.fromData(Map<String, dynamic> d) => SessionInfo(
        workspace: d['workspace'] as String? ?? '',
        model: d['model'] as String? ?? '',
        provider: d['provider'] as String? ?? '',
        mode: d['mode'] as String? ?? '',
        version: d['version'] as String? ?? '',
      );
}

class TextChunk {
  final String id;
  final String chunk;
  final bool done;

  TextChunk({required this.id, required this.chunk, required this.done});

  factory TextChunk.fromData(Map<String, dynamic> d) => TextChunk(
        id: d['id'] as String? ?? '',
        chunk: d['chunk'] as String? ?? '',
        done: d['done'] as bool? ?? false,
      );
}

class AgentStatus {
  final String status;
  final String message;

  AgentStatus({required this.status, required this.message});

  factory AgentStatus.fromData(Map<String, dynamic> d) => AgentStatus(
        status: d['status'] as String? ?? 'idle',
        message: d['message'] as String? ?? '',
      );
}

class ToolCall {
  final String toolName;
  final String args;
  final String detail;

  ToolCall({required this.toolName, required this.args, required this.detail});

  factory ToolCall.fromData(Map<String, dynamic> d) => ToolCall(
        toolName: d['tool_name'] as String? ?? '',
        args: d['args'] as String? ?? '',
        detail: d['detail'] as String? ?? '',
      );
}

class ToolResult {
  final String toolName;
  final String result;
  final bool isError;

  ToolResult(
      {required this.toolName, required this.result, required this.isError});

  factory ToolResult.fromData(Map<String, dynamic> d) => ToolResult(
        toolName: d['tool_name'] as String? ?? '',
        result: d['result'] as String? ?? '',
        isError: d['is_error'] as bool? ?? false,
      );
}

class ApprovalRequest {
  final String id;
  final String toolName;
  final String input;

  ApprovalRequest(
      {required this.id, required this.toolName, required this.input});

  factory ApprovalRequest.fromData(Map<String, dynamic> d) => ApprovalRequest(
        id: d['id'] as String? ?? '',
        toolName: d['tool_name'] as String? ?? '',
        input: d['input'] as String? ?? '',
      );
}

class ErrorEvent {
  final String message;
  final String code;

  ErrorEvent({required this.message, required this.code});

  factory ErrorEvent.fromData(Map<String, dynamic> d) => ErrorEvent(
        message: d['message'] as String? ?? '',
        code: d['code'] as String? ?? '',
      );
}
