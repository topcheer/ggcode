# Round 8 Review: Flutter Mobile (`mobile/flutter/`)

**Reviewer**: flutter-agent  
**Date**: 2026-05-28  
**Scope**: `lib/` (19 files), `test/` (6 files), `ios/`, `android/`, `pubspec.yaml`  
**Classification**: Critical: 1 | High: 4 | Medium: 5 | Low: 4  

---

## Files Reviewed

### `lib/` — 19 Dart source files (all read in full)

| # | File | Lines | Purpose |
|---|------|-------|---------|
| 1 | `main.dart` | 269 | App entry, `_GGCodeApp` ConsumerStatefulWidget, routing, provider listeners |
| 2 | `core/connection_service.dart` | 420 | WebSocket tunnel client: connect, reconnect, heartbeat, AES-GCM encryption |
| 3 | `core/crypto.dart` | 67 | `TunnelCrypto` — AES-GCM encrypt/decrypt using token-derived key |
| 4 | `core/l10n/app_localizations.dart` | 59 | Language provider, JSON translation loader |
| 5 | `core/models/protocol.dart` | 533 | All WebSocket message types: `WsMessage`, 20+ data classes, `fromJson` factories |
| 6 | `core/providers/connection_provider.dart` | 1111 | `ConnectionNotifier` — connect/disconnect, event dispatch, ack tracking |
| 7 | `core/providers/chat_provider.dart` | 763 | `ChatNotifier` — message list CRUD, streaming, tool calls, reasoning blocks |
| 8 | `core/providers/ui_providers.dart` | 234 | Generic `_ValueNotifier<T>`, approval, ask-user, subagent, agent status providers |
| 9 | `core/providers/workspace_cache.dart` | 1369 | `WorkspaceCacheNotifier` — SQLite CRUD, session cache, snapshot persistence |
| 10 | `core/providers/session_provider.dart` | 5 | Barrel export re-exporting all 4 provider files |
| 11 | `core/theme/app_theme.dart` | 319 | Color palette, `ThemeData`, dark/light theme provider |
| 12 | `features/chat/chat_screen.dart` | 1193 | Main chat UI: message list, session picker, workspace scanner, tool cards |
| 13 | `features/chat/input_bar.dart` | 190 | Message input with send button, busy pulse animation |
| 14 | `features/chat/message_bubble.dart` | 670 | Message rendering: markdown, code blocks, Mermaid, reasoning, tool results |
| 15 | `features/chat/approval_sheet.dart` | 101 | Bottom sheet for tool approval (allow/deny) |
| 16 | `features/chat/subagent_panel.dart` | 207 | Floating sub-agent activity cards |
| 17 | `features/chat/ask_user_screen.dart` | 330 | Full-screen questionnaire for `ask_user` tool responses |
| 18 | `features/connect/connect_screen.dart` | 364 | QR scanner + manual URL input for connecting to tunnel relay |
| 19 | `features/status/status_bar.dart` | 113 | Connection status + agent activity indicator |

### `test/` — 6 test files

| # | File | Scope |
|---|------|-------|
| 1 | `connection_service_test.dart` | 55 lines — crypto roundtrip, `normalizeTunnelUrl`, `isPermanentRoomFailureMessage` |
| 2 | `protocol_test.dart` | ~80 lines — `WsMessage.fromJson` roundtrip, edge cases |
| 3 | `app_test.dart` | ~30 lines — smoke test: app starts without crash |
| 4 | `widget_test.dart` | ~40 lines — verify `ChatScreen` renders |
| 5 | `session_provider_test.dart` | ~1667 lines — comprehensive: chat streaming, finalization, reasoning, subagent events, workspace cache SQLite CRUD, session switching, snapshot persistence |
| 6 | `l10n_assets_test.dart` | ~35 lines — translation JSON files exist for `en`/`zh-CN` |

---

## Critical

### C-1: Four stream subscriptions leaked on every reconnect in `ConnectionNotifier._connectImpl()`

**File**: `lib/core/providers/connection_provider.dart:165-209`

Four `.listen()` calls on `service!.statusStream`, `errorStream`, `messageStream`, and `ackStream` create `StreamSubscription` objects that are **never stored in instance fields** and therefore **cannot be cancelled**. Each call to `_connectImpl()` — triggered by initial connect, reconnect, or workspace switch — creates four new subscriptions while the previous ones remain active.

