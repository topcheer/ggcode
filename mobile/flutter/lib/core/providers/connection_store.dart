import 'dart:convert';
import 'dart:math';

import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

const _kConnectionsKey = 'ggcode_connections';

/// A persisted relay connection with its own independent resume state.
///
/// Each connection tracks its own session ID, last applied event ID, and
/// client ID — there is no global shared state between connections.
class StoredConnection {
  final String id;
  final String url;
  final String roomId;
  final String clientId;
  final String? sessionId;
  final String? lastEventId;
  final String? durableEventId;
  final String? workspacePath;
  final String? displayName;
  final String? providerName;
  final String? modelName;
  final bool active;
  final bool alive; // true = had live WebSocket (foreground or background) when app last ran
  final bool permanentlyFailed;
  final String? failReason;
  final DateTime createdAt;
  final DateTime? lastConnectedAt;

  StoredConnection({
    required this.id,
    required this.url,
    required this.roomId,
    required this.clientId,
    this.sessionId,
    this.lastEventId,
    this.durableEventId,
    this.workspacePath,
    this.displayName,
    this.providerName,
    this.modelName,
    this.active = false,
    this.alive = false,
    this.permanentlyFailed = false,
    this.failReason,
    required this.createdAt,
    this.lastConnectedAt,
  });

  /// Generate a new unique client ID for relay connections.
  static String generateClientId() {
    final rng = Random.secure();
    final bytes = List<int>.generate(16, (_) => rng.nextInt(256));
    return bytes
        .map((b) => b.toRadixString(16).padLeft(2, '0'))
        .join();
  }

  /// Create a fresh connection for a new share URL.
  factory StoredConnection.forUrl(String url, String roomId,
      {bool active = true}) {
    return StoredConnection(
      id: DateTime.now().microsecondsSinceEpoch.toString(),
      url: url,
      roomId: roomId,
      clientId: generateClientId(),
      active: active,
      createdAt: DateTime.now(),
    );
  }

  StoredConnection copyWith({
    String? url,
    String? sessionId,
    String? lastEventId,
    String? durableEventId,
    String? workspacePath,
    String? displayName,
    String? providerName,
    String? modelName,
    bool? active,
    bool? alive,
    bool? permanentlyFailed,
    String? failReason,
    DateTime? lastConnectedAt,
  }) {
    return StoredConnection(
      id: id,
      url: url ?? this.url,
      roomId: roomId,
      clientId: clientId,
      sessionId: sessionId ?? this.sessionId,
      lastEventId: lastEventId ?? this.lastEventId,
      durableEventId: durableEventId ?? this.durableEventId,
      workspacePath: workspacePath ?? this.workspacePath,
      displayName: displayName ?? this.displayName,
      providerName: providerName ?? this.providerName,
      modelName: modelName ?? this.modelName,
      active: active ?? this.active,
      alive: alive ?? this.alive,
      permanentlyFailed: permanentlyFailed ?? this.permanentlyFailed,
      failReason: failReason ?? this.failReason,
      createdAt: createdAt,
      lastConnectedAt: lastConnectedAt ?? this.lastConnectedAt,
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'url': url,
        'roomId': roomId,
        'clientId': clientId,
        'sessionId': sessionId,
        'lastEventId': lastEventId,
        'durableEventId': durableEventId,
        'workspacePath': workspacePath,
        'displayName': displayName,
        'providerName': providerName,
        'modelName': modelName,
        'active': active,
        'alive': alive,
        'permanentlyFailed': permanentlyFailed,
        'failReason': failReason,
        'createdAt': createdAt.toIso8601String(),
        'lastConnectedAt': lastConnectedAt?.toIso8601String(),
      };

  factory StoredConnection.fromJson(Map<String, dynamic> json) {
    return StoredConnection(
      id: json['id'] as String,
      url: json['url'] as String,
      roomId: json['roomId'] as String,
      clientId: json['clientId'] as String? ?? StoredConnection.generateClientId(),
      sessionId: json['sessionId'] as String?,
      lastEventId: json['lastEventId'] as String?,
      durableEventId: json['durableEventId'] as String?,
      workspacePath: json['workspacePath'] as String?,
      displayName: json['displayName'] as String?,
      providerName: json['providerName'] as String?,
      modelName: json['modelName'] as String?,
      active: json['active'] as bool? ?? false,
      alive: json['alive'] as bool? ?? false,
      permanentlyFailed: json['permanentlyFailed'] as bool? ?? false,
      failReason: json['failReason'] as String?,
      createdAt: DateTime.parse(json['createdAt'] as String),
      lastConnectedAt: json['lastConnectedAt'] != null
          ? DateTime.parse(json['lastConnectedAt'] as String)
          : null,
    );
  }
}

/// Persists all relay connections to SharedPreferences.
///
/// Each connection has fully independent resume state. On app start,
/// [loadAll] returns every non-failed connection for reconnection.
class ConnectionStore {
  static ConnectionStore? _instance;
  static ConnectionStore get instance {
    _instance ??= ConnectionStore._();
    return _instance!;
  }
  
