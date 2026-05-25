# Flutter Codebase Review — GGCode Mobile (Round 7)

**Scope:** `mobile/flutter/lib/` (11 source files, ~3500 LOC in `session_provider.dart` alone)  
**Date:** 2025-07-15  
**Reviewer:** Automated code review agent

---

## Summary

The Flutter mobile app is a WebSocket-based remote control client for the GGCode AI coding agent. The architecture centers on a monolithic `session_provider.dart` (3650 lines) that handles connection lifecycle, message dispatch, replay recovery, workspace caching (SQLite), and chat state. The codebase shows good test coverage for the provider layer (43 tests in `session_provider_test.dart`, 15+ widget tests) but has several systemic issues around stream lifecycle management, unbounded growth, and the god-object pattern in the provider.

---

## Findings

### CRITICAL

#### C-1. Stream subscription leak in ConnectionNotifier.connect()

**File:** `lib/core/providers/session_provider.dart`, lines 202-248

Four `.listen()` calls on `service!.statusStream`, `service!.errorStream`, `service!.messageStream`, and `service!.ackStream` are fire-and-forget — the `StreamSubscription` objects are never captured and never cancelled. When `connect()` is called again (reconnect, workspace switch), `service` is disposed and recreated at line 155, but the dangling subscriptions from the previous `service` instance will continue firing callbacks on a disposed `ConnectionService`, causing:

- Callbacks on a closed `StreamController` (throws `StateError`)
- Duplicate event processing if the old stream somehow emits
- Memory leak from unclosed stream subscriptions

```dart
// Line 202-248: subscriptions are not captured
service!.statusStream.listen((status) { ... });  // leaked
service!.errorStream.listen((error) { ... });    // leaked
service!.messageStream.listen((msg) => ...);     // leaked
service!.ackStream.listen((ack) { ... });        // leaked
```

**Fix:** Store the subscriptions in a `List<StreamSubscription>` field and cancel them all in the dispose path and before creating new ones on reconnect.

---

#### C-2. Race condition: connect() can interleave with itself

**File:** `lib/core/providers/session_provider.dart`, lines 130-146

While `_connectInFlight` deduplicates calls for the same URL, rapid workspace switching with different URLs can interleave `_connectImpl` executions. The second call disposes `service` at line 155 while the first call's `await service!.connect()` at line 252 is still in progress. This causes:

- Use-after-dispose on the first `ConnectionService`
- `_dispatchMessage` callbacks from the old service writing into the new chat state
- Stream subscriptions from the first connect (see C-1) leaking

```dart
// Line 148-157: second connect() disposes service while first is still awaiting
Future<void> _connectImpl(String url, {required bool clearState}) async {
  // ...
  if (service != null) {
    service!.dispose();  // kills first connection's service
    service = null;
  }
  // ...
  await service!.connect();  // first connect still awaiting on old service
}
```

**Fix:** Add an generation counter or cancellation token. Increment on each `connect()` call; check the token after every `await` and bail if stale.

---

### HIGH

#### H-1. Unbounded chat message list growth

**File:** `lib/core/providers/session_provider.dart`, lines 1400+ (ChatNotifier state)

`ChatNotifier.state` is a `List<ChatMessage>` that grows without bound during a long session. The only clearing happens on `_clearUiProjection()` during reconnect or session switch. A typical multi-hour coding session with heavy tool usage can accumulate thousands of messages, each containing potentially large text/tool payloads. This directly impacts:

- RAM usage on mobile devices (each `ChatMessage` holds full text + tool results)
- UI jank from rebuilding large `ListView` contents
- Snapshot persistence time (SQLite writes grow linearly)

**Fix:** Cap the message list (e.g., 500 messages) and evict oldest entries, or implement lazy loading with a sliding window.

---

#### H-2. ConnectionService._queue can grow unboundedly

**File:** `lib/core/connection_service.dart`, lines 66, 104-108

The `_queue` field is a `Future<void>` chain that appends `_handleRelayMessage` for every incoming WebSocket frame. If decryption/JSON parsing is slow or if `_handleRelayMessage` does async work (it calls `crypto.decryptData` which is `Future`-based), messages pile up. There is no backpressure mechanism.

