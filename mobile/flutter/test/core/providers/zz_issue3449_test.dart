import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:ggcode_mobile/core/connection_service.dart';
import 'package:ggcode_mobile/core/providers/background_connection_manager.dart';
import 'package:ggcode_mobile/core/providers/connection_store.dart';

// #3449: the clear-cache path (chat_screen disposeAll + disconnect +
// clearAll) used to tear down sockets WITHOUT persisting the teardown -
// ConnectionStore.alive stayed true, so connectAllCachedSessions
// restored exactly the sessions the user had just cleared on the next
// launch. disposeAll must mirror graceful shutdown (markAllDead).
void main() {
  test('disposeAll persists markDead so cleared sessions stay dead (#3449)',
      () async {
    SharedPreferences.setMockInitialValues(const {});
    ConnectionStore.resetForTesting();
    addTearDown(ConnectionStore.resetForTesting);

    final container = ProviderContainer();
    addTearDown(container.dispose);
    final mgr = container.read(backgroundConnectionProvider.notifier);

    // Seed the store with a live background session, as a real run would.
    final store = ConnectionStore.instance;
    await store.load();
    await store.add(StoredConnection(
      id: 'c1',
      url: 'wss://relay.example/ws',
      roomId: 'r1',
      clientId: 'cli-1',
      sessionId: 'sess-1',
      createdAt: DateTime.now(),
      active: false,
      alive: true,
    ));
    // And register it live in the manager (service is never connected).
    mgr.registerService(
      sessionId: 'sess-1',
      url: 'wss://relay.example/ws',
      svc: ConnectionService(
        descriptor: ShareConnectionDescriptor(
          relayUrl: 'wss://relay.example/ws',
          roomId: 'r1',
          authTicket: 't',
          renewToken: 'rt',
          serverPublicKey: '',
        ),
      ),
    );

    mgr.disposeAll(); // the clear-cache teardown under test

    // markAllDead rides store.load().then(...): let the microtask chain run.
    await Future<void>.delayed(const Duration(milliseconds: 50));
    await store.load();
    expect(store.all, isNotEmpty, reason: 'seeded connection must persist');
    expect(store.all.first.alive, isFalse,
        reason:
            'clear-cache must persist markDead or connectAllCachedSessions '
            'resurrects the cleared session on next launch (#3449)');
    expect(mgr.liveSessionIds, isEmpty,
        reason: 'in-memory teardown unchanged');
  });
}
