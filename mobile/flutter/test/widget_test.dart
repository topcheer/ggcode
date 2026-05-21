// This is a basic Flutter widget test.
//
// To perform an interaction with a widget in your test, use the WidgetTester
// utility in the flutter_test package. For example, you can send tap and scroll
// gestures. You can also use WidgetTester to find child widgets in the widget
// tree, read text, and verify that the values of widget properties are correct.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'package:ggcode_mobile/core/providers/session_provider.dart';
import 'package:ggcode_mobile/features/chat/chat_screen.dart';
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
}
