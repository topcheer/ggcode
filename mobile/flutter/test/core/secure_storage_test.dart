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
}