```dart
Future<void> _queue = Future.value();
// ...
_socketSub = _socket!.listen((data) {
  _queue = _queue.then((_) => _handleRelayMessage(data));
});
```

**Fix:** Add a max queue depth. Drop or coalesce messages when the queue is full, or use an `StreamTransformer` with a bounded buffer.

---

#### H-3. Snapshot SQLite writes on main thread

**File:** `lib/core/providers/session_provider.dart`, lines 3040-3400 (WorkspaceCacheNotifier._flush)

The `_flush()` method writes workspace, session, and snapshot data to SQLite using the `sqlite3` package's synchronous API. For large snapshots (many messages with full text), this can block the UI isolate for tens to hundreds of milliseconds. The `_flushTimer` runs periodically and is triggered on every event application via `_scheduleFlush()`.

```dart
// Line 3298-3398: synchronous SQLite writes
final db = _db;
db.execute('INSERT OR REPLACE INTO ...', [...]);
db.execute('INSERT OR REPLACE INTO ...', [...]);
```

**Fix:** Move SQLite writes to an `Isolate` or use `compute()` to offload from the main thread. Alternatively, debounce writes more aggressively.

---

#### H-4. Static mutable theme state bypasses Riverpod

**File:** `lib/core/theme/app_theme.dart`, lines 171, 207-209

`_current` is a module-level mutable `_Palette` variable that `AppColors` static getters read from. While `_ThemeNotifier.setTheme()` updates both Riverpod state and `_current`, any widget that reads `AppColors.accent` (static getter) outside a Riverpod watch will not rebuild on theme changes. Several widgets use `AppColors` directly in `build()` without watching `themeProvider`, meaning they will render with stale colors after a theme switch until something else triggers a rebuild.

```dart
_Palette _current = _palettes['midnight']!;  // global mutable

class AppColors {
  static Color get accent => _current.accent;  // not tracked by Riverpod
}
```

**Fix:** Either use `InheritedWidget` / Riverpod theme provider throughout (eliminate static `AppColors`), or ensure every widget that uses `AppColors` also watches `themeProvider`.

---

#### H-5. Static mutable translation map is not thread-safe

**File:** `lib/core/l10n/app_localizations.dart`, lines 31, 33-47

