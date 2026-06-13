import 'dart:convert';
import 'dart:math';

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
  final String? workspacePath;
  final String? displayName;
  final String? providerName;
  final String? modelName;
  final bool active;
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
    this.workspacePath,
    this.displayName,
    this.providerName,
    this.modelName,
    this.active = false,
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
    String? sessionId,
    String? lastEventId,
    String? workspacePath,
    String? displayName,
    String? providerName,
    String? modelName,
    bool? active,
    bool? permanentlyFailed,
    String? failReason,
    DateTime? lastConnectedAt,
  }) {
    return StoredConnection(
      id: id,
      url: url,
      roomId: roomId,
      clientId: clientId,
      sessionId: sessionId ?? this.sessionId,
      lastEventId: lastEventId ?? this.lastEventId,
      workspacePath: workspacePath ?? this.workspacePath,
      displayName: displayName ?? this.displayName,
      providerName: providerName ?? this.providerName,
      modelName: modelName ?? this.modelName,
      active: active ?? this.active,
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
        'workspacePath': workspacePath,
        'displayName': displayName,
        'providerName': providerName,
        'modelName': modelName,
        'active': active,
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
      workspacePath: json['workspacePath'] as String?,
      displayName: json['displayName'] as String?,
      providerName: json['providerName'] as String?,
      modelName: json['modelName'] as String?,
      active: json['active'] as bool? ?? false,
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
  List<StoredConnection> _connections = [];

  /// Load all connections from disk.
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
    } catch (_) {
      _connections = [];
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

  /// Add a new connection. If [active] is true, demotes all others.
  Future<StoredConnection> add(StoredConnection conn) async {
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
}