```dart
// Lines 165-195 — return values discarded:
service!.statusStream.listen((status) { ... });   // NOT stored
service!.errorStream.listen((error) { ... });      // NOT stored
service!.messageStream.listen((msg) => _dispatchMessage(msg)); // NOT stored
service!.ackStream.listen((ack) { ... });           // NOT stored
```

Contrast with `connection_service.dart:120` where `_socketSub` is properly stored:
```dart
_socketSub = _socket!.listen(...);  // stored, cancelled in _cleanup()
```

**Impact**: After N reconnect cycles, 4N active subscriptions all fire on the same events:
- Duplicate chat messages rendered (each `messageStream` listener calls `_dispatchMessage`)
- Duplicate ack status updates (each `ackStream` listener calls `chatNotifier.updateMessageStatus`)
- Duplicate error toasts
- Memory leak: closures capture `ref`, preventing GC of old `ConnectionNotifier` instances

**Fix**: Store all four as instance fields (`_statusSub`, `_errorSub`, `_messageSub`, `_ackSub`). Cancel them at the top of `_connectImpl()` before re-subscribing. Cancel them in `ref.onDispose()`.

---

## High

### H-1: `ConnectionNotifier` has no `ref.onDispose()` — service never cleaned up

**File**: `lib/core/providers/connection_provider.dart:56-59`

```dart
@override
TunnelConnectionState build() {
  // No ref.onDispose(() { ... }) anywhere in the class
  return TunnelConnectionState(status: ConnectionStatus.disconnected);
}
```

`connectionProvider` is a `NotifierProvider` (not autoDispose), so it lives for the full app lifecycle. The `service` field (a `TunnelConnectionService`) is lazily created in `_connectImpl()` but never disposed. Compare with `WorkspaceCacheNotifier` which correctly has `ref.onDispose()` at line 634.

While the service persists for the app lifetime by design, the lack of `onDispose` means:
- In tests, `ProviderContainer` disposal never closes the WebSocket
- Hot restart during development leaks old WebSocket connections and timers
- The four leaked subscriptions from C-1 have no cancellation path through provider lifecycle

**Fix**: Add `ref.onDispose()` to cancel stream subscriptions and call `service?.dispose()`.

### H-2: `Future.delayed` in `ChatNotifier.addUserMessage()` is untracked and fires after provider disposal

**File**: `lib/core/providers/chat_provider.dart:183-197`

```dart
void addUserMessage(String text, {String? displayOverride}) {
  // ...
  Future.delayed(const Duration(seconds: 5), () {
    final idx = state.indexWhere((m) => m.id == messageId);
    if (idx != -1 && state[idx].status == MessageStatus.sent) {
      state = [
        for (int i = 0; i < state.length; i++)
          if (i == idx)
            state[i].copyWith(status: MessageStatus.timeout)
          else
            state[i],
      ];
    }
  });
}
```

The `Future.delayed` closure captures `state` and mutates it after 5 seconds. There is:
- No guard checking `ref.mounted` (unlike `WorkspaceCacheNotifier` which checks `ref.mounted` in 19 places)
- No `Timer` stored for cancellation
- No generation counter to detect stale state

If the user switches workspaces or the provider is recreated during the 5-second window, the callback mutates stale state or throws `StateError` on a disposed provider.

**Fix**: Store as a `Timer`, cancel in `ref.onDispose()`, or add `ref.mounted` guard.

### H-3: `Future.delayed` in `ConnectionNotifier._dispatchMessage()` auto-removes subagent without lifecycle guard

**File**: `lib/core/providers/connection_provider.dart:682-692`

```dart
Future.delayed(const Duration(seconds: 5), () {
  if (!ref.mounted) return;  // ← good: ref.mounted check exists
  // ... but only one of the two paths checks it
  ref.read(subagentProvider.notifier).removeSubagent(data.agentId);
});
```

This one does have a `ref.mounted` check (line 683), but the `ref.read(subagentProvider.notifier)` call can still fail if the subagent was already removed by another event. More importantly, this is a `Future.delayed` inside a Notifier method — if multiple `subagent_complete` events arrive, each schedules an independent 5-second delay, and all fire independently.

**Fix**: Track pending auto-removal timers per `agentId` in a `Map<String, Timer>` and cancel on workspace switch.

