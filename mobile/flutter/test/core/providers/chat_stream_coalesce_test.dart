import 'package:fake_async/fake_async.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/models/protocol.dart' as proto;
import 'package:ggcode_mobile/core/providers/chat_provider.dart';

// M3 (mobile review 2026-11-27): handleTextChunk used to write state once per
// network chunk. Chunks for an existing message are now coalesced for one
// frame and applied in a single state write; a done frame flushes the tail.

ChatNotifier _notifier(ProviderContainer container) =>
    container.read(chatProvider.notifier);

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('50 chunks for one message produce one coalesced state write', () {
    fakeAsync((async) {
      final container = ProviderContainer();
      addTearDown(container.dispose);
      final notifier = _notifier(container);
      var writes = 0;
      container.listen(chatProvider, (_, __) => writes++);

      notifier.handleTextChunk(
          proto.TextData(id: 'm1', chunk: 'a', done: false)); // creates row
      for (var i = 0; i < 50; i++) {
        notifier.handleTextChunk(
            proto.TextData(id: 'm1', chunk: 'x', done: false));
      }
      // Nothing applied yet beyond the row creation.
      expect(container.read(chatProvider).single.text, 'a');
      async.elapse(const Duration(milliseconds: 16));
      final msg = container.read(chatProvider).single;
      expect(msg.text, 'a' + 'x' * 50);
      expect(msg.streaming, true);
      // row creation + one coalesced flush (the listener fires on each write)
      expect(writes, 2);
    });
  });

  test('done frame flushes the buffered tail and closes the stream', () {
    fakeAsync((async) {
      final container = ProviderContainer();
      addTearDown(container.dispose);
      final notifier = _notifier(container);
      notifier.handleTextChunk(
          proto.TextData(id: 'm2', chunk: 'hel', done: false));
      notifier.handleTextChunk(
          proto.TextData(id: 'm2', chunk: 'lo', done: false));
      notifier.handleTextChunk(
          proto.TextData(id: 'm2', chunk: '', done: true));
      final msg = container.read(chatProvider).single;
      expect(msg.text, 'hello');
      expect(msg.streaming, false);
      async.elapse(const Duration(milliseconds: 16));
      // No late flush re-marks the message as streaming.
      expect(container.read(chatProvider).single.streaming, false);
    });
  });

  test('empty chunk on an unknown id still creates nothing', () {
    fakeAsync((async) {
      final container = ProviderContainer();
      addTearDown(container.dispose);
      _notifier(container).handleTextChunk(
          proto.TextData(id: 'm3', chunk: '', done: false));
      expect(container.read(chatProvider), isEmpty);
    });
  });
}
