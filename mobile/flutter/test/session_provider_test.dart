import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:ggcode_mobile/core/models/protocol.dart' as proto;
import 'package:ggcode_mobile/core/providers/session_provider.dart';

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

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
        displayName: 'Inspect file',
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
    expect(message.toolDisplayName, 'Inspect file');
    expect(message.toolResult, 'done');
  });

  test('ChatNotifier absorbs matching remote user echo', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(chatProvider.notifier);
    notifier.addUserMessage('hello from mobile');

    final absorbed = notifier.bindRemoteUserMessage(
      'hello from mobile',
      remoteMessageId: 'ev-0001',
    );

    expect(absorbed, isTrue);
    final messages = container.read(chatProvider);
    expect(messages, hasLength(1));
    expect(messages.single.id, 'ev-0001');
    expect(messages.single.text, 'hello from mobile');
  });

  test('ChatNotifier can render cron trigger as a non-user message', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(chatProvider.notifier);
    notifier.addRemoteSystemMessage(
      '⏰ Cron job triggered',
      messageId: 'ev-cron',
    );

    final messages = container.read(chatProvider);
    expect(messages, hasLength(1));
    expect(messages.single.isUser, isFalse);
    expect(messages.single.text, '⏰ Cron job triggered');
  });

  test('workspace cache persists snapshots and restores selected session',
      () async {
    final info = proto.SessionInfoData(
      workspace: '/tmp/demo',
      model: 'gpt-5.4',
      provider: 'openai',
      mode: 'supervised',
      version: '1.0.0',
    );

    final container = ProviderContainer();
    addTearDown(container.dispose);

    final cache = container.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=abc');
    await cache.registerLiveSession('sess-1', info, lastEventId: 'ev-0001');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'msg-1',
          text: 'hello',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'running',
      agentStatusMessage: 'read_file',
      lastEventId: 'ev-0001',
    );
    await Future<void>.delayed(const Duration(milliseconds: 450));

    final restored = ProviderContainer();
    addTearDown(restored.dispose);
    final restoredCache = restored.read(workspaceCacheProvider.notifier);
    await restoredCache.initialize();

    final restoredState = restored.read(workspaceCacheProvider);
    expect(restoredState.selectedWorkspaceKey, isNotNull);
    expect(restoredState.selectedSessionId, 'sess-1');
    final snapshot = restoredCache.snapshotFor(
      restoredState.selectedWorkspaceKey!,
      'sess-1',
    );
    expect(snapshot, isNotNull);
    expect(snapshot!.messages.single.text, 'hello');
    expect(snapshot.agentStatus, 'running');
    expect(snapshot.agentStatusMessage, 'read_file');
  });

  test('historical session view uses cached messages and disables sending',
      () async {
    final info = proto.SessionInfoData(
      workspace: '/tmp/demo',
      model: 'gpt-5.4',
      provider: 'openai',
      mode: 'supervised',
      version: '1.0.0',
    );

    final container = ProviderContainer();
    addTearDown(container.dispose);
    final cache = container.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=abc');

    await cache.registerLiveSession('sess-old', info, lastEventId: 'ev-0001');
    container.read(chatProvider.notifier).set([
      ChatMessage(
        id: 'old-msg',
        text: 'old session',
        time: DateTime.parse('2026-01-01T00:00:00Z'),
      ),
    ]);
    await cache.captureLiveProjection(
      messages: container.read(chatProvider),
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-0001',
    );

    await cache.registerLiveSession('sess-live', info, lastEventId: 'ev-0002');
    container.read(chatProvider.notifier).set([
      ChatMessage(
        id: 'live-msg',
        text: 'live session',
        time: DateTime.parse('2026-01-02T00:00:00Z'),
      ),
    ]);
    await cache.captureLiveProjection(
      messages: container.read(chatProvider),
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'running',
      agentStatusMessage: 'processing',
      lastEventId: 'ev-0002',
    );

    final workspaceKey =
        container.read(workspaceCacheProvider).selectedWorkspaceKey!;
    await cache.selectSession(workspaceKey, 'sess-old');

    expect(container.read(isHistoricalViewProvider), isTrue);
    expect(container.read(canSendMessagesProvider), isFalse);
    expect(
        container.read(displayedMessagesProvider).single.text, 'old session');
  });

  test('displayed agent status uses cached snapshot for historical session',
      () async {
    final info = proto.SessionInfoData(
      workspace: '/tmp/demo',
      model: 'gpt-5.4',
      provider: 'openai',
      mode: 'supervised',
      version: '1.0.0',
    );

    final container = ProviderContainer();
    addTearDown(container.dispose);
    final cache = container.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=abc');
    await cache.registerLiveSession('sess-1', info, lastEventId: 'ev-0001');
    await cache.captureLiveProjection(
      messages: const [],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'running',
      agentStatusMessage: 'read_file',
      lastEventId: 'ev-0001',
    );
    cache.markDisconnected();

    expect(container.read(displayedAgentStatusProvider), 'running');
    expect(container.read(displayedAgentStatusMessageProvider), 'read_file');
  });
}