### H-4: Unbounded `ChatNotifier.state` list with O(n) copy on every mutation

**File**: `lib/core/providers/chat_provider.dart` (entire class)

Every state mutation uses `state = [for (...) ...]` or `state = [...state, newMsg]`, creating a full list copy each time. During heavy streaming (agent produces thousands of tool call/result pairs), this means:

- O(n) memory allocation per streaming text chunk (line 85: `state = [for (int i = 0; i < state.length; i++) ...]`)
- `toolPayload` and `toolResult` fields can contain full file contents (multi-KB each)
- No upper bound on message count — a long-running session accumulates without limit
- `displayedMessagesProvider` in `ui_providers.dart` filters to the current session but still operates on the full list

**Impact**: Memory pressure on extended sessions; potential UI jank during rapid streaming.

**Fix**: Cap at a configurable maximum (e.g., 500 messages per session) with oldest-first eviction, or paginate historical messages.

---

## Medium

### M-1: Stale `MainActivity.kt` at old package path not cleaned up

**Files**: 
- `android/app/src/main/kotlin/gg/ai/ggcode/mobile/MainActivity.kt` (active, matches `namespace` in `build.gradle.kts`)
- `android/app/src/main/kotlin/gg/ai/ggcode/ggcode_mobile/MainActivity.kt` (stale, package `gg.ai.ggcode.ggcode_mobile`)

The second file is dead code from a package rename. It compiles (different package) but is never loaded by the Android manifest.

**Fix**: Delete `gg/ai/ggcode/ggcode_mobile/MainActivity.kt`.

### M-2: Android release builds ship without code shrinking or resource compression

**File**: `android/app/build.gradle.kts:49-53`

```kotlin
release {
    signingConfig = signingConfigs.getByName("release")
    isMinifyEnabled = false      // ← R8/code shrinking disabled
    isShrinkResources = false    // ← resource shrinking disabled
}
```

Release APK/AAB ships with full debug symbols, unused library code, and uncompressed resources. For a production app, this unnecessarily inflates binary size.

**Fix**: Enable `isMinifyEnabled = true` and `isShrinkResources = true`. Add ProGuard/R8 rules if needed.

### M-3: `WorkspaceCacheNotifier._scheduleFlush()` missing debounce — potential duplicate flushes

**File**: `lib/core/providers/workspace_cache.dart:1053-1062`

```dart
void _scheduleFlush() {
  if (!ref.mounted) return;
  _flushTimer?.cancel();
  _flushTimer = Timer(const Duration(seconds: 2), () {
    _flushTimer = null;
    _doFlush();
  });
}
```

Actually, on re-reading, the cancel-before-create pattern **is correct** — the timer IS cancelled before being recreated. This is properly debounced. Downgrading from my earlier assessment.

### M-4: Silent `catch (_)` in `WorkspaceCacheNotifier` SQLite operations could hide data corruption

**File**: `lib/core/providers/workspace_cache.dart:562`

```dart
} catch (_) {
  _db.execute('ROLLBACK');
}
```

The SQLite transaction in `_doFlushWorkspaces()` catches all exceptions, rolls back, and silently continues. If the database is corrupted or the schema has migrated incorrectly, this silently loses data with no diagnostic output.

Similar silent catches at:
- `workspace_cache.dart:285` — path resolution fallback (acceptable)
- `chat_provider.dart:554,573,592` — JSON formatting fallbacks (acceptable for display)
- `message_bubble.dart:410` — color parsing fallback (acceptable)
- `l10n/app_localizations.dart:40` — translation load fallback (acceptable)

**Fix**: Add `debugPrint` or `developer.log` in the SQLite catch blocks so data corruption is diagnosable. The cosmetic/UI catches are fine as-is.

### M-5: `ui_providers.dart` `_ValueNotifier` pattern does not call `ref.onDispose()`

**File**: `lib/core/providers/ui_providers.dart:5-30`

The generic `_ValueNotifier<T>` class used for `approvalProvider`, `askUserProvider`, `subagentProvider`, `sessionInfoProvider`, `agentStatusProvider`, etc. has no `ref.onDispose()`. Since these are plain `NotifierProvider` (not autoDispose), they persist for the app lifetime. This is acceptable for app-wide state, but `approvalProvider` and `askUserProvider` are scoped to specific UI flows and could benefit from auto-dispose when their screens are popped.

