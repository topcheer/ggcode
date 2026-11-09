import 'dart:io';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/models/protocol.dart' as proto;
import 'package:ggcode_mobile/core/providers/workspace_cache.dart';
import 'package:shared_preferences/shared_preferences.dart';

// Pins #5324: _handlePermanentRoomFailure used to blank _sessionId before
// passing it to clearReconnectTarget. An empty string is non-null, so the
// `?? state.selectedSessionId` fallback inside the cache never fired and
// the clearing branch was skipped — the dead session record kept its
// workspaceKey, and every cold start restored it and retried the stale
// URL forever (ghost reconnect loop). The cache must treat an empty
// sessionId argument exactly like null.
void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('clearReconnectTarget treats empty sessionId like null', () async {
    final tmp = await Directory.systemTemp.createTemp('ggcode_ws_5324');
    debugWorkspaceCacheDatabasePathOverride = '${tmp.path}/cache.db';
    SharedPreferences.setMockInitialValues(const {});
    addTearDown(() async {
      debugWorkspaceCacheDatabasePathOverride = null;
      await tmp.delete(recursive: true);
    });

    final container = ProviderContainer();
    addTearDown(container.dispose);
    final cache = container.read(workspaceCacheProvider.notifier);
    await cache.initialize();

    const sessionId = 'sess-5324';
    final info = proto.SessionInfoData(
      workspace: '/tmp/demo-5324',
      model: 'm',
      provider: 'p',
      mode: 'code',
      version: 'v',
      title: 'demo',
    );
    await cache.registerLiveSession(sessionId, info, lastEventId: 'evt-1');
    await cache.selectSession(sessionId);

    expect(cache.state.sessions[sessionId]?.workspaceKey, isNotEmpty);
    expect(cache.state.selectedSessionId, sessionId);

    // Empty-string argument (the pre-fix caller shape) must still clear
    // the selected session record via the fallback path.
    await cache.clearReconnectTarget(sessionId: '');

    expect(cache.state.sessions[sessionId]?.workspaceKey, isEmpty);
  });
}
