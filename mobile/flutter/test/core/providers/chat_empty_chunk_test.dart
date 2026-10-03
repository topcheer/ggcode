import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/models/protocol.dart' as proto;
import 'package:ggcode_mobile/core/providers/chat_provider.dart';

// Regression for the blank-bubble stack after a session switch: replaying
// the recorded projection re-delivers text frames whose chunk is EMPTY
// (flushAllTexts used to emit them on the Go side; done-only frames may
// exist in already-recorded history). handleTextChunk used to materialize
// a text:'' message for each one when the id was unknown in the fresh
// state; handleReasoningChunk already guarded ("if (chunk.isEmpty) return;").

ChatNotifier _notifier(ProviderContainer container) =>
    container.read(chatProvider.notifier);

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('empty-chunk text frame on unknown id does not create a bubble', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final notifier = _notifier(container);
    notifier.handleTextChunk(
        proto.TextData(id: 'msg-empty-1', chunk: '', done: false));
    expect(
        container
            .read(chatProvider)
            .where((m) => m.id == 'msg-empty-1'),
        isEmpty);
  });

  test('non-empty chunk still creates the bubble (guard is surgical)', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final notifier = _notifier(container);
    notifier.handleTextChunk(
        proto.TextData(id: 'msg-real', chunk: 'hello', done: false));
    final msgs = container.read(chatProvider);
    expect(msgs.where((m) => m.id == 'msg-real').length, 1);
    expect(msgs.last.text, 'hello');
  });

  test('empty chunk on EXISTING id does not clear text (flag update only)',
      () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final notifier = _notifier(container);
    notifier.handleTextChunk(
        proto.TextData(id: 'msg-live', chunk: 'abc', done: false));
    notifier.handleTextChunk(
        proto.TextData(id: 'msg-live', chunk: '', done: true));
    final m =
        container.read(chatProvider).firstWhere((m) => m.id == 'msg-live');
    expect(m.text, 'abc');
    expect(m.streaming, isFalse);
  });
}