  ConnectionStore._();
  
  factory ConnectionStore() => instance;

  /// Reset singleton state (for testing only).
  static void resetForTesting() {
    _instance = null;
  }

  List<StoredConnection> _connections = [];

  /// Load all connections from disk and deduplicate by sessionId.
  Future<void> load() async {
    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString(_kConnectionsKey);
    if (raw == null || raw.isEmpty) {
      _connections = [];
      return;
    }
    try {
      final list = jsonDecode(raw) as List<dynamic>;
      _connections = list
          .map((e) => StoredConnection.fromJson(e as Map<String, dynamic>))
          .toList();
      _deduplicate();
      // Cleanup stale connections on load — remove anything older than 6 hours
      // and permanently failed connections. Session data (CachedSessionRecord)
      // is NOT touched.
      await cleanupStale();
    } catch (_) {
      _connections = [];
    }
  }

  /// Remove duplicate connections for the same sessionId, keeping the
  /// newest one (by createdAt, fallback to lastConnectedAt).
  void _deduplicate() {
    final bySession = <String, StoredConnection>{};
    for (final c in _connections) {
      final key = c.sessionId ?? c.id;
      final existing = bySession[key];
      if (existing == null) {
        bySession[key] = c;
      } else {
        // Keep whichever has a renew_token URL (preferred for reconnect)
        final cHasRenew = c.url.contains('renew_token');
        final existingHasRenew = existing.url.contains('renew_token');
        if (cHasRenew && !existingHasRenew) {
          bySession[key] = c;
        } else if (!cHasRenew && existingHasRenew) {
          // keep existing
        } else {
          // Keep the newer one
          if (c.createdAt.isAfter(existing.createdAt)) {
            bySession[key] = c;
          }
        }
      }
    }
    final before = _connections.length;
    _connections = bySession.values.toList();
    if (_connections.length < before) {
      debugPrint('[store] deduplicated: $before -> ${_connections.length}');
    }
  }

  /// Save all connections to disk.
  Future<void> save() async {
    final prefs = await SharedPreferences.getInstance();
    final raw = jsonEncode(_connections.map((c) => c.toJson()).toList());
    await prefs.setString(_kConnectionsKey, raw);
  }

  /// All stored connections (including failed ones).
  List<StoredConnection> get all => List.unmodifiable(_connections);

  /// Active connections (not permanently failed).
  List<StoredConnection> get alive =>
      _connections.where((c) => !c.permanentlyFailed).toList();

  /// The active connection (shown in UI), or null.
  StoredConnection? get active =>
      _connections.where((c) => c.active && !c.permanentlyFailed).firstOrNull;

  /// Find a connection by room ID.
  StoredConnection? findByRoomId(String roomId) {
    return _connections.where((c) => c.roomId == roomId).firstOrNull;
  }

  /// Find a connection by ID.
  StoredConnection? findById(String id) {
    return _connections.where((c) => c.id == id).firstOrNull;
  }

  /// Find a connection by session ID.
  StoredConnection? findBySessionId(String sessionId) {
    return _connections
        .where((c) => c.sessionId == sessionId && !c.permanentlyFailed)
        .firstOrNull;
  }

  /// Connections that were alive (had live WebSocket) when the app last ran.
  /// These are the ONLY ones that should be restored on app restart.
  List<StoredConnection> get aliveConnections =>
      _connections.where((c) => c.alive && !c.permanentlyFailed && c.sessionId != null && c.sessionId!.isNotEmpty).toList();

  /// Mark a connection as alive (WebSocket established).
  Future<void> markAlive(String id) async {
    final idx = _connections.indexWhere((c) => c.id == id);
    if (idx >= 0) {
      _connections[idx] = _connections[idx].copyWith(alive: true);
      await save();
      debugPrint('[store] markAlive id=$id session=${_connections[idx].sessionId}');
    }
  }

  /// Mark a connection as dead (WebSocket closed or user disconnected).
  Future<void> markDead(String id) async {
    final idx = _connections.indexWhere((c) => c.id == id);
    if (idx >= 0) {
      debugPrint('[store] markDead id=$id session=${_connections[idx].sessionId}');
      _connections[idx] = _connections[idx].copyWith(alive: false);
      await save();
    }
  }

