import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/models/protocol.dart' as proto;
import '../../core/providers/session_provider.dart';

/// Full-screen questionnaire page for ask_user requests.
class AskUserScreen extends ConsumerStatefulWidget {
  const AskUserScreen({super.key});

  @override
  ConsumerState<AskUserScreen> createState() => _AskUserScreenState();
}

class _AskUserScreenState extends ConsumerState<AskUserScreen> {
  final Map<String, List<String>> _selectedChoices = {};
  final Map<String, TextEditingController> _freeformControllers = {};

  @override
  void dispose() {
    for (final ctrl in _freeformControllers.values) {
      ctrl.dispose();
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final keyboardInset = MediaQuery.viewInsetsOf(context).bottom;
    final maxHeight = MediaQuery.sizeOf(context).height * 0.85;
    ref.listen<AskUserInfo?>(askUserProvider, (prev, next) {
      if (prev != null &&
          next == null &&
          mounted &&
          Navigator.of(context).canPop()) {
        Navigator.of(context).pop();
      }
    });
    final askUser = ref.watch(askUserProvider);
    if (askUser == null) {
      return const SizedBox.shrink();
    }

    return AnimatedPadding(
      key: const Key('askUserKeyboardPadding'),
      duration: const Duration(milliseconds: 180),
      curve: Curves.easeOut,
      padding: EdgeInsets.only(bottom: keyboardInset),
      child: ConstrainedBox(
        constraints: BoxConstraints(maxHeight: maxHeight),
        child: Container(
          decoration: const BoxDecoration(
            color: Color(0xFF0D0D14),
            borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
          ),
          child: SafeArea(
            top: false,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                // Handle bar
                Container(
                  margin: const EdgeInsets.symmetric(vertical: 8),
                  width: 40,
                  height: 4,
                  decoration: BoxDecoration(
                    color: Colors.white24,
                    borderRadius: BorderRadius.circular(2),
                  ),
                ),
                // Title
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
                  child: Row(
                    children: [
                      const Icon(Icons.help_outline,
                          color: Colors.blueAccent, size: 20),
                      const SizedBox(width: 8),
                      Expanded(
                        child: Text(
                          askUser.title,
                          style: const TextStyle(
                            color: Colors.white,
                            fontSize: 17,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                      IconButton(
                        icon: const Icon(Icons.close, color: Colors.white54),
                        onPressed: () => _cancel(askUser.id),
                      ),
                    ],
                  ),
                ),
                const Divider(color: Colors.white12, height: 1),
                // Questions
                Flexible(
                  child: ListView.builder(
                    shrinkWrap: true,
                    padding: const EdgeInsets.symmetric(vertical: 12),
                    itemCount: askUser.questions.length,
                    itemBuilder: (context, index) {
                      return _buildQuestion(askUser.questions[index], index);
                    },
                  ),
                ),
                const Divider(color: Colors.white12, height: 1),
                // Submit button
                Padding(
                  padding: const EdgeInsets.all(16),
                  child: SizedBox(
                    width: double.infinity,
                    height: 48,
                    child: ElevatedButton(
                      style: ElevatedButton.styleFrom(
                        backgroundColor: Colors.blueAccent,
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(12),
                        ),
                      ),
                      onPressed: () => _submit(askUser),
                      child: const Text(
                        'Submit',
                        style: TextStyle(
                            color: Colors.white,
                            fontSize: 16,
                            fontWeight: FontWeight.w600),
                      ),
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildQuestion(proto.AskUserQuestion question, int index) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Question prompt
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                '${index + 1}. ',
                style: const TextStyle(
                    color: Colors.blueAccent, fontWeight: FontWeight.w600),
              ),
              Expanded(
                child: Text(
                  question.prompt,
                  style: const TextStyle(color: Colors.white, fontSize: 15),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),

          // Single choice
          if (question.kind == 'single')
            ...question.choices.map((choice) => _buildChoiceTile(
                  question.id,
                  choice,
                  isSelected:
                      (_selectedChoices[question.id]?.length ?? 0) == 1 &&
                          _selectedChoices[question.id]!.first == choice.id,
                  multi: false,
                )),

          // Multi choice
          if (question.kind == 'multi')
            ...question.choices.map((choice) => _buildChoiceTile(
                  question.id,
                  choice,
                  isSelected:
                      _selectedChoices[question.id]?.contains(choice.id) ??
                          false,
                  multi: true,
                )),

          // Freeform
          if (question.kind == 'text' || question.allowFreeform) ...[
            const SizedBox(height: 4),
            TextField(
              controller: _freeformControllers.putIfAbsent(
                  question.id, () => TextEditingController()),
              style: const TextStyle(color: Colors.white, fontSize: 14),
              decoration: InputDecoration(
                hintText: question.placeholder.isNotEmpty
                    ? question.placeholder
                    : 'Type your answer...',
                hintStyle:
                    TextStyle(color: Colors.white.withValues(alpha: 0.3)),
                filled: true,
                fillColor: const Color(0xFF1A1A2E),
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(8),
                  borderSide: BorderSide.none,
                ),
                contentPadding:
                    const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
              ),
              maxLines: question.kind == 'text' ? 3 : 1,
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildChoiceTile(String questionId, proto.AskUserChoice choice,
      {required bool isSelected, required bool multi}) {
    return InkWell(
      onTap: () {
        setState(() {
          _selectedChoices.putIfAbsent(questionId, () => []);
          if (multi) {
            if (_selectedChoices[questionId]!.contains(choice.id)) {
              _selectedChoices[questionId]!.remove(choice.id);
            } else {
              _selectedChoices[questionId]!.add(choice.id);
            }
          } else {
            _selectedChoices[questionId] = [choice.id];
          }
        });
      },
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 4),
        child: Row(
          children: [
            Icon(
              multi
                  ? (isSelected
                      ? Icons.check_box
                      : Icons.check_box_outline_blank)
                  : (isSelected
                      ? Icons.radio_button_checked
                      : Icons.radio_button_unchecked),
              color: isSelected ? Colors.blueAccent : Colors.white38,
              size: 20,
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                choice.label,
                style: TextStyle(
                  color: isSelected ? Colors.white : Colors.white70,
                  fontSize: 14,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  void _submit(AskUserInfo askUser) {
    final answers = askUser.questions.map((q) {
      return proto.AskUserAnswer(
        questionId: q.id,
        choiceIds: _selectedChoices[q.id] ?? [],
        freeformText: _freeformControllers[q.id]?.text ?? '',
      );
    }).toList();

    // Build human-readable answer summary for chat
    final parts = <String>[];
    for (int i = 0; i < askUser.questions.length; i++) {
      final q = askUser.questions[i];
      final a = answers[i];
      String answer;
      if (a.choiceIds.isNotEmpty) {
        final labels = q.choices
            .where((c) => a.choiceIds.contains(c.id))
            .map((c) => c.label)
            .toList();
        answer = labels.join(', ');
      } else {
        answer = a.freeformText;
      }
      if (answer.isNotEmpty) parts.add(answer);
    }

    ref.read(connectionProvider.notifier).send({
      'type': 'ask_user_response',
      'data': {
        'id': askUser.id,
        'status': 'submitted',
        'answers': answers.map((a) => a.toJson()).toList(),
      },
    });

    // Update the ask_user chat message with the answer
    if (askUser.msgId.isNotEmpty) {
      ref.read(chatProvider.notifier).updateAskUserAnswer(
            askUser.msgId,
            parts.join(' / '),
          );
    }

    ref.read(askUserProvider.notifier).set(null);
  }

  void _cancel(String id) {
    ref.read(connectionProvider.notifier).send({
      'type': 'ask_user_response',
      'data': {'id': id, 'status': 'cancelled', 'answers': []},
    });

    // Update the ask_user chat message with cancelled status
    final askUser = ref.read(askUserProvider);
    if (askUser != null && askUser.msgId.isNotEmpty) {
      ref.read(chatProvider.notifier).updateAskUserAnswer(
            askUser.msgId,
            'Cancelled',
          );
    }

    ref.read(askUserProvider.notifier).set(null);
  }
}
