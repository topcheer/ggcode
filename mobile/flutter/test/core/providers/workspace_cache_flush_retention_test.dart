import 'dart:io';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/models/protocol.dart' as proto;
import 'package:ggcode_mobile/core/providers/workspace_cache.dart';
import 'package:shared_preferences/shared_preferences.dart';

// Pins #2812: _flushDirtyState used to unconditionally clear
// _dirtySnapshots even for keys it skipped writing because the session
// record was missing its workspaceKey (clearReconnectTarget during a
// disconnect window). Those keys silently left the dirty set, so their
// in-memory updates were never persisted unless a later mutation re-armed
// them — increments appended during the disconnect window were lost on
// process exit. The fix retains skipped keys in the dirty set so the next
// flush after the session is restored picks them up.
void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('flush retains dirty snapshot key when session lost workspaceKey',
      () async {
    final tmp = await Directory.systemTemp.createTemp('ggcode_ws_2812');
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

    const sessionId = 'sess-2812';
    final info = proto.SessionInfoData(
      workspace: '/tmp/demo-2812',
      model: 'm',
      provider: 'p',
      mode: 'code',
      version: 'v',
      title: 'demo',
    );
    await cache.registerLiveSession(sessionId, info, lastEventId: 'evt-1');

    // Sanity: with a valid workspaceKey a flush round drains the dirty set.
    await cache.flushNow();
    expect(cache.debugPendingSnapshotFlush, isNot(contains(sessionId)));

    // Background event during a connected window marks the snapshot dirty.
    cache.appendSessionEvent(
      sessionId: sessionId,
      eventType: 'user_message',
      eventData: const {'text': 'hello', 'message_id': 'm1'},
      eventId: 'evt-2',
    );
    expect(cache.debugPendingSnapshotFlush, contains(sessionId));

    // Disconnect window: session record is rebuilt with an empty
    // workspaceKey (clearReconnectTarget) while the snapshot stays in memory.
    await cache.clearReconnectTarget(sessionId: sessionId);

    // Flush round: the snapshot cannot be written (no workspaceKey) but its
    // dirty flag must SURVIVE — pre-fix this clear silently dropped it.
    await cache.flushNow();
    expect(cache.debugPendingSnapshotFlush, contains(sessionId));
  });
}