**Fix**: Consider making `approvalProvider` and `askUserProvider` auto-dispose to prevent stale approval/ask-user state from persisting across navigations.

---

## Low

### L-1: `pubspec.yaml` SDK constraint `>=3.0.0 <4.0.0` is overly broad

**File**: `pubspec.yaml:7`

```yaml
environment:
  sdk: '>=3.0.0 <4.0.0'
```

The codebase uses Dart 3.x features (records, patterns, switch expressions) that require at minimum Dart 3.3+. Setting the floor at 3.0.0 means the pub solver could resolve to an incompatible SDK.

**Fix**: Tighten to `>=3.3.0 <4.0.0` or whatever minimum version is actually tested in CI.

### L-2: `analysis_options.yaml` has no project-specific lint rules

**File**: `analysis_options.yaml`

```yaml
include: package:flutter_lints/flutter.yaml
```

Missing lint rules that would catch the Critical finding:
- `cancel_subscriptions` — would flag the uncancelled `.listen()` return values in C-1
- `close_sinks` — would flag unclosed stream controllers
- `avoid_print` — enforce `debugPrint` usage
- `unnecessary_statements`

**Fix**: Add a `linter` section with `cancel_subscriptions` and `close_sinks`.

### L-3: iOS Podfile targets iOS 13.0 — too low for ML Kit dependencies

**File**: `ios/Podfile:2`

```ruby
platform :ios, '13.0'
```

`mobile_scanner` v7.x uses ML Kit which recommends iOS 14+. iOS 13 has been deprecated by Apple and ML Kit APIs may not function correctly.

**Fix**: Raise to `platform :ios, '14.0'`.

### L-4: No evidence of CI workflow for Flutter tests

No `.github/workflows/*.yml` targeting `mobile/flutter/` was found. The 6 test files exist but there's no automated enforcement. The Go project has CI but the Flutter project appears untested in automation.

**Fix**: Add a GitHub Actions workflow running `flutter test` and `flutter analyze`, or document the manual testing process.

---

## Test Coverage Assessment

| Source File | Test File | Coverage |
|---|---|---|
| `core/connection_service.dart` | `connection_service_test.dart` | Partial — crypto, URL normalization, error detection. No reconnect/heartbeat/dispose tests |
| `core/models/protocol.dart` | `protocol_test.dart` | Good — `fromJson` roundtrips for key message types |
| `core/providers/chat_provider.dart` | `session_provider_test.dart` | Good — streaming, finalization, reasoning, tool calls, subagent events |
| `core/providers/connection_provider.dart` | `session_provider_test.dart` (partial) | Moderate — event dispatch tested via session tests |
| `core/providers/workspace_cache.dart` | `session_provider_test.dart` | Good — SQLite CRUD, selection, snapshots, session switching |
| `core/providers/ui_providers.dart` | None | No coverage |
| `core/crypto.dart` | `connection_service_test.dart` (partial) | Basic encrypt/decrypt roundtrip |
| `core/l10n/app_localizations.dart` | `l10n_assets_test.dart` | Asset existence only |
| `core/theme/app_theme.dart` | None | No coverage |
| `features/chat/chat_screen.dart` | `widget_test.dart` | Minimal — renders without crash |
| `features/chat/input_bar.dart` | None | No coverage |
| `features/chat/message_bubble.dart` | None | No coverage |
| `features/chat/approval_sheet.dart` | None | No coverage |
| `features/chat/subagent_panel.dart` | None | No coverage |
| `features/chat/ask_user_screen.dart` | None | No coverage |
| `features/connect/connect_screen.dart` | None | No coverage |
| `features/status/status_bar.dart` | None | No coverage |
| `main.dart` | `app_test.dart` | Smoke test only |

**Summary**: Provider logic is well-tested (~1700 lines in `session_provider_test.dart`). Zero widget-level tests for any screen. No integration test for the full connect-stream-disconnect-reconnect lifecycle. No test for the C-1 subscription leak scenario.

---

## Platform-Specific Code

### MethodChannel / Native Bridges
No `MethodChannel`, `EventChannel`, or `BasicMessageChannel` usage found anywhere in the codebase. The app is pure Dart/Flutter. Both native entry points are standard boilerplate:
- `ios/Runner/AppDelegate.swift` — standard `FlutterAppDelegate`
- `android/.../MainActivity.kt` — standard `FlutterActivity()`