`_translations` is a module-level `Map<String, String>` that is reassigned in `loadTranslations()`. If two rapid language changes occur, a `t()` call could read a partially-assigned map (though Dart's single-threaded event loop makes true data races unlikely). More critically, `loadTranslations()` is async and the `t()` function has no guarantee the translations are loaded when first called — the map could be empty on first render.

**Fix:** Ensure `loadTranslations` completes before any `build()` call, or provide a fallback that blocks/defaults gracefully.

---

### MEDIUM

#### M-1. Global mutable `_current` palette not restored on app restart

**File:** `lib/core/theme/app_theme.dart`, lines 171, 213-218

`loadThemePreference()` is async and loads the saved theme from `SharedPreferences`. But `_current` is initialized to `midnight` at module level. If the app restarts with a saved 'oled' theme, there is a window where `_current` is `midnight` until `loadThemePreference` completes. Widgets built during this window use wrong colors.

**Fix:** Await `loadThemePreference()` before rendering the first frame (in `main()`), or set `_current` synchronously from a cached value.

---

#### M-2. SubagentPanel `_expanded` set grows without cleanup

**File:** `lib/features/chat/subagent_panel.dart`, line 18

`_expanded` is a `Set<String>` tracking which sub-agents are expanded. Completed agents that are removed from the display (line 24 filters to `!a.completed || _expanded.contains(a.agentId)`) still have their IDs in `_expanded` if they were expanded before completing. Over time, stale IDs accumulate.

**Fix:** Clean up `_expanded` when agents are removed from `subagentProvider`.

---

#### M-3. Missing error handling in sendEncrypted

**File:** `lib/core/connection_service.dart`, lines 361-372

`sendEncrypted` calls `crypto.encryptData` which can throw. If the socket is closed, `_socket?.add` silently no-ops. There is no error handling for encryption failures — the message is silently dropped.

```dart
Future<void> sendEncrypted(proto.WsMessage msg) async {
  final plaintext = utf8.encode(msg.toJson());
  final encrypted = await crypto.encryptData(plaintext);  // can throw
  final relayMsg = jsonEncode({...});
  _socket?.add(relayMsg);  // silently no-ops if socket is null
}
```

**Fix:** Wrap in try-catch; surface errors to the caller or via `errorStream`.

---

#### M-4. Silently swallowed decrypt errors

**File:** `lib/core/connection_service.dart`, lines 282-284

```dart
} catch (e) {
  // Decrypt error
}
```

Decrypt errors are silently ignored. If the token rotates or there is a nonce collision, messages are dropped without any indication to the user or the provider layer.

**Fix:** Log the error (at minimum `debugPrint`) and optionally emit on `errorStream` with a degraded indicator.

---

#### M-5. `_pendingReplayEvents` SplayTreeMap can accumulate orphaned events

**File:** `lib/core/providers/session_provider.dart`, lines 78, 979-987

Events with a gap in ordinals are stored in `_pendingReplayEvents` and only drained when the preceding ordinal arrives. If the relay never sends the missing event, these accumulate until the watchdog timeout triggers. However, the watchdog only fires after new events arrive to trigger `_updateReplayWatchdog`. If the stream goes quiet, orphaned events persist indefinitely.

**Fix:** Ensure the watchdog is active whenever `_pendingReplayEvents` is non-empty, even without new events.

---

#### M-6. ChatNotifier.fullCopyForSnapshot is O(n) per snapshot flush

**File:** `lib/core/providers/session_provider.dart`, lines ~2040-2060 (ChatNotifier)

Snapshot serialization copies the entire chat message list with `List<ChatMessage>.from(state)`, then serializes each message to JSON. For sessions with hundreds of messages, this is called on every flush cycle and adds GC pressure.

**Fix:** Use dirty-flag tracking to only serialize when messages have actually changed since the last snapshot.

---

#### M-7. TunnelConnectionState.copyWith does not support null-clearing

**File:** `lib/core/providers/session_provider.dart`, lines 44-57

`copyWith` uses `error: error ?? this.error`, making it impossible to clear the error field to `null` after displaying it. The error persists across state transitions.

**Fix:** Use the sentinel pattern (like `workspaceCacheProvider` does) to allow explicitly setting fields to null.

---

#### M-8. Connect screen QR scanner controller lifecycle

**File:** `lib/features/connect/connect_screen.dart`

The `MobileScannerController` is created but needs proper disposal when the widget is removed from the tree. If the user navigates away during scanning, the camera may not be released.

**Fix:** Ensure `MobileScannerController.dispose()` is called in the widget's `dispose()` method.

---

### LOW

#### L-1. app_test.dart is a no-op smoke test

**File:** `test/app_test.dart`, lines 3-6

```dart
testWidgets('App smoke test', (WidgetTester tester) async {
  // Basic smoke test placeholder
});
```

This test does nothing and always passes. It gives a false sense of coverage.

**Fix:** Remove or implement a meaningful smoke test.

---

#### L-2. Hardcoded UI strings not using l10n

**File:** `lib/features/chat/ask_user_screen.dart`, line 124 (`'Submit'`)  
**File:** `lib/features/chat/approval_sheet.dart`, line 73 (`'Deny'`), line 79 (`'Allow'`), line 85 (`'Always Allow'`)  
**File:** `lib/features/chat/subagent_panel.dart`, line 282 (`'Diagram'`)  
**File:** `lib/features/chat/message_bubble.dart`, line 230 (`'Terminal'`), line 281 (`'Diagram'`)

Several UI strings are hardcoded in English rather than using the `t()` translation function, breaking i18n consistency.

**Fix:** Move all user-facing strings to `assets/translations/*.json` and use `t()`.

---

#### L-3. `_AnsiState.apply` does not handle 256-color out-of-range or 24-bit truecolor

**File:** `lib/features/chat/message_bubble.dart`, lines 569-616

ANSI escape code parsing handles 8-color, 16-color, and 256-color, but not 24-bit truecolor (SGR 38;2;R;G;B). Any `38;2;...` sequences will fall through the default case and be misinterpreted.

**Fix:** Add handling for `code == 2` following `code == 38` or `code == 48` to parse RGB triplets.

---

#### L-4. No test coverage for crypto.dart

**File:** `lib/core/crypto.dart` — 0 test files

The AES-GCM encryption/decryption used for the tunnel has no unit tests. A bug in nonce handling or key derivation would silently corrupt all encrypted messages.

**Fix:** Add tests for encrypt/decrypt round-trip, empty input, and invalid ciphertext.

---

#### L-5. No test coverage for app_theme.dart

**File:** `lib/core/theme/app_theme.dart` — 0 test files

Theme switching, palette normalization, and `buildAppTheme()` are untested.

**Fix:** Add unit tests verifying theme switching updates `AppColors` getters and that `normalizeThemeName` falls back correctly.

---

#### L-6. No test coverage for connect_screen.dart, status_bar.dart, input_bar.dart edge cases

**Files:** `lib/features/connect/connect_screen.dart`, `lib/features/status/status_bar.dart`, `lib/features/chat/input_bar.dart`

While `widget_test.dart` has some integration tests touching these, there are no isolated widget tests for:
- Connect screen error states (invalid URL, scanner failure)
- Status bar with long detail messages
- Input bar send with whitespace-only input
- Interrupt button behavior

---

#### L-7. `_connectInFlight` guard only matches identical URLs

**File:** `lib/core/providers/session_provider.dart`, lines 132-134

```dart
if (_connectInFlight != null && _connectInFlightUrl == url) {
  return _connectInFlight!;
}
```

If two different URLs are requested in rapid succession, both proceed concurrently, which is the race condition in C-2. This guard is insufficient as a deduplication mechanism.

---

#### L-8. ConnectionStateNotifier ref.onDispose only cancels replay watchdog

**File:** `lib/core/providers/session_provider.dart`, lines 93-96

```dart
@override
TunnelConnectionState build() {
  ref.onDispose(_cancelReplayWatchdog);
  return TunnelConnectionState(status: ConnectionStatus.disconnected);
}
```

On dispose, only the replay watchdog timer is cancelled. The stream subscriptions (C-1), heartbeat timer, and WebSocket are not cleaned up. This means provider disposal leaks the connection.

**Fix:** Dispose `service` and cancel all timers in the `ref.onDispose` callback.

---

## Architecture Observations

### God Object: session_provider.dart

At 3650 lines, `session_provider.dart` contains:
- `ConnectionNotifier` — WebSocket lifecycle, reconnect, resume/replay
- `ChatNotifier` — message CRUD, streaming, tool calls
- `WorkspaceCacheNotifier` — SQLite persistence, snapshot serialization
- 15+ provider definitions
- 10+ data model classes (`ChatMessage`, `SubagentInfo`, `ApprovalInfo`, etc.)
- Utility functions (`normalizeTunnelUrl`, `_workspaceDisplayName`)

This should be split into at least:
1. `connection_provider.dart` — WebSocket lifecycle only
2. `chat_provider.dart` — Message state management
3. `workspace_cache_provider.dart` — SQLite cache and snapshots
4. `models/` — Data classes extracted from providers

### Positive Notes

- **Test infrastructure is solid**: `_FakeConnectionService`, `_CaptureResumeHelloService`, etc. are well-structured test doubles.
- **43 session provider tests** cover replay recovery, deduplication, resume flows, permanent failure, server offline, and concurrent connect guards — this is excellent coverage for the most complex component.
- **Event deduplication** via `_recentEventSet`/`_recentEventIds` with 1000-entry eviction is a sound approach.
- **Replay recovery** with watchdog + exponential backoff + full-history fallback is well-designed.
- **ANSI rendering** in message bubbles is thorough (8-color, 16-color, 256-color support).

---

## Priority Fix Order

| Priority | ID | Effort | Impact |
|----------|-----|--------|--------|
| 1 | C-1 | S | Stream leak causes crashes on reconnect |
| 2 | C-2 | M | Race condition causes state corruption |
| 3 | H-3 | M | SQLite on main thread causes jank |
| 4 | H-1 | S | Unbounded message list causes OOM on long sessions |
| 5 | H-2 | S | Unbounded queue causes memory pressure |
| 6 | L-8 | S | Provider dispose incompleteness |
| 7 | H-4 | M | Theme statics can show stale colors |
| 8 | M-3 | S | Silent send failures |
| 9 | M-4 | S | Silent decrypt failures |

*S = Small (< 1 hour), M = Medium (1-4 hours), L = Large (> 4 hours)*
