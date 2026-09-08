import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/providers/workspace_cache.dart';

// Pins #1871 case 1: appendSessionEvent (the BACKGROUND path used by
// background_connection_manager) must drop replayed events whose ordinal
// is at or behind the snapshot's lastEventId. The foreground
// connection_provider already had this cursor guard; without it, a
// reconnect relay full-history replay duplicated user messages, tool
// results and streaming text into the cached snapshot and SQLite.
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
  test('appendSessionEvent drops replayed events at or behind cursor', () {
    final container = ProviderContainer(
      overrides: [
        workspaceCacheProvider.overrideWith(() => _Preloaded()),
      ],
    );
    addTearDown(container.dispose);
    final cache = container.read(workspaceCacheProvider.notifier);

    // Replay of an already-applied event: must be dropped.
    cache.appendSessionEvent(
      sessionId: 'sess-1',
      eventType: 'user_message',
      eventData: {'text': 'replayed', 'message_id': 'm1'},
      eventId: 'evt-42',
    );
    expect(cache.getSessionSnapshot('sess-1')!.messages, isEmpty,
        reason: 'ordinal == cursor must be dropped');

    // Older replay: also dropped.
    cache.appendSessionEvent(
      sessionId: 'sess-1',
      eventType: 'user_message',
      eventData: {'text': 'older', 'message_id': 'm2'},
      eventId: 'evt-40',
    );
    expect(cache.getSessionSnapshot('sess-1')!.messages, isEmpty,
        reason: 'ordinal < cursor must be dropped');

    // New event: applied, and cursor advances.
    cache.appendSessionEvent(
      sessionId: 'sess-1',
      eventType: 'user_message',
      eventData: {'text': 'fresh', 'message_id': 'm3'},
      eventId: 'evt-43',
    );
    final snap = cache.getSessionSnapshot('sess-1')!;
    expect(snap.messages.length, 1, reason: 'ordinal > cursor must apply');
    expect(snap.messages.first.text, 'fresh');
    expect(snap.lastEventId, 'evt-43');

    // Ordinal-unparseable ids carry no ordering info: applied (same
    // fallback as the foreground guard).
    cache.appendSessionEvent(
      sessionId: 'sess-1',
      eventType: 'user_message',
      eventData: {'text': 'opaque', 'message_id': 'm4'},
      eventId: 'opaque-id',
    );
    expect(cache.getSessionSnapshot('sess-1')!.messages.length, 2,
        reason: 'non-ordinal ids must apply (no ordering info)');
  });
}