This is appropriate for a WebSocket client with camera scanning (handled by `mobile_scanner` plugin).

### iOS Configuration
- Camera entitlement with user-facing description: present and correct
- `ITSAppUsesNonExemptEncryption = false`: correct
- No `NSAppTransportSecurity` override — the app connects via `ws://` (not `wss://`). On physical iOS devices, ATS may block cleartext WebSocket connections. Note: the URL entry field allows `ws://` and `wss://`, but there's no ATS exception declared for `ws://`.
- Portrait + both landscape orientations supported

### Android Configuration
- `compileSdk = 36`, `targetSdk = 35`, `minSdk = 24` — modern and reasonable
- Permissions: `INTERNET`, `CAMERA` — minimal, appropriate
- `hardwareAccelerated = true` on activity
- Release signing via `key.properties` — properly configured
- `android:exported="true"` with intent filter — correct for launcher activity
- Kotlin + Java 17 target

---

## Dependency Audit

| Package | Version | Status |
|---|---|---|
| `flutter_riverpod` | `^3.0.0` | Current major |
| `web_socket_channel` | `^3.0.0` | Current |
| `mobile_scanner` | `^7.0.0` | Current (ML Kit camera) |
| `flutter_markdown_plus` | `^1.0.0` | Current |
| `highlight` | `^0.7.0` | Stable |
| `flutter_highlight` | `^0.7.0` | Stable |
| `shared_preferences` | `^2.2.2` | Current |
| `uuid` | `^4.2.2` | Current |
| `url_launcher` | `^6.2.2` | Current |
| `google_fonts` | `^8.0.0` | Current |
| `animations` | `^2.0.8` | Current |
| `flutter_mermaid` | `^0.1.0` | **Young dependency** — watch for breaking changes |
| `cryptography` | `^2.9.0` | Current (AES-GCM tunnel encryption) |
| `wakelock_plus` | `^1.6.0` | Current |
| `path` | `^1.9.1` | Current |
| `path_provider` | `^2.1.5` | Current |
| `sqlite3` | `^2.9.3` | Current (workspace cache) |
| `sqlite3_flutter_libs` | `^0.5.39` | Current |
| `flutter_lints` | `^6.0.0` | Current (dev) |

No known vulnerabilities. `flutter_mermaid ^0.1.0` is the youngest dependency — consider pinning or monitoring.

---

## Positive Observations

1. **Clean architecture**: Feature-based folder structure with clear separation (`core/`, `features/`, `widgets/`).
2. **Crypto correctness**: `TunnelCrypto` uses AES-256-GCM with proper 12-byte nonce, derived from SHA-256 of the tunnel token. Nonce is never reused.
3. **Widget lifecycle**: All `TextEditingController` (connect_screen:47, ask_user_screen:193), `ScrollController` (chat_screen:23), and `AnimationController` (chat_screen:609, input_bar:18) instances are properly disposed in `dispose()`.
4. **ConnectionService cleanup**: `_cleanup()` (line 324), `disconnect()` (line 396), and `dispose()` (line 407) properly cancel timers, subscriptions, and close stream controllers. The `_disposed` flag prevents double-cleanup.
5. **Ordered message processing**: `ConnectionService` uses `_queue` chain (line 126) to serialize message handling, preventing race conditions.
6. **WorkspaceCacheNotifier**: SQLite-backed with 19 `ref.mounted` guards, proper `ref.onDispose()`, and correct flush timer debounce (cancel-before-create).
7. **Reconnection logic**: Exponential backoff with jitter, max attempt cap, server-offline recovery with 30s delay.
8. **Event deduplication**: `_recentEventIds` / `_recentEventSet` prevent duplicate processing on reconnect.
9. **Secrets management**: `.gitignore` properly excludes `.env`, `secrets/`, keystores, `.p8`, `.p12`, `.pem`, `key.properties`, service account JSON.
10. **Ack tracking**: User messages get delivery status progression (sending -> sent -> delivered/acknowledged/timeout) with 5-second fallback.
11. **L10n**: Translation files verified present for `en` and `zh-CN` with fallback chain.
12. **Protocol model**: Clean `fromJson` factories with defensive `as String? ?? ''` defaults throughout.
