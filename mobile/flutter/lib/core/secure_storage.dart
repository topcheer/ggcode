/// Secure storage wrapper for sensitive data (tokens, connection URLs).
///
/// Uses flutter_secure_storage which delegates to:
/// - iOS: Keychain
/// - Android: EncryptedSharedPreferences (Jetpack Security)
///
/// Falls back to SharedPreferences with a timeout guard if Keychain
/// is unavailable (e.g. simulator deadlock).
library;

import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Centralized secure storage for token-bearing data.
class SecureTokenStorage {
  static SecureTokenStorage? _instance;
  static SecureTokenStorage get instance {
    _instance ??= SecureTokenStorage._();
    return _instance!;
  }

  SecureTokenStorage._({FlutterSecureStorage? storage})
      : _storage = storage ??
            const FlutterSecureStorage(
              iOptions: IOSOptions(
                accessibility: KeychainAccessibility.first_unlock,
              ),
            );

  /// For testing — inject a mock storage.
  factory SecureTokenStorage.forTesting(FlutterSecureStorage storage) {
    _instance = SecureTokenStorage._(storage: storage);
    return _instance!;
  }

  /// Reset singleton (for testing only).
  static void resetForTesting() {
    _instance = null;
  }

  /// Disable secure storage for testing — forces all reads/writes to
  /// use SharedPreferences immediately, avoiding 3s timeout timers
  /// that leak into widget tests.
  static void disableForTesting() {
    final s = SecureTokenStorage._(storage: const FlutterSecureStorage())
      .._disabledForTest = true;
    _instance = s;
  }

  final FlutterSecureStorage _storage;

  /// Timeout for secure storage operations. If exceeded, falls back to
  /// SharedPreferences to avoid blocking the UI.
  static const _timeout = Duration(seconds: 3);

  /// #931/#931-followup: a single transient Keychain error used to set a
  /// PROCESS-GLOBAL _secureAvailable=false - one session's failure degraded
  /// every other session's tokens to plaintext (cross-session leak) and the
  /// disableForTesting() bypass produced 3s timeout timers that hung widget
  /// tests. Health state is now tracked PER STORAGE KEY (each key namespace
  /// = an independent session-storage unit): one key's transient failure
  /// never degrades another. A successful read/write re-enables that key.
  static const _secureRetryAfter = Duration(minutes: 5);

  final Map<String, DateTime> _degradedUntilByKey = {};
  bool _disabledForTest = false;

  /// Whether secure storage should be attempted for this key right now.
  bool _shouldTrySecureFor(String key) {
    if (_disabledForTest) return false;
    final retryAt = _degradedUntilByKey[key];
    return retryAt == null || DateTime.now().isAfter(retryAt);
  }

  void _degradeSecureFor(String key) {
    _degradedUntilByKey[key] = DateTime.now().add(_secureRetryAfter);
  }

  /// Generic read with timeout fallback to SharedPreferences.
  Future<String?> _readSecure(String key, String fallbackKey) async {
    if (!_shouldTrySecureFor(key)) {
      final prefs = await SharedPreferences.getInstance();
      return prefs.getString(fallbackKey);
    }
    try {
      final result = await _storage.read(key: key).timeout(_timeout);
      debugPrint('[secure_storage] Keychain read OK: $key');
      _degradedUntilByKey.remove(key); // success re-enables
      return result;
    } on TimeoutException {
      debugPrint('[secure_storage] Keychain timed out, falling back to SharedPreferences');
      _degradeSecureFor(key);
      final prefs = await SharedPreferences.getInstance();
      return prefs.getString(fallbackKey);
    } catch (e) {
      debugPrint('[secure_storage] read error: $e, falling back to SharedPreferences');
      _degradeSecureFor(key);
      final prefs = await SharedPreferences.getInstance();
      return prefs.getString(fallbackKey);
    }
  }

  /// Generic write with timeout fallback to SharedPreferences.
  Future<void> _writeSecure(String key, String value, String fallbackKey) async {
    if (!_shouldTrySecureFor(key)) {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(fallbackKey, value);
      return;
    }
    try {
      await _storage.write(key: key, value: value).timeout(_timeout);
      debugPrint('[secure_storage] Keychain write OK: $key');
      _degradedUntilByKey.remove(key); // success re-enables
    } on TimeoutException {
      debugPrint('[secure_storage] Keychain timed out on write, falling back to SharedPreferences');
      _degradeSecureFor(key);
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(fallbackKey, value);
    } catch (e) {
      debugPrint('[secure_storage] write error: $e, falling back to SharedPreferences');
      _degradeSecureFor(key);
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(fallbackKey, value);
    }
  }

  // ── Connection store ──────────────────────────────────────────

  static const _connectionsKey = 'ggcode_connections_secure';
  static const _legacyConnectionsKey = 'ggcode_connections';

  /// Load connection JSON from secure storage, with one-time migration
  /// from legacy plaintext SharedPreferences.
  Future<String?> loadConnectionsJson() async {
    var raw = await _readSecure(_connectionsKey, _legacyConnectionsKey);
    if (raw != null) return raw;

    // Try legacy migration (from either SharedPreferences or secure storage).
    final prefs = await SharedPreferences.getInstance();
    raw = prefs.getString(_legacyConnectionsKey);
    if (raw != null && raw.isNotEmpty) {
      await _writeSecure(_connectionsKey, raw, _legacyConnectionsKey);
      try {
        await prefs.remove(_legacyConnectionsKey);
      } catch (_) {}
      debugPrint('[secure_storage] migrated connections from SharedPreferences');
    }
    return raw;
  }

  /// Save connection JSON to secure storage.
  Future<void> saveConnectionsJson(String json) async {
    await _writeSecure(_connectionsKey, json, _legacyConnectionsKey);
  }

  /// Delete connection data from secure storage.
  Future<void> deleteConnections() async {
    try {
      await _storage.delete(key: _connectionsKey).timeout(_timeout);
    } catch (_) {}
    final prefs = await SharedPreferences.getInstance();
    try {
      await prefs.remove(_legacyConnectionsKey);
    } catch (_) {}
  }

  // ── URL history ───────────────────────────────────────────────

  static const _historyKey = 'ggcode_history_secure';
  static const _legacyHistoryKey = 'ggcode_history';

  /// Load URL history from secure storage, with one-time migration.
  Future<List<String>> loadHistory() async {
    var raw = await _readSecure(_historyKey, _legacyHistoryKey);
    if (raw == null) {
      // Migrate from legacy SharedPreferences.
      final prefs = await SharedPreferences.getInstance();
      final legacy = prefs.getStringList(_legacyHistoryKey);
      if (legacy != null && legacy.isNotEmpty) {
        raw = jsonEncode(legacy);
        await _writeSecure(_historyKey, raw, _legacyHistoryKey);
        try {
          await prefs.remove(_legacyHistoryKey);
        } catch (_) {}
        debugPrint('[secure_storage] migrated URL history from SharedPreferences');
        return legacy;
      }
      return [];
    }
    try {
      final list = jsonDecode(raw) as List<dynamic>;
      return list.cast<String>();
    } catch (_) {
      return [];
    }
  }

  /// Save URL history to secure storage.
  Future<void> saveHistory(List<String> urls) async {
    await _writeSecure(_historyKey, jsonEncode(urls), _legacyHistoryKey);
  }
}
