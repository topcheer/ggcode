import 'dart:convert';

/// Base class for all WebSocket messages
class WsMessage {
  final String? sessionId;
  final String? eventId;
  final String? streamId;
  final String? messageId;
  final int? generation;
  final String type;
  final Map<String, dynamic>? data;

  WsMessage({
    this.sessionId,
    this.eventId,
    this.streamId,
    this.messageId,
    this.generation,
    required this.type,
    this.data,
  });

  String toJson() => jsonEncode({
        'session_id': sessionId,
        'event_id': eventId,
        'stream_id': streamId,
        'message_id': messageId,
        'generation': generation,
        'type': type,
        'data': data,
      });

  static WsMessage fromJson(String jsonStr) {
    final map = jsonDecode(jsonStr) as Map<String, dynamic>;
    return WsMessage(
      sessionId: map['session_id'] as String?,
      eventId: map['event_id'] as String?,
      streamId: map['stream_id'] as String?,
      messageId: map['message_id'] as String?,
      generation: (map['generation'] as num?)?.toInt(),
      type: map['type'] as String,
      data: map['data'] as Map<String, dynamic>?,
    );
  }
}

/// ─── Session Info ───

class SessionInfoData {
  final String workspace;
  final String model;
  final String provider;
  final String mode;
  final String version;
  final String language;
  final String theme;

  SessionInfoData({
    required this.workspace,
    required this.model,
    required this.provider,
    required this.mode,
    required this.version,
    this.language = '',
    this.theme = '',
  });

  factory SessionInfoData.fromJson(Map<String, dynamic> d) => SessionInfoData(
        workspace: d['workspace'] as String? ?? '',
        model: d['model'] as String? ?? '',
        provider: d['provider'] as String? ?? '',
        mode: d['mode'] as String? ?? '',
        version: d['version'] as String? ?? '',
        language: d['language'] as String? ?? '',
        theme: d['theme'] as String? ?? '',
      );
}

/// ─── Text Streaming ───

class TextData {
  final String id;
  final String chunk;
  final bool done;
  final String kind;

  TextData(
      {required this.id,
      required this.chunk,
      required this.done,
      this.kind = ''});

  factory TextData.fromJson(Map<String, dynamic> d) => TextData(
        id: d['id'] as String? ?? '',
        chunk: d['chunk'] as String? ?? '',
        done: d['done'] as bool? ?? false,
        kind: d['kind'] as String? ?? '',
      );
}

/// ─── Status ───

class StatusData {
  final String status;
  final String message;

  StatusData({required this.status, required this.message});

  factory StatusData.fromJson(Map<String, dynamic> d) => StatusData(
        status: d['status'] as String? ?? '',
        message: d['message'] as String? ?? '',
      );
}

class ActivityData {
  final String activity;

  ActivityData({required this.activity});

  factory ActivityData.fromJson(Map<String, dynamic> d) => ActivityData(
        activity: d['activity'] as String? ?? '',
      );
}

class MessageData {
  final String text;
  final String displayText;
  final String kind;
  final String messageId;

  MessageData({
    required this.text,
    this.displayText = '',
    this.kind = '',
    this.messageId = '',
  });

  Map<String, dynamic> toJson() => {
        'text': text,
        'display_text': displayText,
        'kind': kind,
        'message_id': messageId,
      };

  factory MessageData.fromJson(Map<String, dynamic> d) => MessageData(
        text: d['text'] as String? ?? '',
        displayText: d['display_text'] as String? ?? '',
        kind: d['kind'] as String? ?? '',
        messageId: d['message_id'] as String? ?? '',
      );
}

/// ─── Tool Call / Result ───

class ToolCallData {
  final String toolId;
  final String toolName;
  final String displayName;
  final String args;
  final String detail;

  ToolCallData(
      {required this.toolId,
      required this.toolName,
      this.displayName = '',
      required this.args,
      required this.detail});

  factory ToolCallData.fromJson(Map<String, dynamic> d) => ToolCallData(
        toolId: d['tool_id'] as String? ?? '',
        toolName: d['tool_name'] as String? ?? '',
        displayName: d['display_name'] as String? ?? '',
        args: d['args'] as String? ?? '',
        detail: d['detail'] as String? ?? '',
      );
}

class ToolResultData {
  final String toolId;
  final String toolName;
  final String result;
  final String summary;
  final String payload;
  final String payloadMode;
  final bool isError;

  ToolResultData(
      {required this.toolId,
      required this.toolName,
      required this.result,
      this.summary = '',
      this.payload = '',
      this.payloadMode = '',
      required this.isError});

