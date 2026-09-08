import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/protocol.dart' as proto;

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

/// Which completed sub-agent cards the user has tapped open (#1874 case 1).
/// Lives in the provider layer (not widget state) so the connection
/// provider's 5s cleanup can see it and defer removal while the user is
/// still reading the card - previously a card (and its whole message
/// stream) vanished ~100ms after the user opened it.
class _SubagentExpandedNotifier extends Notifier<Set<String>> {
  @override
  Set<String> build() => {};

  void toggle(String agentId) {
    final next = <String>{...state};
    if (!next.remove(agentId)) {
      next.add(agentId);
    }
    state = next;
  }

  void remove(String agentId) {
    if (!state.contains(agentId)) return;
    state = <String>{...state}..remove(agentId);
  }
}

final subagentExpandedProvider =
    NotifierProvider<_SubagentExpandedNotifier, Set<String>>(
  _SubagentExpandedNotifier.new,
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

String newAskUserMessageId() =>
    'askuser-${DateTime.now().millisecondsSinceEpoch}';

String describeAskUserQuestion(proto.AskUserQuestion q) {
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

String summarizeAskUserResponse(
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