  /// Mark all connections as dead. Called on graceful app shutdown so
  /// they won't be auto-restored next time.
  Future<void> markAllDead() async {
    _connections = _connections.map((c) => c.copyWith(alive: false)).toList();
    await save();
  }

  /// Add a new connection. If [active] is true, demotes all others.
  /// If a connection for the same sessionId already exists, update it
  /// instead of creating a duplicate.
  Future<StoredConnection> add(StoredConnection conn) async {
    // Upsert by sessionId — don't create duplicates
    if (conn.sessionId != null && conn.sessionId!.isNotEmpty) {
      final idx = _connections.indexWhere((c) => c.sessionId == conn.sessionId);
      if (idx >= 0) {
        // Preserve original ID so markAlive/markDead can find it
        final origId = _connections[idx].id;
        _connections[idx] = conn.copyWith();
        _connections[idx] = StoredConnection(
          id: origId,
          url: conn.url.isNotEmpty ? conn.url : _connections[idx].url,
          roomId: _connections[idx].roomId,
          clientId: _connections[idx].clientId,
          sessionId: conn.sessionId ?? _connections[idx].sessionId,
          lastEventId: conn.lastEventId ?? _connections[idx].lastEventId,
          durableEventId: conn.durableEventId ?? _connections[idx].durableEventId,
          workspacePath: conn.workspacePath ?? _connections[idx].workspacePath,
          displayName: conn.displayName ?? _connections[idx].displayName,
          providerName: conn.providerName ?? _connections[idx].providerName,
          modelName: conn.modelName ?? _connections[idx].modelName,
          active: conn.active,
          alive: conn.alive,
          permanentlyFailed: conn.permanentlyFailed,
          failReason: conn.failReason ?? _connections[idx].failReason,
          createdAt: _connections[idx].createdAt,
          lastConnectedAt: conn.lastConnectedAt ?? _connections[idx].lastConnectedAt,
        );
        if (conn.active) {
          _connections = _connections
              .map((c) => c.id != conn.id && c.active ? c.copyWith(active: false) : c)
              .toList();
        }
        await save();
        return _connections[idx];
      }
    }
    if (conn.active) {
      _connections = _connections
          .map((c) => c.active ? c.copyWith(active: false) : c)
          .toList();
    }
    _connections.add(conn);
    await save();
    return conn;
  }

  /// Update a connection by ID.
  Future<void> update(String id, StoredConnection updated) async {
    final idx = _connections.indexWhere((c) => c.id == id);
    if (idx >= 0) {
      _connections[idx] = updated;
      await save();
    }
  }

  /// Mark a connection as active (demotes all others).
  Future<void> setActive(String id) async {
    _connections = _connections.map((c) {
      if (c.id == id) return c.copyWith(active: true);
      if (c.active) return c.copyWith(active: false);
      return c;
    }).toList();
    await save();
  }

  /// Mark a connection as permanently failed.
  Future<void> markFailed(String id, String reason) async {
    final idx = _connections.indexWhere((c) => c.id == id);
    if (idx >= 0) {
      _connections[idx] =
          _connections[idx].copyWith(permanentlyFailed: true, failReason: reason);
      await save();
    }
  }

  /// Remove a connection by ID.
  Future<void> remove(String id) async {
    _connections.removeWhere((c) => c.id == id);
    await save();
  }

  /// Remove all permanently failed connections.
  Future<void> clearFailed() async {
    _connections.removeWhere((c) => c.permanentlyFailed);
    await save();
  }

  /// Cleanup stale connections on startup.
  /// Removes connections older than 6 hours.
  /// Does NOT touch session data (CachedSessionRecord) — those may be shared.
  Future<void> cleanupStale({Duration maxAge = const Duration(hours: 6)}) async {
    final cutoff = DateTime.now().subtract(maxAge);
    final before = _connections.length;
    _connections.removeWhere((c) {
      // Keep if permanently failed (already handled by clearFailed)
      if (c.permanentlyFailed) return false; // will be cleaned separately
      // Remove if last connected before cutoff
      final lastConnected = c.lastConnectedAt ?? c.createdAt;
      return lastConnected.isBefore(cutoff);
    });
    // Also remove permanently failed ones while we're at it
    _connections.removeWhere((c) => c.permanentlyFailed);
    final removed = before - _connections.length;
    if (removed > 0) {
      debugPrint('[store] cleanupStale: removed $removed stale connections (${_connections.length} remaining)');
      await save();
    }
  }

  /// Mark a connection as permanently failed and remove it immediately.
  Future<void> markAndRemove(String id, String reason) async {
    debugPrint('[store] markAndRemove id=$id reason=$reason');
    _connections.removeWhere((c) => c.id == id);
    await save();
  }
}
