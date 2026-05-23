import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/connection_service.dart';
import 'package:ggcode_mobile/core/crypto.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:ggcode_mobile/core/models/protocol.dart' as proto;
import 'package:ggcode_mobile/core/providers/session_provider.dart';

class _FakeConnectionService extends ConnectionService {
  int replayRequests = 0;
  String? replayClientId;
  String? replaySessionId;
  String? replayLastEventId;

  _FakeConnectionService()
      : super(
          url: 'ws://example.test/ws?token=test-token',
          crypto: TunnelCrypto('test-token'),
        );

  @override
  void requestReplayFrom({
    required String clientId,
    String? sessionId,
    String? lastEventId,
  }) {
    replayRequests++;
    replayClientId = clientId;
    replaySessionId = sessionId;
    replayLastEventId = lastEventId;
  }
}

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

  test('ConnectionNotifier buffers replay gaps until missing events arrive',
      () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(connectionProvider.notifier);
    final fakeService = _FakeConnectionService();
    notifier.service = fakeService;

    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000001',
      type: 'text',
      data: {'id': 'msg-1', 'chunk': '这些都是', 'done': false},
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000003',
      type: 'text',
      data: {'id': 'msg-1', 'chunk': '，要', 'done': false},
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000004',
      type: 'text',
      data: {'id': 'msg-1', 'chunk': '提交吗？', 'done': false},
    ));

    expect(fakeService.replayRequests, 1);
    expect(fakeService.replayLastEventId, 'ev-000000001');
    expect(container.read(chatProvider).single.text, '这些都是');

    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      type: 'resume_ack',
      data: {'resume_mode': 'incremental'},
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000002',
      type: 'text',
      data: {'id': 'msg-1', 'chunk': '有意义的改动', 'done': false},
    ));

    final message = container.read(chatProvider).single;
    expect(message.text, '这些都是有意义的改动，要提交吗？');
  });

  test('ConnectionNotifier accepts a fresh snapshot after a buffered gap', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(connectionProvider.notifier);
    final fakeService = _FakeConnectionService();
    notifier.service = fakeService;

    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-old',
      eventId: 'ev-000000001',
      type: 'text',
      data: {'id': 'old-msg', 'chunk': 'old', 'done': false},
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-old',
      eventId: 'ev-000000003',
      type: 'text',
      data: {'id': 'old-msg', 'chunk': ' gap', 'done': false},
    ));

    expect(fakeService.replayRequests, 1);
    expect(container.read(chatProvider).single.text, 'old');

    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-new',
      type: 'snapshot_reset',
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-new',
      eventId: 'ev-000000001',
      type: 'text',
      data: {'id': 'new-msg', 'chunk': 'fresh snapshot', 'done': false},
    ));

    expect(notifier.currentSessionId, 'sess-new');
    expect(notifier.lastAppliedEventId, 'ev-000000001');
    expect(container.read(chatProvider).single.text, 'fresh snapshot');
  });

  test('ConnectionNotifier follows active session control message', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(connectionProvider.notifier);
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-old',
      eventId: 'ev-000000001',
      type: 'text',
      data: {'id': 'old-msg', 'chunk': 'old session', 'done': false},
    ));

    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-new',
      type: 'active_session',
      data: {'session_id': 'sess-new'},
    ));

    expect(notifier.currentSessionId, 'sess-new');
    expect(notifier.lastAppliedEventId, isEmpty);
    expect(container.read(chatProvider), isEmpty);
  });

  test('ConnectionNotifier restores cached session projection before resume',
      () async {
    final info = proto.SessionInfoData(
      workspace: '/tmp/demo',
      model: 'gpt-5.4',
      provider: 'openai',
      mode: 'bypass',
      version: '1.0.0',
    );

    final container = ProviderContainer();
    addTearDown(container.dispose);

    final cache = container.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=abc');
    await cache.registerLiveSession('sess-122', info,
        lastEventId: 'ev-000000120');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'msg-1',
          text: '已提交并推送。 Commit `20399ef1`，5 files changed，+100/-1。',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
        ChatMessage(
          id: 'msg-2',
          text: '两个设备都已成功重新构建并启动：',
          time: DateTime.parse('2026-01-01T00:01:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000120',
    );

    container.read(chatProvider.notifier).clearMessages();
    container.read(sessionInfoProvider.notifier).set(null);
    container.read(currentModeProvider.notifier).set('supervised');

    final notifier = container.read(connectionProvider.notifier);
    final restored = notifier.restoreProjectionFromCacheForTest();

    expect(restored, isTrue);
    expect(notifier.currentSessionId, 'sess-122');
    expect(notifier.lastAppliedEventId, 'ev-000000120');
    expect(container.read(chatProvider), hasLength(2));
    expect(
      container.read(chatProvider).last.text,
      '两个设备都已成功重新构建并启动：',
    );
    expect(container.read(sessionInfoProvider)?.mode, 'bypass');
    expect(container.read(currentModeProvider), 'bypass');
  });
}
