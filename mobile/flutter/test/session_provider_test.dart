import 'dart:async';
import 'dart:convert';
import 'dart:io';

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

class _CaptureResumeHelloService extends _FakeConnectionService {
  final _status = StreamController<ConnectionStatus>.broadcast();
  final _messages = StreamController<proto.WsMessage>.broadcast();
  int resumeHelloRequests = 0;
  String? resumeClientId;
  String? resumeSessionId;
  String? resumeLastEventId;

  @override
  Stream<ConnectionStatus> get statusStream => _status.stream;

  @override
  Stream<String> get errorStream => const Stream<String>.empty();

  @override
  Stream<proto.WsMessage> get messageStream => _messages.stream;

  @override
  Future<void> connect() async {
    _status.add(ConnectionStatus.connected);
  }

  @override
  void sendResumeHello({
    required String clientId,
    String? sessionId,
    String? lastEventId,
  }) {
    resumeHelloRequests++;
    resumeClientId = clientId;
    resumeSessionId = sessionId;
    resumeLastEventId = lastEventId;
  }

  @override
  void dispose() {
    _status.close();
    _messages.close();
    super.dispose();
  }

  void emit(proto.WsMessage msg) {
    _messages.add(msg);
  }
}

class _PermanentRoomFailureService extends _FakeConnectionService {
  final _status = StreamController<ConnectionStatus>.broadcast();
  final _errors = StreamController<String>.broadcast();
  final _messages = StreamController<proto.WsMessage>.broadcast();

  @override
  Stream<ConnectionStatus> get statusStream => _status.stream;

  @override
  Stream<String> get errorStream => _errors.stream;

  @override
  Stream<proto.WsMessage> get messageStream => _messages.stream;

  @override
  Future<void> connect() async {
    _errors.add('Room not found: stale or expired share token');
    _status.add(ConnectionStatus.disconnected);
  }

  @override
  void dispose() {
    _status.close();
    _errors.close();
    _messages.close();
    super.dispose();
  }
}

class _ServerOfflineService extends _FakeConnectionService {
  final _status = StreamController<ConnectionStatus>.broadcast();
  final _messages = StreamController<proto.WsMessage>.broadcast();

  @override
  Stream<ConnectionStatus> get statusStream => _status.stream;

  @override
  Stream<String> get errorStream => const Stream<String>.empty();

  @override
  Stream<proto.WsMessage> get messageStream => _messages.stream;

  @override
  Future<void> connect() async {
    _status.add(ConnectionStatus.connected);
    _messages.add(proto.WsMessage(
      type: 'server_offline',
      data: {'retry_after_ms': 60000},
    ));
    _status.add(ConnectionStatus.disconnected);
  }

  @override
  void dispose() {
    _status.close();
    _messages.close();
    super.dispose();
  }
}

class _BlockingConnectService extends _FakeConnectionService {
  final _status = StreamController<ConnectionStatus>.broadcast();
  final _messages = StreamController<proto.WsMessage>.broadcast();
  final Completer<void> ready = Completer<void>();
  int connectCalls = 0;

  @override
  Stream<ConnectionStatus> get statusStream => _status.stream;

  @override
  Stream<String> get errorStream => const Stream<String>.empty();

  @override
  Stream<proto.WsMessage> get messageStream => _messages.stream;

  @override
  Future<void> connect() async {
    connectCalls++;
    await ready.future;
    _status.add(ConnectionStatus.connected);
  }

  @override
  void dispose() {
    _status.close();
    _messages.close();
    super.dispose();
  }
}

class _TestConnectionNotifier extends ConnectionNotifier {
  static ConnectionService Function(String url, TunnelCrypto crypto)? factory;

  @override
  ConnectionService createConnectionService(String url, TunnelCrypto crypto) {
    return factory!(url, crypto);
  }
}

