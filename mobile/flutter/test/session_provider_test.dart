import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/models/protocol.dart' as proto;
import 'package:ggcode_mobile/core/providers/session_provider.dart';

void main() {
  test('ChatNotifier finalizes only the targeted stream', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(chatProvider.notifier);
    notifier.handleTextChunk(
      proto.TextData(id: 'msg-1', chunk: 'hello', done: false),
    );
    notifier.handleTextChunk(
      proto.TextData(id: 'msg-2', chunk: 'world', done: false),
    );

    notifier.finalizeStreaming('msg-1');

    final messages = container.read(chatProvider);
    final first = messages.firstWhere((m) => m.id == 'msg-1');
    final second = messages.firstWhere((m) => m.id == 'msg-2');
    expect(first.streaming, isFalse);
    expect(second.streaming, isTrue);
  });

  test(
      'ChatNotifier keeps stable tool message ids and binds results by tool id',
      () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(chatProvider.notifier);
    notifier.handleToolCall(
      proto.ToolCallData(
        toolId: 'tool-1',
        toolName: 'read_file',
        args: '{"path":"a"}',
        detail: 'read file',
      ),
      messageId: 'ev-0001',
    );
    notifier.handleToolResult(
      proto.ToolResultData(
        toolId: 'tool-1',
        toolName: 'read_file',
        result: 'done',
        isError: false,
      ),
    );

    final message = container.read(chatProvider).single;
    expect(message.id, 'ev-0001');
    expect(message.toolId, 'tool-1');
    expect(message.toolResult, 'done');
  });
}
