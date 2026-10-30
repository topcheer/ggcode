import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:ggcode_mobile/features/chat/session_rail.dart';
import 'package:ggcode_mobile/core/providers/workspace_cache.dart';

Widget _wrap(Widget child, {Size size = const Size(400, 800)}) {
  return MediaQuery(
    data: MediaQueryData(size: size),
    child: MaterialApp(home: Scaffold(body: child)),
  );
}

void main() {
  testWidgets('#4585 compact width keeps single-column class', (tester) async {
    tester.view.physicalSize = const Size(400 * 2, 800 * 2);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.reset);

    late WindowSizeClass seen;
    await tester.pumpWidget(_wrap(Builder(
      builder: (context) {
        seen = windowSizeClassOf(context);
        return const SizedBox();
      },
    )));

    expect(seen, WindowSizeClass.compact);
  });

  testWidgets('#4585 expanded width (>=600) flips to expanded class',
      (tester) async {
    tester.view.physicalSize = const Size(800 * 2, 800 * 2);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.reset);

    late WindowSizeClass seen;
    await tester.pumpWidget(_wrap(
      Builder(builder: (context) {
        seen = windowSizeClassOf(context);
        return const SizedBox();
      }),
      size: const Size(800, 800),
    ));

    expect(seen, WindowSizeClass.expanded);
  });

  testWidgets('#4585 SessionRail renders workspace group and forwards tap',
      (tester) async {
    CachedSessionRecord? tapped;
    final ws = WorkspaceRecord(
      key: 'a2VleQ==',
      displayName: 'demo',
      lastSessionId: 's1',
      lastOpenedAt: DateTime.fromMillisecondsSinceEpoch(0),
    );
    final session = CachedSessionRecord(
      workspaceKey: ws.key,
      sessionId: 's1',
      title: 'session one',
      model: '',
      provider: '',
      mode: '',
      version: '',
      lastEventId: 'e1',
      lastUpdatedAt: DateTime.fromMillisecondsSinceEpoch(0),
    );

    await tester.pumpWidget(_wrap(SessionRail(
      workspaces: [ws],
      sessionsFor: (key) => key == ws.key ? [session] : const [],
      activeWorkspaceKey: ws.key,
      liveSessionId: '',
      liveSessionIds: const {},
      onSessionTap: (s) => tapped = s,
    )));
    await tester.pumpAndSettle();

    // Active workspace auto-expands; the session tile must be present.
    expect(find.text('session one'), findsOneWidget);
    await tester.tap(find.text('session one'));
    await tester.pumpAndSettle();
    expect(tapped?.sessionId, 's1');
  });
}