  factory ToolResultData.fromJson(Map<String, dynamic> d) => ToolResultData(
        toolId: d['tool_id'] as String? ?? '',
        toolName: d['tool_name'] as String? ?? '',
        result: d['result'] as String? ?? '',
        summary: d['summary'] as String? ?? '',
        payload: d['payload'] as String? ?? '',
        payloadMode: d['payload_mode'] as String? ?? '',
        isError: d['is_error'] as bool? ?? false,
      );
}

/// ─── Approval ───

class ApprovalRequestData {
  final String id;
  final String toolName;
  final String input;

  ApprovalRequestData(
      {required this.id, required this.toolName, required this.input});

  factory ApprovalRequestData.fromJson(Map<String, dynamic> d) =>
      ApprovalRequestData(
        id: d['id'] as String? ?? '',
        toolName: d['tool_name'] as String? ?? '',
        input: d['input'] as String? ?? '',
      );
}

class ApprovalResultData {
  final String id;
  final String decision;

  ApprovalResultData({required this.id, required this.decision});

  factory ApprovalResultData.fromJson(Map<String, dynamic> d) =>
      ApprovalResultData(
        id: d['id'] as String? ?? '',
        decision: d['decision'] as String? ?? '',
      );
}

/// ─── Ask User (Structured Questionnaire) ───

class AskUserQuestion {
  final String id;
  final String prompt;
  final String kind; // single/multi/text
  final List<AskUserChoice> choices;
  final bool allowFreeform;
  final String placeholder;

  AskUserQuestion({
    required this.id,
    required this.prompt,
    required this.kind,
    this.choices = const [],
    this.allowFreeform = false,
    this.placeholder = '',
  });

  factory AskUserQuestion.fromJson(Map<String, dynamic> d) => AskUserQuestion(
        id: d['id'] as String? ?? '',
        prompt: d['prompt'] as String? ?? '',
        kind: d['kind'] as String? ?? 'text',
        choices: (d['choices'] as List<dynamic>?)
                ?.map((c) => AskUserChoice.fromJson(c as Map<String, dynamic>))
                .toList() ??
            [],
        allowFreeform: d['allow_freeform'] as bool? ?? false,
        placeholder: d['placeholder'] as String? ?? '',
      );
}

class AskUserChoice {
  final String id;
  final String label;

  AskUserChoice({required this.id, required this.label});

  factory AskUserChoice.fromJson(Map<String, dynamic> d) => AskUserChoice(
        id: d['id'] as String? ?? '',
        label: d['label'] as String? ?? '',
      );
}

class AskUserRequestData {
  final String id;
  final String title;
  final List<AskUserQuestion> questions;

  AskUserRequestData({
    required this.id,
    required this.title,
    required this.questions,
  });

  factory AskUserRequestData.fromJson(Map<String, dynamic> d) =>
      AskUserRequestData(
        id: d['id'] as String? ?? '',
        title: d['title'] as String? ?? '',
        questions: (d['questions'] as List<dynamic>?)
                ?.map(
                    (q) => AskUserQuestion.fromJson(q as Map<String, dynamic>))
                .toList() ??
            [],
      );
}

class AskUserAnswer {
  final String questionId;
  final List<String> choiceIds;
  final String freeformText;

  AskUserAnswer({
    required this.questionId,
    this.choiceIds = const [],
    this.freeformText = '',
  });

  Map<String, dynamic> toJson() => {
        'question_id': questionId,
        'choice_ids': choiceIds,
        'freeform_text': freeformText,
      };

  factory AskUserAnswer.fromJson(Map<String, dynamic> d) => AskUserAnswer(
        questionId: d['question_id'] as String? ?? '',
        choiceIds: (d['choice_ids'] as List<dynamic>?)
                ?.map((e) => e.toString())
                .toList() ??
            const [],
        freeformText: d['freeform_text'] as String? ?? '',
      );
}

class AskUserResponseData {
  final String id;
  final String status;
  final List<AskUserAnswer> answers;

  AskUserResponseData({
    required this.id,
    required this.status,
    required this.answers,
  });

  factory AskUserResponseData.fromJson(Map<String, dynamic> d) =>
      AskUserResponseData(
        id: d['id'] as String? ?? '',
        status: d['status'] as String? ?? '',
        answers: (d['answers'] as List<dynamic>?)
                ?.map((a) => AskUserAnswer.fromJson(a as Map<String, dynamic>))
                .toList() ??
            const [],
      );
}

/// ─── Sub-agent / Teammate ───

class SubagentSpawnData {
  final String agentId;
  final String name;
  final String task;
  final String color;
  final String parentId;

