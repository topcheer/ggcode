import 'dart:convert';
import 'dart:io';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/models/protocol.dart' as proto;
import 'package:ggcode_mobile/core/providers/workspace_cache.dart';
import 'package:shared_preferences/shared_preferences.dart';

// Found while driving the simulator through the idb-first mobile_device tool
// (2026-11-28): "Clear all cached data" wiped the SQLite store and the in-memory
// state but left the persisted index (selected_workspace_key /
// selected_session_id in SharedPreferences) untouched. On the next launch the
// cache restored that stale selection, `hasSelectedSession` stayed true and the
// app opened on a "cached historical session" screen for a session that no
// longer existed — with no workspaces left the sheet no longer offered the
// Clear button either, so there was no way back to the Connect screen.
void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('clearAll also drops the persisted selection (cold start lands on Connect)', () async {
    final tmp = await Directory.systemTemp.createTemp('ggcode_ws_clear_all');
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

    const sessionId = 'sess-clear-all';
    await cache.registerLiveSession(
      sessionId,
      proto.SessionInfoData(
        workspace: '/tmp/demo-clear-all',
        model: 'm',
        provider: 'p',
        mode: 'code',
        version: 'v',
        title: 'demo',
      ),
      lastEventId: 'evt-1',
    );
    await cache.selectSession(sessionId);
    await cache.flushNow();

    final prefs = await SharedPreferences.getInstance();
    final before = jsonDecode(prefs.getString('ggcode_workspace_cache_v1')!) as Map<String, dynamic>;
    expect(before['selected_session_id'], sessionId, reason: 'precondition: selection persisted');

    await cache.clearAll();
    await cache.flushNow();

    expect(cache.state.selectedSessionId, isNull);
    expect(cache.state.selectedWorkspaceKey, isNull);
    final after = jsonDecode(prefs.getString('ggcode_workspace_cache_v1')!) as Map<String, dynamic>;
    expect(after['selected_session_id'], isNull, reason: 'persisted selection must be dropped by clearAll');
    expect(after['selected_workspace_key'], isNull);

    // Cold start with the same prefs: the cache must come up with no selection
    // so main.dart shows the Connect screen instead of a dead cached session.
    final relaunched = ProviderContainer();
    addTearDown(relaunched.dispose);
    final restored = relaunched.read(workspaceCacheProvider.notifier);
    await restored.initialize();
    expect(relaunched.read(workspaceCacheProvider).selectedSessionId, isNull);
    expect(restored.state.workspaces, isEmpty);
  });
}
