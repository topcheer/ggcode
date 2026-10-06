import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:ggcode_mobile/core/secure_storage.dart';

/// A FlutterSecureStorage mock that intercepts read/write/delete via
/// noSuchMethod so it never drifts from the plugin's evolving signatures.
class _MockSecureStorage implements FlutterSecureStorage {
  final Map<String, String> store = {};
  final Map<String, Duration> delays = {}; // key -> one-shot write delay
  final Set<String> failWrites = {}; // one-shot write failures

  dynamic _arg(Invocation i, Symbol name) {
    final v = i.namedArguments[name];
    if (v is String) return v;
    return null;
  }

  @override
  dynamic noSuchMethod(Invocation i) {
    final key = _arg(i, #key) as String?;
    switch (i.memberName) {
      case #read:
        return Future.value(key == null ? null : store[key]);
      case #write:
        final value = _arg(i, #value) as String?;
        if (key != null && failWrites.remove(key)) {
          return Future.error(Exception('keychain unavailable'));
        }
        final delay = key == null ? null : delays.remove(key);
        Future<void> body() async {
          if (delay != null) await Future<void>.delayed(delay);
          if (key != null && value != null) store[key] = value;
        }

        return body();
      case #delete:
        if (key != null) store.remove(key);
        return Future.value();
      default:
        return super.noSuchMethod(i);
    }
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
    SecureTokenStorage.resetForTesting();
  });

  test('#2814: secure write after a degraded window removes the plaintext '
      'fallback copy', () async {
    final mock = _MockSecureStorage();
    final storage = SecureTokenStorage.forTesting(mock as FlutterSecureStorage);
    storage.secureRetryAfter = Duration.zero;

    // Force one degraded (fallback) write of connection JSON.
    mock.failWrites.add('ggcode_connections_secure');
    await storage.saveConnectionsJson('{"url":"wss://x","renew_token":"t1"}');
    final prefs = await SharedPreferences.getInstance();
    final legacy = prefs.getString('ggcode_connections');
    expect(legacy, isNotNull, reason: 'degraded write must land in fallback');

    // Next successful secure write carries the newest value - the obsolete
    // plaintext copy must be removed (#2814), not left behind forever.
    await storage.saveConnectionsJson('{"url":"wss://x","renew_token":"t2"}');
    expect(mock.store["ggcode_connections_secure"], contains("t2"));
    final legacyAfter = await prefs.getString('ggcode_connections');
    expect(legacyAfter, isNull, reason: 'plaintext fallback must be scrubbed after secure write (#2814)');
  });

  test('#2814: heal path removes the plaintext fallback copy after writing '
      'it back to Keychain', () async {
    final mock = _MockSecureStorage();
    final storage = SecureTokenStorage.forTesting(mock as FlutterSecureStorage);
    storage.secureRetryAfter = Duration.zero;

    // Seed: secure holds old value; a fallback write during a degraded
    // window holds the NEWER value.
    mock.store["ggcode_connections_secure"] = "{\"v\":1}";
    mock.failWrites.add('ggcode_connections_secure');
    await storage.saveConnectionsJson('{"v":2}');
    final prefs = await SharedPreferences.getInstance();
    expect(prefs.getString('ggcode_connections'), '{"v":2}');

    // A read once the cooldown expired runs the heal: fallback value goes
    // back into the secure store AND the plaintext copy is scrubbed.
    final raw = await storage.loadConnectionsJson();
    expect(raw, contains('"v":2'));
    expect(mock.store["ggcode_connections_secure"], "{\"v\":2}");
    final legacyAfter = await prefs.getString('ggcode_connections');
    expect(legacyAfter, isNull, reason: 'heal must scrub the plaintext copy (#2814)');
  });

  test('#1872 case 2: edits during a degraded window survive cooldown expiry '
      '(the stale Keychain value must not resurrect)', () async {
    final mock = _MockSecureStorage();
    final storage = SecureTokenStorage.forTesting(mock as FlutterSecureStorage);
    // Cooldown expires immediately so the NEXT read takes the secure path
    // and runs the #1872 case 2 arbitration (last-write-wins + heal).
    storage.secureRetryAfter = Duration.zero;

    // Seed the Keychain with the pre-degradation value.
    await storage.saveConnectionsJson('{"v":1}');
    expect(mock.store['ggcode_connections_secure'], '{"v":1}');

    // Next write times out -> falls back to prefs; the Keychain keeps the
    // OLD value (the timed-out write never landed).
    mock.delays['ggcode_connections_secure'] = const Duration(seconds: 30);
    await SecureTokenStorage.instance.saveConnectionsJson('{"v":2}');

    // Recovery: the key is healthy again (no delay on the next write - the
    // heal write inside arbitration must be able to land).
    mock.delays.remove('ggcode_connections_secure');

    // A later successful secure read must arbitrate to the NEWER fallback
    // value and heal the secure store.
    final raw = await SecureTokenStorage.instance.loadConnectionsJson();
    expect(raw, '{"v":2}',
        reason: 'the newer fallback value written during degradation must win');
    expect(mock.store['ggcode_connections_secure'], '{"v":2}',
        reason: 'arbitration must heal the secure store');
  });

  test('#1872 case 3: history fallback writes go to a distinct string key - '
      'the legacy StringList key is never rewritten with a String', () async {
    final mock = _MockSecureStorage();
    SecureTokenStorage.forTesting(mock as FlutterSecureStorage);

    // First save populates secure normally.
    await SecureTokenStorage.instance.saveHistory(['https://a']);
    expect(mock.store['ggcode_history_secure'], isNotNull);

    // Next history write fails -> fallback goes to ggcode_history_fallback
    // (a String), NOT ggcode_history (the legacy StringList key).
    mock.failWrites.add('ggcode_history_secure');
    await SecureTokenStorage.instance.saveHistory(['https://a', 'https://b']);

    final prefs = await SharedPreferences.getInstance();
    expect(prefs.getString('ggcode_history_fallback'), isNotNull,
        reason: 'string fallback must use its own key');
    expect(prefs.getStringList('ggcode_history'), isNull,
        reason: 'the legacy StringList key must never hold a JSON String');

    // And the read path recovers the newer history via arbitration.
    final history = await SecureTokenStorage.instance.loadHistory();
    expect(history, ['https://a', 'https://b']);
  });

  // #3452: the heal write itself may fail (quota/ACL/timeout right after a
  // successful read). The #2814 cleanup deleted the plaintext fallback copy
  // unconditionally - destroying the ONLY persistent copy of the user's
  // newer value. The fix keeps copy + flag on heal failure so a later read
  // retries (write-path discipline: delete only after secure persistence).
  test('#3452: failed heal keeps the plaintext copy and retries later',
      () async {
    final mock = _MockSecureStorage();
    final storage = SecureTokenStorage.forTesting(mock as FlutterSecureStorage);
    storage.secureRetryAfter = Duration.zero;

    // Seed: secure holds old; a degraded write lands the NEWER value in the
    // fallback (consumes the first one-shot write failure).
    mock.store["ggcode_connections_secure"] = "{\"v\":1}";
    mock.failWrites.add('ggcode_connections_secure');
    await storage.saveConnectionsJson('{"v":2}');
    final prefs = await SharedPreferences.getInstance();
    expect(prefs.getString('ggcode_connections'), '{"v":2}');

    // Re-arm the one-shot failure so the HEAL write fails too.
    mock.failWrites.add('ggcode_connections_secure');
    final raw = await storage.loadConnectionsJson();
    expect(raw, contains('"v":2'), reason: 'newer fallback value still wins');
    expect(prefs.getString('ggcode_connections'), '{"v":2}',
        reason: '#3452: failed heal MUST keep the only persistent copy');

    // Recovery: a later read with a healthy store retries the heal, lands
    // the newer value in the secure store, and only then scrubs the copy.
    final raw2 = await storage.loadConnectionsJson();
    expect(raw2, contains('"v":2'));
    expect(mock.store["ggcode_connections_secure"], '{"v":2}',
        reason: 'kept flag must retry the heal on a later read');
    expect(prefs.getString('ggcode_connections'), isNull,
        reason: 'successful heal still scrubs the copy (#2814 intact)');
  });
}