  SubagentSpawnData({
    required this.agentId,
    required this.name,
    required this.task,
    this.color = '#4CAF50',
    this.parentId = '',
  });

  factory SubagentSpawnData.fromJson(Map<String, dynamic> d) =>
      SubagentSpawnData(
        agentId: d['agent_id'] as String? ?? '',
        name: d['name'] as String? ?? '',
        task: d['task'] as String? ?? '',
        color: d['color'] as String? ?? '#4CAF50',
        parentId: d['parent_id'] as String? ?? '',
      );
}

class SubagentTextData {
  final String agentId;
  final String id;
  final String chunk;
  final bool done;

  SubagentTextData({
    required this.agentId,
    required this.id,
    required this.chunk,
    required this.done,
  });

  factory SubagentTextData.fromJson(Map<String, dynamic> d) => SubagentTextData(
        agentId: d['agent_id'] as String? ?? '',
        id: d['id'] as String? ?? '',
        chunk: d['chunk'] as String? ?? '',
        done: d['done'] as bool? ?? false,
      );
}

class SubagentReasoningData {
  final String agentId;
  final String id;
  final String chunk;
  final bool done;

  SubagentReasoningData({
    required this.agentId,
    required this.id,
    required this.chunk,
    required this.done,
  });

  factory SubagentReasoningData.fromJson(Map<String, dynamic> d) =>
      SubagentReasoningData(
        agentId: d['agent_id'] as String? ?? '',
        id: d['id'] as String? ?? '',
        chunk: d['chunk'] as String? ?? '',
        done: d['done'] as bool? ?? false,
      );
}

class SubagentStatusData {
  final String agentId;
  final String status;
  final String message;

  SubagentStatusData({
    required this.agentId,
    required this.status,
    required this.message,
  });

  factory SubagentStatusData.fromJson(Map<String, dynamic> d) =>
      SubagentStatusData(
        agentId: d['agent_id'] as String? ?? '',
        status: d['status'] as String? ?? '',
        message: d['message'] as String? ?? '',
      );
}

class SubagentCompleteData {
  final String agentId;
  final String name;
  final String summary;
  final bool success;

  SubagentCompleteData({
    required this.agentId,
    required this.name,
    required this.summary,
    required this.success,
  });

  factory SubagentCompleteData.fromJson(Map<String, dynamic> d) =>
      SubagentCompleteData(
        agentId: d['agent_id'] as String? ?? '',
        name: d['name'] as String? ?? '',
        summary: d['summary'] as String? ?? '',
        success: d['success'] as bool? ?? false,
      );
}

class SubagentToolCallData {
  final String agentId;
  final String toolId;
  final String toolName;
  final String displayName;
  final String args;
  final String detail;

  SubagentToolCallData({
    required this.agentId,
    required this.toolId,
    required this.toolName,
    this.displayName = '',
    this.args = '',
    this.detail = '',
  });

  factory SubagentToolCallData.fromJson(Map<String, dynamic> d) =>
      SubagentToolCallData(
        agentId: d['agent_id'] as String? ?? '',
        toolId: d['tool_id'] as String? ?? '',
        toolName: d['tool_name'] as String? ?? '',
        displayName: d['display_name'] as String? ?? '',
        args: d['args'] as String? ?? '',
        detail: d['detail'] as String? ?? '',
      );
}

class SubagentToolResultData {
  final String agentId;
  final String toolId;
  final String toolName;
  final String result;
  final String summary;
  final String payload;
  final String payloadMode;
  final bool isError;

  SubagentToolResultData({
    required this.agentId,
    required this.toolId,
    required this.toolName,
    required this.result,
    this.summary = '',
    this.payload = '',
    this.payloadMode = '',
    this.isError = false,
  });

  factory SubagentToolResultData.fromJson(Map<String, dynamic> d) =>
      SubagentToolResultData(
        agentId: d['agent_id'] as String? ?? '',
        toolId: d['tool_id'] as String? ?? '',
        toolName: d['tool_name'] as String? ?? '',
        result: d['result'] as String? ?? '',
        summary: d['summary'] as String? ?? '',
        payload: d['payload'] as String? ?? '',
        payloadMode: d['payload_mode'] as String? ?? '',
        isError: d['is_error'] as bool? ?? false,
      );
}

class ErrorData {
  final String message;
  final String code;

  ErrorData({required this.message, required this.code});

  factory ErrorData.fromJson(Map<String, dynamic> d) => ErrorData(
        message: d["message"] as String? ?? "",
        code: d["code"] as String? ?? "",
      );
}
