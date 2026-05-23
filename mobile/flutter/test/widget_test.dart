// This is a basic Flutter widget test.
//
// To perform an interaction with a widget in your test, use the WidgetTester
// utility in the flutter_test package. For example, you can send tap and scroll
// gestures. You can also use WidgetTester to find child widgets in the widget
// tree, read text, and verify that the values of widget properties are correct.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:ggcode_mobile/core/models/protocol.dart' as proto;
import 'package:ggcode_mobile/core/providers/session_provider.dart';
import 'package:ggcode_mobile/features/chat/chat_screen.dart';
import 'package:ggcode_mobile/features/chat/input_bar.dart';
import 'package:ggcode_mobile/features/connect/connect_screen.dart';
import 'package:ggcode_mobile/main.dart';

class _FakeConnectionNotifier extends ConnectionNotifier {
  @override
  TunnelConnectionState build() =>
      TunnelConnectionState(status: ConnectionStatus.disconnected);

  void emit(TunnelConnectionState next) {
    state = next;
  }
}

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  testWidgets('App shell renders', (WidgetTester tester) async {
    await tester.pumpWidget(const ProviderScope(child: GGCodeApp()));

    expect(find.byType(MaterialApp), findsOneWidget);
  });

  testWidgets(
      'AppShell returns to ConnectScreen after leaving a disconnected session',
      (WidgetTester tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          connectionProvider.overrideWith(_FakeConnectionNotifier.new),
        ],
        child: const GGCodeApp(),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.byType(ConnectScreen), findsOneWidget);

    final context = tester.element(find.byType(GGCodeApp));
    final container = ProviderScope.containerOf(context, listen: false);
    final notifier =
        container.read(connectionProvider.notifier) as _FakeConnectionNotifier;

    notifier.emit(TunnelConnectionState(
      status: ConnectionStatus.connected,
      url: 'wss://example.test/ws?token=abc',
    ));
    await tester.pump();
    await tester.pump();
    expect(find.byType(ChatScreen), findsOneWidget);

    notifier.emit(TunnelConnectionState(status: ConnectionStatus.disconnected));
    await tester.pump();
    await tester.pump();
    expect(find.byType(ConnectScreen), findsOneWidget);
  });

  testWidgets('ChatScreen uses tool display name as card title',
      (WidgetTester tester) async {
    await tester.pumpWidget(
      const ProviderScope(
        child: MaterialApp(home: ChatScreen()),
      ),
    );

    final context = tester.element(find.byType(ChatScreen));
    final container = ProviderScope.containerOf(context, listen: false);
    final notifier = container.read(chatProvider.notifier);

    notifier.handleToolCall(
      proto.ToolCallData(
        toolId: 'tool-1',
        toolName: 'run_command',
        displayName: 'Build Android APK',
        args: '{"command":"flutter build apk"}',
        detail: 'flutter build apk',
      ),
      messageId: 'ev-0001',
    );

    await tester.pump();

    expect(find.textContaining('Build Android APK'), findsOneWidget);
    expect(find.textContaining('flutter build apk'), findsOneWidget);
    expect(find.text('(Run Command)'), findsNothing);
  });

  testWidgets('Tool cards show a working badge until the result arrives',
      (WidgetTester tester) async {
    await tester.pumpWidget(
      const ProviderScope(
        child: MaterialApp(home: ChatScreen()),
      ),
    );

    final context = tester.element(find.byType(ChatScreen));
    final container = ProviderScope.containerOf(context, listen: false);
    final notifier = container.read(chatProvider.notifier);

    notifier.handleToolCall(
      proto.ToolCallData(
        toolId: 'tool-2',
        toolName: 'run_command',
        displayName: 'Run command',
        args: '{"command":"go test ./..."}',
        detail: 'go test ./...',
      ),
      messageId: 'ev-tool-2',
    );
    await tester.pump();

    expect(
        find.byKey(const Key('toolStatusWorking-ev-tool-2')), findsOneWidget);
    expect(find.byKey(const Key('toolStatusDone-ev-tool-2')), findsNothing);

    notifier.handleToolResult(
      proto.ToolResultData(
        toolId: 'tool-2',
        toolName: 'run_command',
        result: 'ok',
        isError: false,
      ),
    );
    await tester.pump();

    expect(find.byKey(const Key('toolStatusWorking-ev-tool-2')), findsNothing);
    expect(find.byKey(const Key('toolStatusDone-ev-tool-2')), findsOneWidget);
  });

  testWidgets(
      'ChatScreen shows an agent tab when activity arrives before spawn',
      (WidgetTester tester) async {
    await tester.pumpWidget(
      const ProviderScope(
        child: MaterialApp(home: ChatScreen()),
      ),
    );

    final context = tester.element(find.byType(ChatScreen));
    final container = ProviderScope.containerOf(context, listen: false);
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
    await tester.pump();

    expect(find.byType(TabBar), findsOneWidget);
    expect(
      find.descendant(
        of: find.byType(TabBar),
        matching: find.text('sa-1'),
      ),
      findsOneWidget,
    );
    expect(find.descendant(of: find.byType(TabBar), matching: find.byType(Tab)),
        findsNWidgets(2));
  });

  testWidgets(
      'ChatScreen shows an agent tab for live session activity after active_session',
      (WidgetTester tester) async {
    await tester.pumpWidget(
      const ProviderScope(
        child: MaterialApp(home: ChatScreen()),
      ),
    );

    final context = tester.element(find.byType(ChatScreen));
    final container = ProviderScope.containerOf(context, listen: false);
    final notifier = container.read(connectionProvider.notifier);

    notifier.handleIncomingForTest(
      proto.WsMessage(
        sessionId: 'sess-live',
        type: 'active_session',
        data: {'session_id': 'sess-live'},
      ),
    );
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 10));

    notifier.handleIncomingForTest(
      proto.WsMessage(
        sessionId: 'sess-live',
        eventId: 'ev-000000010',
        type: 'subagent_tool_call',
        data: {
          'agent_id': 'sa-live',
          'tool_id': 'tool-1',
          'tool_name': 'read_file',
          'display_name': 'Read',
          'args': '{"path":"a.txt"}',
          'detail': 'a.txt',
        },
      ),
    );
    await tester.pump();

    expect(find.byType(TabBar), findsOneWidget);
    expect(
      find.descendant(
        of: find.byType(TabBar),
        matching: find.text('sa-live'),
      ),
      findsOneWidget,
    );
    expect(find.descendant(of: find.byType(TabBar), matching: find.byType(Tab)),
        findsNWidgets(2));
  });

  testWidgets('InputBar stays enabled while agent is busy',
      (WidgetTester tester) async {
    final controller = TextEditingController();
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          connectionProvider.overrideWith(_FakeConnectionNotifier.new),
        ],
        child: MaterialApp(
          home: Scaffold(body: InputBar(controller: controller)),
        ),
      ),
    );

    final context = tester.element(find.byType(InputBar));
    final container = ProviderScope.containerOf(context, listen: false);
    final notifier =
        container.read(connectionProvider.notifier) as _FakeConnectionNotifier;
    notifier.emit(TunnelConnectionState(
      status: ConnectionStatus.connected,
      url: 'wss://example.test/ws?token=abc',
    ));
    container.read(agentStatusProvider.notifier).set('running');
    container.read(agentStatusMessageProvider.notifier).set('read_file');
    await tester.pump();

    final textField = tester.widget<TextField>(find.byType(TextField));
    expect(textField.enabled, isTrue);
    expect(find.byIcon(Icons.stop_circle), findsOneWidget);
    expect(find.byIcon(Icons.send), findsOneWidget);
  });

  testWidgets('workspace scanner allows manual URL entry',
      (WidgetTester tester) async {
    await tester.pumpWidget(
      const ProviderScope(
        child: MaterialApp(home: ChatScreen()),
      ),
    );

    await tester.tap(find.byIcon(Icons.qr_code_scanner));
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('workspaceScannerManualUrlField')),
        findsOneWidget);
    expect(find.byKey(const Key('workspaceScannerManualConnectButton')),
        findsOneWidget);
  });
}
