import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/providers/workspace_cache.dart';

// Pins #3443: appendSessionEvent/cacheBackgroundEvent must drop events
// tagged with a FOREIGN session id instead of persisting them into the
// local session's cache. Trigger: the host switches the active session
// (broker.SwitchSession sends active_session out-of-band, bypassing the
// outbound queue), so stale tail events from the PREVIOUS session arrive
// after the client adopted the new one; _shouldApplyEvent rejects them
// but _ackSkippedEvent / _handleBackgroundMessage used to persist their
// payload anyway. The #1871 ordinal guard cannot stop this write because
// the foreign ordinal space (e.g. evt-150) is larger than the freshly
// reset local cursor - only the ownership guard can.
class _Preloaded extends WorkspaceCacheNotifier {
  @override
  WorkspaceCacheState build() => WorkspaceCacheState(
        initialized: true,
        workspaces: const {},
        sessions: {
          'ws1': CachedSessionRecord(
            workspaceKey: 'ws1',
            sessionId: 'sess-1',
            title: 'demo',
            model: '',
            provider: '',
            mode: '',
            version: '',
            lastEventId: 'evt-42',
            lastUpdatedAt: DateTime.now(),
          ),
        },
        snapshots: {
          'ws1': CachedSessionSnapshot(
            messages: [],
            subagents: {},
            sessionInfo: null,
            lastEventId: 'evt-42',
          ),
        },
      );

  @override
  Future<void> initialize() async {}
}

void main() {
  test('#3443: foreign-session event is dropped even when ordinal passes the replay guard', () {
    final container = ProviderContainer(
      overrides: [
        workspaceCacheProvider.overrideWith(() => _Preloaded()),
      ],
    );
    addTearDown(container.dispose);
    final cache = container.read(workspaceCacheProvider.notifier);

    // evt-150 > cursor evt-42: the #1871 ordinal guard would let it
    // through - only the ownership guard can drop it.
    cache.appendSessionEvent(
      sessionId: 'sess-1',
      eventType: 'text',
      eventData: {'text': 'leaked from session S1'},
      eventId: 'evt-150',
      eventSessionId: 'sess-OLD',
    );
    final snap = cache.getSessionSnapshot('sess-1')!;
    expect(snap.messages, isEmpty,
        reason: 'foreign-session event must not be persisted (#3443)');
    expect(snap.lastEventId, 'evt-42',
        reason: 'foreign-session event must not advance the cursor');
  });

  test('#3443: cacheBackgroundEvent drops foreign-session events (cursor not poisoned)', () {
    final container = ProviderContainer(
      overrides: [
        workspaceCacheProvider.overrideWith(() => _Preloaded()),
      ],
    );
    addTearDown(container.dispose);
    final cache = container.read(workspaceCacheProvider.notifier);

    cache.cacheBackgroundEvent(
      sessionId: 'sess-1',
      eventType: 'text',
      eventData: {'event_id': 'evt-150'},
      eventSessionId: 'sess-OLD',
    );
    final record = container
        .read(workspaceCacheProvider)
        .sessions['ws1']!;
    expect(record.lastEventId, 'evt-42',
        reason: 'background cursor must not be advanced by foreign events');
  });

  test('#3443: untagged events keep the legacy behavior (applied)', () {
    final container = ProviderContainer(
      overrides: [
        workspaceCacheProvider.overrideWith(() => _Preloaded()),
      ],
    );
    addTearDown(container.dispose);
    final cache = container.read(workspaceCacheProvider.notifier);

    // Untagged (null) - legacy callers and relay events without a session
    // tag must keep working.
    cache.appendSessionEvent(
      sessionId: 'sess-1',
      eventType: 'user_message',
      eventData: {'text': 'untagged', 'message_id': 'm1'},
      eventId: 'evt-43',
    );
    // Empty tag - same as untagged.
    cache.appendSessionEvent(
      sessionId: 'sess-1',
      eventType: 'user_message',
      eventData: {'text': 'empty tag', 'message_id': 'm2'},
      eventId: 'evt-44',
      eventSessionId: '',
    );
    final snap = cache.getSessionSnapshot('sess-1')!;
    expect(snap.messages.length, 2,
        reason: 'untagged/empty-tag events must still be applied');
    expect(snap.lastEventId, 'evt-44');
  });

  test('#3443: own-session tagged events are applied (no false positives)', () {
    final container = ProviderContainer(
      overrides: [
        workspaceCacheProvider.overrideWith(() => _Preloaded()),
      ],
    );
    addTearDown(container.dispose);
    final cache = container.read(workspaceCacheProvider.notifier);

    cache.appendSessionEvent(
      sessionId: 'sess-1',
      eventType: 'user_message',
      eventData: {'text': 'mine', 'message_id': 'm1'},
      eventId: 'evt-43',
      eventSessionId: 'sess-1',
    );
    final snap = cache.getSessionSnapshot('sess-1')!;
    expect(snap.messages.length, 1,
        reason: 'own-session tag must not block the write');
    expect(snap.lastEventId, 'evt-43');
  });
}