void main() {
  late Directory cacheDir;
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    cacheDir = Directory.systemTemp.createTempSync('ggcode_mobile_cache_test_');
    debugWorkspaceCacheDatabasePathOverride =
        '${cacheDir.path}/workspace_cache.sqlite';
  });

  tearDown(() {
    debugWorkspaceCacheDatabasePathOverride = null;
    if (cacheDir.existsSync()) {
      cacheDir.deleteSync(recursive: true);
    }
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
    expect(message.toolCompleted, isTrue);
  });

  test('ChatNotifier marks tool complete even when result text is empty', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(chatProvider.notifier);
    notifier.handleToolCall(
      proto.ToolCallData(
        toolId: 'tool-empty',
        toolName: 'grep',
        displayName: 'Grep',
        args: '{"pattern":"foo"}',
        detail: 'foo',
      ),
      messageId: 'ev-empty',
    );
    notifier.handleToolResult(
      proto.ToolResultData(
        toolId: 'tool-empty',
        toolName: 'grep',
        result: '',
        isError: false,
      ),
    );

    final message = container.read(chatProvider).single;
    expect(message.toolCompleted, isTrue);
    expect(message.toolResult, '');
  });

  test('ChatNotifier stores task summaries and expanded payload separately',
      () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(chatProvider.notifier);
    notifier.handleToolCall(
      proto.ToolCallData(
        toolId: 'tool-task',
        toolName: 'task_get',
        displayName: 'Task',
        args: '{"taskId":"task-1"}',
        detail: 'task-1',
      ),
      messageId: 'ev-task',
    );
    notifier.handleToolResult(
      proto.ToolResultData(
        toolId: 'tool-task',
        toolName: 'task_get',
        result:
            '{"id":"task-1","subject":"Fix tunnel parity","status":"in_progress"}',
        summary: 'Fix tunnel parity [in progress] — task-1',
        payload:
            'Task ID: task-1\nStatus: in progress\nSubject: Fix tunnel parity',
        payloadMode: 'task_fields',
        isError: false,
      ),
    );

    final message = container.read(chatProvider).single;
    expect(message.toolResult, 'Fix tunnel parity [in progress] — task-1');
    expect(message.toolPayload, contains('Task ID: task-1'));
    expect(message.toolPayloadMode, 'task_fields');
    expect(message.toolCompleted, isTrue);
  });

  test('ChatNotifier collapses reasoning when assistant text arrives', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(chatProvider.notifier);
    notifier.handleReasoningChunk(
      proto.TextData(id: 'reason-1', chunk: 'thinking', done: false),
    );
    notifier.handleTextChunk(
      proto.TextData(id: 'msg-1', chunk: 'answer', done: false),
    );

    final messages = container.read(chatProvider);
    final reasoning = messages.firstWhere((m) => m.id == 'reason-1');
    final answer = messages.firstWhere((m) => m.id == 'msg-1');
    expect(reasoning.kind, 'reasoning');
    expect(reasoning.streaming, isFalse);
    expect(reasoning.reasoningCollapsed, isTrue);
    expect(answer.text, 'answer');
  });

  test('ConnectionNotifier projects subagent reasoning into agent tab', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(connectionProvider.notifier);
    notifier.handleIncomingForTest(
      proto.WsMessage(
        sessionId: 'sess-1',
        eventId: 'ev-000000001',
        type: 'subagent_reasoning',
        data: {
          'agent_id': 'sa-1',
          'id': 'reason-1',
          'chunk': 'thinking',
        },
      ),
    );
    notifier.handleIncomingForTest(
      proto.WsMessage(
        sessionId: 'sess-1',
        eventId: 'ev-000000002',
        type: 'subagent_text',
        data: {
          'agent_id': 'sa-1',
          'id': 'msg-1',
          'chunk': 'done',
        },
      ),
    );

    final messages = container.read(chatProvider);
    final reasoning = messages.firstWhere((m) => m.id == 'sa-1-reason-1');
    final text = messages.firstWhere((m) => m.id == 'sa-1-msg-1');
    expect(reasoning.sourceId, 'sa-1');
    expect(reasoning.kind, 'reasoning');
    expect(reasoning.reasoningCollapsed, isTrue);
    expect(text.sourceId, 'sa-1');
    expect(text.text, 'done');
  });

  test('ChatNotifier formats teammate_spawn tool results', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(chatProvider.notifier);
    notifier.handleToolCall(
      proto.ToolCallData(
        toolId: 'tool-spawn',
        toolName: 'teammate_spawn',
        displayName: 'Spawn teammate',
        args: '{"team_id":"team-1","name":"researcher"}',
        detail: 'Create researcher',
      ),
      messageId: 'ev-spawn',
    );
    notifier.handleToolResult(
      proto.ToolResultData(
        toolId: 'tool-spawn',
        toolName: 'teammate_spawn',
        result: '{"ID":"tm-1","Name":"researcher","Status":"idle"}',
        isError: false,
      ),
    );

    final message = container.read(chatProvider).single;
    expect(message.toolResult, 'Teammate researcher Created');
    expect(message.toolCompleted, isTrue);
  });

  test('ChatNotifier formats team_create tool results', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(chatProvider.notifier);
    notifier.handleToolCall(
      proto.ToolCallData(
        toolId: 'tool-team',
        toolName: 'team_create',
        displayName: 'Create team',
        args: '{"name":"research-squad"}',
        detail: 'research-squad',
      ),
      messageId: 'ev-team',
    );
    notifier.handleToolResult(
      proto.ToolResultData(
        toolId: 'tool-team',
        toolName: 'team_create',
        result: '{"ID":"team-1","Name":"research-squad"}',
        isError: false,
      ),
    );

    final message = container.read(chatProvider).single;
    expect(message.toolResult, 'Team research-squad Created');
    expect(message.toolCompleted, isTrue);
  });

  test('ChatNotifier formats swarm_task_create tool results as markdown', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(chatProvider.notifier);
    notifier.handleToolCall(
      proto.ToolCallData(
        toolId: 'tool-task',
        toolName: 'swarm_task_create',
        displayName: 'Fix replay gaps',
        args:
            '{"team_id":"team-1","subject":"Fix replay gaps","description":"## Plan\\n- ignore for header"}',
        detail: '',
      ),
      messageId: 'ev-task',
    );
    notifier.handleToolResult(
      proto.ToolResultData(
        toolId: 'tool-task',
        toolName: 'swarm_task_create',
        result:
            '{"ID":"task-1","Subject":"Fix replay gaps","Description":"## Plan\\n1. Keep markdown\\n2. Render it"}',
        isError: false,
      ),
    );

    final message = container.read(chatProvider).single;
    expect(message.toolDisplayName, 'Fix replay gaps');
    expect(message.toolResult, '## Plan\n1. Keep markdown\n2. Render it');
    expect(message.toolCompleted, isTrue);
  });

  test('ChatNotifier formats start_command tool results as Started/Failed', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(chatProvider.notifier);
    notifier.handleToolCall(
      proto.ToolCallData(
        toolId: 'tool-start',
        toolName: 'start_command',
        displayName: 'Run in background',
        args: '{"command":"npm run dev"}',
        detail: 'npm run dev',
      ),
      messageId: 'ev-start',
    );
    notifier.handleToolResult(
      proto.ToolResultData(
        toolId: 'tool-start',
        toolName: 'start_command',
        result: 'Job ID: cmd-1\nStatus: running\nDuration: 1s',
        isError: false,
      ),
    );

    expect(container.read(chatProvider).single.toolResult, 'Started');

    notifier.handleToolCall(
      proto.ToolCallData(
        toolId: 'tool-start-fail',
        toolName: 'start_command',
        displayName: 'Run in background',
        args: '{"command":"npm run dev"}',
        detail: 'npm run dev',
      ),
      messageId: 'ev-start-fail',
    );
    notifier.handleToolResult(
      proto.ToolResultData(
        toolId: 'tool-start-fail',
        toolName: 'start_command',
        result: 'permission denied',
        isError: true,
      ),
    );

    expect(container.read(chatProvider).last.toolResult, 'Failed');
  });

  test(
      'ConnectionNotifier creates subagent state from tool activity without spawn',
      () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(connectionProvider.notifier);
    notifier.handleIncomingForTest(
      proto.WsMessage(
        sessionId: 'sess-1',
        eventId: 'ev-000000001',
        type: 'subagent_tool_call',
        data: {
          'agent_id': 'sa-1',
          'tool_id': 'tool-1',
          'tool_name': 'read_file',
          'display_name': 'Read',
          'args': '{"path":"a.txt"}',
          'detail': 'a.txt',
        },
      ),
    );

    final agents = container.read(subagentProvider);
    expect(agents.containsKey('sa-1'), isTrue);
    expect(agents['sa-1']!.name, 'sa-1');
    expect(agents['sa-1']!.status, 'running');

    final messages = container.read(chatProvider);
    expect(messages.single.sourceId, 'sa-1');
    expect(messages.single.toolDisplayName, 'Read');
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

  test('ConnectionNotifier preserves shell message kinds', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(connectionProvider.notifier);
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-shell-user',
      type: 'user_message',
      data: {
        'text': r'$ git status',
        'display_text': 'git status',
        'kind': 'shell_command',
      },
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-shell-text',
      type: 'text',
      data: {
        'id': 'shell-out-1',
        'chunk': '\u001b[31mfail\u001b[0m\n',
        'kind': 'shell_output',
        'done': false,
      },
    ));

    final messages = container.read(chatProvider);
    expect(messages, hasLength(2));
    expect(messages[0].kind, 'shell_command');
    expect(messages[0].text, 'git status');
    expect(messages[1].kind, 'shell_output');
    expect(messages[1].text, '\u001b[31mfail\u001b[0m\n');
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
      agentStatus: 'busy',
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
    expect(snapshot.agentStatus, 'busy');
    expect(snapshot.agentStatusMessage, 'read_file');
  });

  test('workspace cache preserves full message history without truncation',
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
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=full-cache');
    await cache.registerLiveSession('sess-full', info, lastEventId: 'ev-0400');
    final messages = List<ChatMessage>.generate(
      400,
      (index) => ChatMessage(
        id: 'msg-$index',
        text: 'message-$index',
        time: DateTime.parse('2026-01-01T00:00:00Z')
            .add(Duration(minutes: index)),
      ),
    );
    await cache.captureLiveProjection(
      messages: messages,
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-0400',
    );
    await Future<void>.delayed(const Duration(milliseconds: 450));

    final restored = ProviderContainer();
    addTearDown(restored.dispose);
    final restoredCache = restored.read(workspaceCacheProvider.notifier);
    await restoredCache.initialize();

    final restoredState = restored.read(workspaceCacheProvider);
    final snapshot = restoredCache.snapshotFor(
      restoredState.selectedWorkspaceKey!,
      'sess-full',
    );
    expect(snapshot, isNotNull);
    expect(snapshot!.messages, hasLength(400));
    expect(snapshot.messages.first.text, 'message-0');
    expect(snapshot.messages.last.text, 'message-399');
  });

  test('workspace cache flushNow persists snapshot without debounce wait',
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
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=flush-now');
    await cache.registerLiveSession('sess-flush', info, lastEventId: 'ev-0002');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'msg-flush',
          text: 'persist immediately',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-0002',
    );
    await cache.flushNow();

    final restored = ProviderContainer();
    addTearDown(restored.dispose);
    final restoredCache = restored.read(workspaceCacheProvider.notifier);
    await restoredCache.initialize();

    final restoredState = restored.read(workspaceCacheProvider);
    final snapshot = restoredCache.snapshotFor(
      restoredState.selectedWorkspaceKey!,
      'sess-flush',
    );
    expect(snapshot, isNotNull);
    expect(snapshot!.messages.single.text, 'persist immediately');
  });

  test(
      'workspace cache restores selected workspace url when sqlite is unavailable',
      () async {
    const selectedWorkspaceKey = 'workspace-fallback';
    const selectedWorkspaceUrl = 'wss://example.test/ws?token=fallback';
    SharedPreferences.setMockInitialValues({
      'ggcode_workspace_cache_v1': jsonEncode({
        'selected_workspace_key': selectedWorkspaceKey,
        'selected_session_id': 'sess-fallback',
        'selected_workspace_url': selectedWorkspaceUrl,
      }),
    });
    debugWorkspaceCacheDatabasePathOverride = cacheDir.path;

    final container = ProviderContainer();
    addTearDown(container.dispose);

    final cache = container.read(workspaceCacheProvider.notifier);
    await cache.initialize();

    final state = container.read(workspaceCacheProvider);
    expect(state.initialized, isTrue);
    expect(state.selectedWorkspaceKey, selectedWorkspaceKey);
    expect(state.selectedSessionId, 'sess-fallback');
    expect(cache.urlForWorkspace(selectedWorkspaceKey), selectedWorkspaceUrl);
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
      agentStatus: 'busy',
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
      agentStatus: 'busy',
      agentStatusMessage: 'read_file',
      lastEventId: 'ev-0001',
    );
    cache.markDisconnected();

    expect(container.read(displayedAgentStatusProvider), 'busy');
    expect(container.read(displayedAgentStatusMessageProvider), 'read_file');
  });

  test('ConnectionNotifier keeps busy lifecycle separate from activity', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(connectionProvider.notifier);
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000001',
      type: 'status',
      data: {'status': 'busy'},
    ));

    expect(container.read(displayedAgentStatusProvider), 'busy');
    expect(container.read(displayedAgentStatusMessageProvider), '');

    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000002',
      type: 'activity',
      data: {'activity': 'Collecting project knowledge...'},
    ));

    expect(container.read(displayedAgentStatusProvider), 'busy');
    expect(
      container.read(displayedAgentStatusMessageProvider),
      'Collecting project knowledge...',
    );

    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000003',
      type: 'status',
      data: {'status': 'idle'},
    ));

    expect(container.read(displayedAgentStatusProvider), 'idle');
    expect(container.read(displayedAgentStatusMessageProvider), '');
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

  test('ConnectionNotifier retries replay gaps and falls back to full history',
      () async {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(connectionProvider.notifier);
    final fakeService = _CaptureResumeHelloService();
    notifier.service = fakeService;
    notifier.configureReplayRecoveryForTest(
      watchdogTimeout: const Duration(milliseconds: 5),
      retryBackoffs: const [
        Duration(milliseconds: 5),
        Duration(milliseconds: 5),
        Duration(milliseconds: 5),
      ],
      fallbackTimeout: const Duration(milliseconds: 5),
    );

    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000001',
      type: 'text',
      data: {'id': 'msg-1', 'chunk': 'first', 'done': false},
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000003',
      type: 'text',
      data: {'id': 'msg-1', 'chunk': 'third', 'done': false},
    ));

    expect(fakeService.replayRequests, 1);

    await Future<void>.delayed(const Duration(milliseconds: 8));
    expect(fakeService.replayRequests, greaterThanOrEqualTo(2));
    await Future<void>.delayed(const Duration(milliseconds: 8));
    expect(fakeService.replayRequests, greaterThanOrEqualTo(3));
    await Future<void>.delayed(const Duration(milliseconds: 8));
    expect(fakeService.replayRequests, greaterThanOrEqualTo(4));
    await Future<void>.delayed(const Duration(milliseconds: 8));

    expect(fakeService.resumeHelloRequests, 1);
    expect(fakeService.resumeClientId, isNotNull);
    expect(fakeService.resumeSessionId, 'sess-1');
    expect(fakeService.resumeLastEventId, isNull);
  });

  test('replay fallback reconnect clears persisted resume cursor', () async {
    SharedPreferences.setMockInitialValues({
      'ggcode_tunnel_client_id': 'client-1',
      'ggcode_tunnel_session_id': 'sess-stale',
      'ggcode_tunnel_last_event_id': 'ev-000000099',
    });

    final firstService = _CaptureResumeHelloService();
    final secondService = _CaptureResumeHelloService();
    final services = <_CaptureResumeHelloService>[firstService, secondService];
    _TestConnectionNotifier.factory = (_, __) => services.removeAt(0);
    final container = ProviderContainer(
      overrides: [
        connectionProvider.overrideWith(_TestConnectionNotifier.new),
      ],
    );
    addTearDown(() {
      _TestConnectionNotifier.factory = null;
      container.dispose();
    });

    final notifier = container.read(connectionProvider.notifier);
    await notifier.connect('wss://example.test/ws?token=abc',
        clearState: false);
    notifier.configureReplayRecoveryForTest(
      watchdogTimeout: const Duration(milliseconds: 2),
      retryBackoffs: const [Duration(milliseconds: 2)],
      fallbackTimeout: const Duration(milliseconds: 2),
    );

    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000001',
      type: 'text',
      data: {'id': 'msg-1', 'chunk': 'first', 'done': false},
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000003',
      type: 'text',
      data: {'id': 'msg-1', 'chunk': 'third', 'done': false},
    ));

    await Future<void>.delayed(const Duration(milliseconds: 20));

    expect(secondService.resumeClientId, 'client-1');
    expect(secondService.resumeSessionId, anyOf(isNull, isEmpty));
    expect(secondService.resumeLastEventId, anyOf(isNull, isEmpty));
  });

  test('stale room failure clears auto-resume selection and cursor', () async {
    SharedPreferences.setMockInitialValues({
      'ggcode_tunnel_client_id': 'client-1',
      'ggcode_tunnel_session_id': 'sess-stale',
      'ggcode_tunnel_last_event_id': 'ev-000000099',
    });

    final info = proto.SessionInfoData(
      workspace: '/tmp/demo',
      model: 'gpt-5.4',
      provider: 'openai',
      mode: 'supervised',
      version: '1.0.0',
    );

    final service = _PermanentRoomFailureService();
    _TestConnectionNotifier.factory = (_, __) => service;
    final container = ProviderContainer(
      overrides: [
        connectionProvider.overrideWith(_TestConnectionNotifier.new),
      ],
    );
    addTearDown(() {
      _TestConnectionNotifier.factory = null;
      container.dispose();
    });

    final cache = container.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=stale');
    await cache.registerLiveSession('sess-stale', info,
        lastEventId: 'ev-000000099');
    await cache.flushNow();

    final notifier = container.read(connectionProvider.notifier);
    await notifier.restoreSelectedWorkspace();
    await Future<void>.delayed(const Duration(milliseconds: 10));

    final cacheState = container.read(workspaceCacheProvider);
    final connState = container.read(connectionProvider);
    final prefs = await SharedPreferences.getInstance();

    expect(cacheState.selectedWorkspaceKey, isNull);
    expect(cacheState.selectedSessionId, isNull);
    expect(connState.status, ConnectionStatus.disconnected);
    expect(connState.url, isNull);
    expect(connState.error, contains('Room not found'));
    expect(
      cache.sortedWorkspaces().single.url,
      'wss://example.test/ws?token=stale',
    );
    expect(prefs.getString('ggcode_tunnel_session_id'), anyOf(isNull, isEmpty));
    expect(
      prefs.getString('ggcode_tunnel_last_event_id'),
      anyOf(isNull, isEmpty),
    );
  });

  test('server offline keeps room selection pinned for recovery', () async {
    final info = proto.SessionInfoData(
      workspace: '/tmp/demo',
      model: 'gpt-5.4',
      provider: 'openai',
      mode: 'supervised',
      version: '1.0.0',
    );

    final service = _ServerOfflineService();
    _TestConnectionNotifier.factory = (_, __) => service;
    final container = ProviderContainer(
      overrides: [
        connectionProvider.overrideWith(_TestConnectionNotifier.new),
      ],
    );
    addTearDown(() {
      _TestConnectionNotifier.factory = null;
      container.dispose();
    });

    final cache = container.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=room-a');
    await cache.registerLiveSession('sess-room', info,
        lastEventId: 'ev-000000120');
    container.read(chatProvider.notifier).set([
      ChatMessage(
        id: 'msg-1',
        text: 'keep this while relay recovers',
        time: DateTime.parse('2026-01-01T00:00:00Z'),
      ),
    ]);
    await cache.captureLiveProjection(
      messages: container.read(chatProvider),
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000120',
    );

    final notifier = container.read(connectionProvider.notifier);
    await notifier.restoreSelectedWorkspace();
    await Future<void>.delayed(const Duration(milliseconds: 10));

    final cacheState = container.read(workspaceCacheProvider);
    final connState = container.read(connectionProvider);
    expect(cacheState.selectedWorkspaceKey, isNotNull);
    expect(cacheState.selectedSessionId, 'sess-room');
    expect(connState.status, ConnectionStatus.disconnected);
    expect(connState.url, 'wss://example.test/ws?token=room-a');
    expect(connState.error, 'Relay recovering');
    expect(container.read(displayedMessagesProvider).single.text,
        'keep this while relay recovers');
  });

  test('connect coalesces concurrent requests for the same url', () async {
    final service = _BlockingConnectService();
    var factoryCalls = 0;
    _TestConnectionNotifier.factory = (_, __) {
      factoryCalls++;
      return service;
    };
    final container = ProviderContainer(
      overrides: [
        connectionProvider.overrideWith(_TestConnectionNotifier.new),
      ],
    );
    addTearDown(() {
      _TestConnectionNotifier.factory = null;
      container.dispose();
    });

    final notifier = container.read(connectionProvider.notifier);
    final connectA = notifier.connect('wss://example.test/ws?token=room-a');
    final connectB = notifier.connect('wss://example.test/ws?token=room-a');

    await Future<void>.delayed(const Duration(milliseconds: 10));
    expect(factoryCalls, 1);
    expect(service.connectCalls, 1);

    service.ready.complete();
    await Future.wait([connectA, connectB]);
    await Future<void>.delayed(const Duration(milliseconds: 10));

    final state = container.read(connectionProvider);
    expect(state.status, ConnectionStatus.connected);
    expect(state.url, 'wss://example.test/ws?token=room-a');
  });

  test(
      'replay fallback restores cached projection without reconnect when no authoritative events arrived',
      () async {
    SharedPreferences.setMockInitialValues({
      'ggcode_tunnel_client_id': 'client-1',
      'ggcode_tunnel_session_id': 'sess-1',
      'ggcode_tunnel_last_event_id': 'ev-000000001',
    });

    final info = proto.SessionInfoData(
      workspace: '/tmp/demo',
      model: 'gpt-5.4',
      provider: 'openai',
      mode: 'supervised',
      version: '1.0.0',
    );

    final firstService = _CaptureResumeHelloService();
    final secondService = _CaptureResumeHelloService();
    final services = <_CaptureResumeHelloService>[firstService, secondService];
    _TestConnectionNotifier.factory = (_, __) => services.removeAt(0);
    final container = ProviderContainer(
      overrides: [
        connectionProvider.overrideWith(_TestConnectionNotifier.new),
      ],
    );
    addTearDown(() {
      _TestConnectionNotifier.factory = null;
      container.dispose();
    });

    final cache = container.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=abc');
    await cache.registerLiveSession('sess-1', info,
        lastEventId: 'ev-000000001');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'msg-cached',
          text: 'cached history',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000001',
    );

    final notifier = container.read(connectionProvider.notifier);
    await notifier.connect('wss://example.test/ws?token=abc',
        clearState: false);
    notifier.configureReplayRecoveryForTest(
      watchdogTimeout: const Duration(milliseconds: 2),
      retryBackoffs: const [Duration(milliseconds: 2)],
      fallbackTimeout: const Duration(milliseconds: 2),
    );

    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000003',
      type: 'text',
      data: {'id': 'msg-gap', 'chunk': 'third', 'done': false},
    ));

    await Future<void>.delayed(const Duration(milliseconds: 12));

    expect(container.read(displayedMessagesProvider), hasLength(1));
    expect(container.read(displayedMessagesProvider).single.text,
        'cached history');
    expect(firstService.resumeHelloRequests, greaterThanOrEqualTo(2));
    expect(secondService.resumeHelloRequests, 0);
    expect(secondService.replayRequests, 0);
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

  test(
      'ConnectionNotifier restores snapshot cursor instead of newer session record cursor',
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
        lastEventId: 'ev-000000100');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'msg-1',
          text: 'cached hello',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000100',
    );
    await cache.updateLiveCursor('sess-122', 'ev-000000120');

    container.read(chatProvider.notifier).clearMessages();
    container.read(sessionInfoProvider.notifier).set(null);
    container.read(currentModeProvider.notifier).set('supervised');

    final notifier = container.read(connectionProvider.notifier);
    final restored = notifier.restoreProjectionFromCacheForTest();

    expect(restored, isTrue);
    expect(notifier.currentSessionId, 'sess-122');
    expect(notifier.lastAppliedEventId, 'ev-000000100');
    expect(container.read(chatProvider).single.text, 'cached hello');
  });

  test(
      'cached snapshot seeds resume cursor so reconnect and gap recovery stay incremental',
      () async {
    SharedPreferences.setMockInitialValues({
      'ggcode_tunnel_client_id': 'client-1',
    });

    final info = proto.SessionInfoData(
      workspace: '/tmp/demo',
      model: 'gpt-5.4',
      provider: 'openai',
      mode: 'bypass',
      version: '1.0.0',
    );

    final service = _CaptureResumeHelloService();
    _TestConnectionNotifier.factory = (_, __) => service;
    final container = ProviderContainer(
      overrides: [
        connectionProvider.overrideWith(_TestConnectionNotifier.new),
      ],
    );
    addTearDown(() {
      _TestConnectionNotifier.factory = null;
      container.dispose();
    });

    final cache = container.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=abc');
    await cache.registerLiveSession('sess-122', info,
        lastEventId: 'ev-000000120');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'msg-1',
          text: 'cached hello',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000120',
    );

    final notifier = container.read(connectionProvider.notifier);
    await notifier.connect('wss://example.test/ws?token=abc',
        clearState: false);
    await Future<void>.delayed(Duration.zero);

    expect(service.resumeClientId, 'client-1');
    expect(service.resumeSessionId, 'sess-122');
    expect(service.resumeLastEventId, 'ev-000000120');
    expect(container.read(chatProvider).single.text, 'cached hello');

    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-122',
      eventId: 'ev-000000122',
      type: 'text',
      data: {'id': 'msg-2', 'chunk': 'gap', 'done': false},
    ));

    expect(service.replayRequests, 1);
    expect(service.replaySessionId, 'sess-122');
    expect(service.replayLastEventId, 'ev-000000120');
    expect(service.resumeHelloRequests, 1);
  });

  test(
      'cached reconnect resumes from snapshot cursor when session record advanced ahead',
      () async {
    SharedPreferences.setMockInitialValues({
      'ggcode_tunnel_client_id': 'client-1',
    });

    final info = proto.SessionInfoData(
      workspace: '/tmp/demo',
      model: 'gpt-5.4',
      provider: 'openai',
      mode: 'bypass',
      version: '1.0.0',
    );

    final service = _CaptureResumeHelloService();
    _TestConnectionNotifier.factory = (_, __) => service;
    final container = ProviderContainer(
      overrides: [
        connectionProvider.overrideWith(_TestConnectionNotifier.new),
      ],
    );
    addTearDown(() {
      _TestConnectionNotifier.factory = null;
      container.dispose();
    });

    final cache = container.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=abc');
    await cache.registerLiveSession('sess-122', info,
        lastEventId: 'ev-000000100');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'msg-1',
          text: 'cached hello',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000100',
    );
    await cache.updateLiveCursor('sess-122', 'ev-000000120');

    final notifier = container.read(connectionProvider.notifier);
    await notifier.connect('wss://example.test/ws?token=abc',
        clearState: false);
    await Future<void>.delayed(Duration.zero);

    expect(service.resumeClientId, 'client-1');
    expect(service.resumeSessionId, 'sess-122');
    expect(service.resumeLastEventId, 'ev-000000100');
    expect(container.read(chatProvider).single.text, 'cached hello');
  });

  test('fresh connect defers cached projection restore until live attach',
      () async {
    SharedPreferences.setMockInitialValues({
      'ggcode_tunnel_client_id': 'client-1',
      'ggcode_tunnel_session_id': 'sess-122',
      'ggcode_tunnel_last_event_id': 'ev-000000120',
    });

    final info = proto.SessionInfoData(
      workspace: '/tmp/demo',
      model: 'gpt-5.4',
      provider: 'openai',
      mode: 'bypass',
      version: '1.0.0',
    );

    final service = _CaptureResumeHelloService();
    _TestConnectionNotifier.factory = (_, __) => service;
    final container = ProviderContainer(
      overrides: [
        connectionProvider.overrideWith(_TestConnectionNotifier.new),
      ],
    );
    addTearDown(() {
      _TestConnectionNotifier.factory = null;
      container.dispose();
    });

    final cache = container.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=abc');
    await cache.registerLiveSession('sess-122', info,
        lastEventId: 'ev-000000120');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'msg-1',
          text: 'cached hello',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000120',
    );

    final notifier = container.read(connectionProvider.notifier);
    await notifier.connect('wss://example.test/ws?token=abc');
    await Future<void>.delayed(Duration.zero);

    expect(container.read(chatProvider), isEmpty);
    expect(service.resumeClientId, 'client-1');
    expect(service.resumeSessionId, isNull);
    expect(service.resumeLastEventId, isNull);

    service.emit(proto.WsMessage(
      sessionId: 'sess-122',
      type: 'active_session',
      data: {'session_id': 'sess-122'},
    ));
    await Future<void>.delayed(const Duration(milliseconds: 10));

    expect(container.read(chatProvider).single.text, 'cached hello');
  });

  test('reconnect replaces cached busy state with authoritative idle status',
      () async {
    final info = proto.SessionInfoData(
      workspace: '/tmp/demo',
      model: 'gpt-5.4',
      provider: 'openai',
      mode: 'supervised',
      version: '1.0.0',
    );

    final service = _CaptureResumeHelloService();
    _TestConnectionNotifier.factory = (_, __) => service;
    final container = ProviderContainer(
      overrides: [
        connectionProvider.overrideWith(_TestConnectionNotifier.new),
      ],
    );
    addTearDown(() {
      _TestConnectionNotifier.factory = null;
      container.dispose();
    });

    final cache = container.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=abc');
    await cache.registerLiveSession('sess-1', info,
        lastEventId: 'ev-000000120');
    await cache.captureLiveProjection(
      messages: const [],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'busy',
      agentStatusMessage: 'Collecting project knowledge...',
      lastEventId: 'ev-000000120',
    );

    final notifier = container.read(connectionProvider.notifier);
    await notifier.restoreSelectedWorkspace();
    await Future<void>.delayed(Duration.zero);

    expect(container.read(displayedAgentStatusProvider), 'busy');
    expect(
      container.read(displayedAgentStatusMessageProvider),
      'Collecting project knowledge...',
    );

    service.emit(proto.WsMessage(
      sessionId: 'sess-1',
      type: 'active_session',
      data: {'session_id': 'sess-1'},
    ));
    service.emit(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000121',
      type: 'status',
      data: {'status': 'idle'},
    ));
    await Future<void>.delayed(Duration.zero);

    expect(container.read(displayedAgentStatusProvider), 'idle');
    expect(container.read(displayedAgentStatusMessageProvider), '');
  });

  test(
      'ConnectionNotifier keeps cached messages visible when live session attaches after cold restore',
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
    await cache.registerLiveSession('sess-live', info,
        lastEventId: 'ev-000000120');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'msg-1',
          text: 'cached hello',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
        ChatMessage(
          id: 'msg-2',
          text: 'cached world',
          time: DateTime.parse('2026-01-01T00:01:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000120',
    );
    cache.markDisconnected();

    container.read(chatProvider.notifier).clearMessages();
    container.read(sessionInfoProvider.notifier).set(null);
    container.read(currentModeProvider.notifier).set('supervised');

    final notifier = container.read(connectionProvider.notifier);
    final restored =
        notifier.restoreProjectionFromCacheForTest(adoptCursor: false);

    expect(restored, isTrue);
    expect(notifier.currentSessionId, isEmpty);
    expect(container.read(chatProvider), hasLength(2));

    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-live',
      type: 'active_session',
      data: {'session_id': 'sess-live'},
    ));
    await Future<void>.delayed(const Duration(milliseconds: 1));

    expect(container.read(displayedMessagesProvider), hasLength(2));
    expect(container.read(displayedMessagesProvider).last.text, 'cached world');
  });

  test(
      'ConnectionNotifier does not restore cached projection on full-history resume',
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
    await cache.registerLiveSession('sess-live', info,
        lastEventId: 'ev-000000120');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'ev-000000120',
          text: 'cached hello',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000120',
    );
    cache.markDisconnected();

    final notifier = container.read(connectionProvider.notifier);
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-live',
      type: 'resume_ack',
      data: {'resume_mode': 'full_history'},
    ));
    await Future<void>.delayed(const Duration(milliseconds: 1));

    expect(container.read(chatProvider), isEmpty);
    expect(notifier.lastAppliedEventId, isEmpty);
  });

  test(
      'ConnectionNotifier does not reseed stale cursor when active_session restore races with full-history resume',
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
    await cache.registerLiveSession('sess-live', info,
        lastEventId: 'ev-000000174');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'ev-000000174',
          text: 'cached stale history',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000174',
    );
    cache.markDisconnected();

    final notifier = container.read(connectionProvider.notifier);
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-live',
      type: 'active_session',
      data: {'session_id': 'sess-live'},
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-live',
      type: 'resume_ack',
      data: {'resume_mode': 'full_history'},
    ));
    await Future<void>.delayed(const Duration(milliseconds: 10));

    expect(container.read(chatProvider), isEmpty);
    expect(notifier.lastAppliedEventId, isEmpty);
  });

  test(
      'ConnectionNotifier does not restore cached projection after snapshot reset',
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
    await cache.registerLiveSession('sess-live', info,
        lastEventId: 'ev-000000120');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'ev-000000120',
          text: 'cached hello',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000120',
    );
    cache.markDisconnected();

    final notifier = container.read(connectionProvider.notifier);
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-live',
      type: 'snapshot_reset',
    ));
    await Future<void>.delayed(const Duration(milliseconds: 1));

    expect(container.read(chatProvider), isEmpty);
    expect(notifier.lastAppliedEventId, isEmpty);
  });

  test(
      'workspace cache does not reattach cached session after scanning a new room',
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
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=old-room');
    await cache.registerLiveSession('sess-room', info,
        lastEventId: 'ev-000000120');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'msg-1',
          text: 'cached from old room',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000120',
    );
    await Future<void>.delayed(const Duration(milliseconds: 450));

    await cache.activateWorkspaceUrl('wss://example.test/ws?token=new-room');
    final adopted = await cache.attachSessionToActiveWorkspace('sess-room');

    expect(adopted, isFalse);
    final cacheState = container.read(workspaceCacheProvider);
    final snapshot = cache.snapshotFor(
      cacheState.selectedWorkspaceKey!,
      'sess-room',
    );
    expect(cacheState.selectedSessionId, isNull);
    expect(snapshot, isNull);
  });

  test(
      'ConnectionNotifier does not restore cached session after active_session moves to a new room',
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
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=old-room');
    await cache.registerLiveSession('sess-room', info,
        lastEventId: 'ev-000000120');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'msg-1',
          text: 'cached after room switch',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000120',
    );
    await Future<void>.delayed(const Duration(milliseconds: 450));

    await cache.activateWorkspaceUrl('wss://example.test/ws?token=new-room');
    container.read(chatProvider.notifier).clearMessages();
    container.read(sessionInfoProvider.notifier).set(null);

    final notifier = container.read(connectionProvider.notifier);
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-room',
      type: 'active_session',
      data: {'session_id': 'sess-room'},
    ));
    await Future<void>.delayed(const Duration(milliseconds: 10));

    expect(container.read(displayedMessagesProvider), isEmpty);
  });

  test(
      'ConnectionNotifier keeps new-room events authoritative when old-room cache exists',
      () async {
    final oldInfo = proto.SessionInfoData(
      workspace: '/tmp/old',
      model: 'gpt-4.1',
      provider: 'openai',
      mode: 'bypass',
      version: '1.0.0',
    );
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
    await cache.activateWorkspaceUrl('wss://example.test/ws?token=old-room');
    await cache.registerLiveSession('sess-room', info,
        lastEventId: 'ev-000000120');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'msg-1',
          text: 'cached after control events',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000120',
    );
    await Future<void>.delayed(const Duration(milliseconds: 450));

    await cache.activateWorkspaceUrl('wss://example.test/ws?token=new-room');
    container.read(chatProvider.notifier).clearMessages();
    container.read(sessionInfoProvider.notifier).set(null);

    final notifier = container.read(connectionProvider.notifier);
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-old',
      type: 'active_session',
      data: {'session_id': 'sess-old'},
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-old',
      eventId: 'ev-000000090',
      type: 'session_info',
      data: {
        'workspace': oldInfo.workspace,
        'model': oldInfo.model,
        'provider': oldInfo.provider,
        'mode': oldInfo.mode,
        'version': oldInfo.version,
      },
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-room',
      eventId: 'ev-000000121',
      type: 'system_message',
      data: {'text': 'mobile connected'},
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-room',
      eventId: 'ev-000000122',
      type: 'activity',
      data: {'activity': 'restoring'},
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-room',
      type: 'active_session',
      data: {'session_id': 'sess-room'},
    ));
    await Future<void>.delayed(const Duration(milliseconds: 10));

    expect(container.read(displayedMessagesProvider), hasLength(1));
    expect(
      container.read(displayedMessagesProvider).single.text,
      'mobile connected',
    );
    expect(container.read(sessionInfoProvider), isNull);
  });

  test(
      'ConnectionNotifier renders system messages and preserves post-tool text ordering',
      () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(connectionProvider.notifier);
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000001',
      type: 'text',
      data: {
        'id': 'msg-before',
        'chunk': 'I checked the current run.',
        'done': false
      },
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000002',
      type: 'text_done',
      data: {'id': 'msg-before', 'done': true},
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000003',
      type: 'tool_call',
      data: {
        'tool_id': 'tool-1',
        'tool_name': 'run_command',
        'display_name': 'Check status',
        'args': '{"command":"gh run list --limit 3"}',
        'detail': 'gh run list --limit 3',
      },
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000004',
      type: 'tool_result',
      data: {
        'tool_id': 'tool-1',
        'tool_name': 'run_command',
        'result': 'completed success release',
        'is_error': false,
      },
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000005',
      type: 'system_message',
      data: {'text': 'rerun is still running'},
    ));
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000006',
      type: 'text',
      data: {
        'id': 'msg-after',
        'chunk': 'The rerun completed successfully.',
        'done': false
      },
    ));

    final messages = container.read(chatProvider);
    expect(messages, hasLength(4));
    expect(messages[0].id, 'msg-before');
    expect(messages[0].text, 'I checked the current run.');
    expect(messages[1].toolId, 'tool-1');
    expect(messages[1].toolResult, 'completed success release');
    expect(messages[2].text, 'rerun is still running');
    expect(messages[2].isUser, isFalse);
    expect(messages[3].id, 'msg-after');
    expect(messages[3].text, 'The rerun completed successfully.');
  });

  test(
      'ConnectionNotifier does not trigger replay recovery after snapshot_reset even with gap',
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
    await cache.registerLiveSession('sess-live', info,
        lastEventId: 'ev-000000860');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'ev-000000860',
          text: 'cached hello',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000860',
    );
    cache.markDisconnected();

    final notifier = container.read(connectionProvider.notifier);
    // snapshot_reset clears the cursor and sets _awaitingSnapshotProjection
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-live',
      type: 'snapshot_reset',
    ));
    await Future<void>.delayed(const Duration(milliseconds: 1));

    // Stale relay event with old ordinal arrives — should still apply but
    // should NOT trigger replay recovery even if it creates a gap with
    // subsequent events. Because _awaitingSnapshotProjection is true,
    // the gap check is skipped entirely.
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-live',
      eventId: 'ev-000000861',
      type: 'text',
      data: {
        'id': 'msg-stale',
        'chunk': 'stale replay text',
      },
    ));
    await Future<void>.delayed(const Duration(milliseconds: 1));
    expect(container.read(chatProvider), isNotEmpty);
    expect(notifier.lastAppliedEventId, 'ev-000000861');
    // _awaitingSnapshotProjection still true (only cleared by session_info)

    // New authoritative session_info from broker snapshot arrives with a
    // much higher ordinal (gap after 861). Because _awaitingSnapshotProjection
    // is still true, the gap check is skipped and session_info is applied
    // directly. _awaitingSnapshotProjection is cleared by session_info handler.
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-live',
      eventId: 'ev-000002000',
      type: 'session_info',
      data: {
        'workspace': '/tmp/demo',
        'model': 'gpt-5.4',
        'provider': 'openai',
        'mode': 'auto',
        'version': '1.0.0',
      },
    ));
    await Future<void>.delayed(const Duration(milliseconds: 1));

    // session_info applied directly (gap check bypassed), mode updated,
    // cursor jumped to 2000.
    expect(container.read(currentModeProvider), 'auto');
    expect(notifier.lastAppliedEventId, 'ev-000002000');
    // No replay recovery was triggered
  });

  test(
      'ConnectionNotifier clears awaitingSnapshotProjection when event ordinal advances',
      () async {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(connectionProvider.notifier);
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-live',
      type: 'snapshot_reset',
    ));

    // First data event clears _awaitingSnapshotProjection
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-live',
      eventId: 'ev-000000001',
      type: 'session_info',
      data: {
        'workspace': '/tmp/demo',
        'model': 'gpt-5.4',
        'provider': 'openai',
        'mode': 'supervised',
        'version': '1.0.0',
      },
    ));
    await Future<void>.delayed(const Duration(milliseconds: 1));
    expect(notifier.lastAppliedEventId, 'ev-000000001');

    // After _awaitingSnapshotProjection is cleared, a gap should now
    // trigger replay recovery normally.
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-live',
      eventId: 'ev-000000010',
      type: 'text',
      data: {'id': 'msg-gap', 'chunk': 'gap'},
    ));
    // Event is buffered (gap 1→10) but NOT applied yet
    expect(
        container.read(chatProvider).where((m) => m.id == 'msg-gap'), isEmpty);
  });

  test(
      'ConnectionNotifier does not reseed stale cursor on session change inside _shouldApplyEvent',
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
    await cache.registerLiveSession('sess-old', info,
        lastEventId: 'ev-000000860');
    await cache.captureLiveProjection(
      messages: [
        ChatMessage(
          id: 'ev-000000860',
          text: 'cached old session',
          time: DateTime.parse('2026-01-01T00:00:00Z'),
        ),
      ],
      subagents: const {},
      sessionInfo: info,
      agentStatus: 'idle',
      agentStatusMessage: 'Ready',
      lastEventId: 'ev-000000860',
    );
    cache.markDisconnected();

    final notifier = container.read(connectionProvider.notifier);
    // Establish an authoritative projection
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-old',
      eventId: 'ev-000000860',
      type: 'session_info',
      data: {
        'workspace': '/tmp/demo',
        'model': 'gpt-5.4',
        'provider': 'openai',
        'mode': 'supervised',
        'version': '1.0.0',
      },
    ));
    await Future<void>.delayed(const Duration(milliseconds: 1));

    // Now an event arrives with a different session ID — this triggers
    // _clearUiProjection inside _shouldApplyEvent. With the fix,
    // _hasAuthoritativeProjection is re-set to true, preventing
    // _restoreSessionProjectionIfAvailable from reseeding cursor.
    notifier.handleIncomingForTest(proto.WsMessage(
      sessionId: 'sess-new',
      eventId: 'ev-000000001',
      type: 'session_info',
      data: {
        'workspace': '/tmp/demo',
        'model': 'gpt-5.4',
        'provider': 'openai',
        'mode': 'auto',
        'version': '1.0.0',
      },
    ));
    await Future<void>.delayed(const Duration(milliseconds: 10));

    // Cursor should be at the new event, NOT the cached old cursor
    expect(notifier.lastAppliedEventId, 'ev-000000001');
    expect(notifier.currentSessionId, 'sess-new');
    // Chat should NOT contain the cached old session message
    expect(
        container
            .read(chatProvider)
            .where((m) => m.text == 'cached old session'),
        isEmpty);
  });
}
