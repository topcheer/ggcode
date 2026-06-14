import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../connection_service.dart';
import '../models/protocol.dart' as proto;
import 'connection_store.dart';
import 'workspace_cache.dart';

/// Manages background WebSocket connections for non-active sessions.
///
/// Every connection (foreground or background) receives messages and
/// persists them to the session's SQLite cache via [appendSessionEvent].
/// When a background connection is promoted to foreground via [adoptService],
/// the UI loads already-persisted messages from cache.
class BackgroundConnectionManager extends Notifier<void> {
  final Map<String, ConnectionService> _services = {};
  final Map<String, List<StreamSubscription<dynamic>>> _subscriptions = {};
  final Map<String, String> _sessionIdToUrl = {};
  final Set<String> _liveSessionIds = {};

  @override
  void build() {
    ref.onDispose(_disposeAll);
  }

  /// Connect a new background session.
  Future<void> connect({
    required String url,
    required String sessionId,
  }) async {
    if (_liveSessionIds.contains(sessionId)) {
      debugPrint('[bg-conn] session $sessionId already connected');
      return;
    }

    debugPrint('[bg-conn] connecting session=$sessionId');
    final descriptor = ShareConnectionDescriptor.parse(url);
    final svc = ConnectionService(descriptor: descriptor);
    _services[sessionId] = svc;
    _sessionIdToUrl[sessionId] = url;

    _wireService(sessionId, svc);
    await svc.connect();
    _liveSessionIds.add(sessionId);
  }

  void _wireService(String sessionId, ConnectionService svc) {
    _subscriptions[sessionId]?.forEach((s) => s.cancel());
    _subscriptions[sessionId] = [
      svc.messageStream.listen((msg) {
        _handleBackgroundMessage(sessionId, msg);
      }),
      svc.statusStream.listen((status) {
        debugPrint('[bg-conn] session=$sessionId status=$status');
        if (status == ConnectionStatus.connected) {
          _liveSessionIds.add(sessionId);
          _updateConnectionAlive(sessionId, true);
        } else if (status == ConnectionStatus.disconnected) {
          // DON'T markDead here — WebSocket disconnect can happen due to
          // network issues, app backgrounding, or relay restart. We only
          // mark dead when the user explicitly disconnects.
          // Keeping alive=true allows app restart to reconnect.
          _liveSessionIds.remove(sessionId);
        }
      }),
    ];
  }

  /// Update the stored connection's alive flag so app restart only
  /// restores connections that were genuinely active.
  void _updateConnectionAlive(String sessionId, bool alive) {
    final store = ConnectionStore.instance;
    store.load().then((_) {
      final conn = store.findBySessionId(sessionId);
      if (conn != null) {
        if (alive) {
          store.markAlive(conn.id);
        } else {
          store.markDead(conn.id);
        }
      }
    });
  }

  /// Process a message from a background connection.
  /// Persist it to the session's cache immediately.
  void _handleBackgroundMessage(String sessionId, proto.WsMessage msg) {
    final cache = ref.read(workspaceCacheProvider.notifier);
    cache.appendSessionEvent(
      sessionId: sessionId,
      eventType: msg.type,
      eventData: Map<String, dynamic>.from(msg.data ?? {}),
      eventId: msg.eventId,
    );
    cache.cacheBackgroundEvent(
      sessionId: sessionId,
      eventType: msg.type,
      eventData: Map<String, dynamic>.from(msg.data ?? {}),
    );
  }

  /// Take ownership of a background connection.
  ConnectionService? adoptService(String sessionId) {
    final svc = _services.remove(sessionId);
    if (svc != null) {
      _subscriptions[sessionId]?.forEach((s) => s.cancel());
      _subscriptions.remove(sessionId);
      _liveSessionIds.remove(sessionId);
      _sessionIdToUrl.remove(sessionId);
    }
    return svc;
  }

  /// Register a foreground service as a background connection (demote).
  void registerService({
    required String sessionId,
    required ConnectionService svc,
    required String url,
  }) {
    _services[sessionId]?.dispose();
    _services[sessionId] = svc;
    _sessionIdToUrl[sessionId] = url;
    _wireService(sessionId, svc);
    _liveSessionIds.add(sessionId);
    debugPrint('[bg-conn] demoted session=$sessionId to background');
  }

  /// Reconnect sessions that were alive (had live WebSocket) when the app
  /// last ran. NOT all cached sessions — only the ones that were actively
  /// connected before the app died.
  Future<void> connectAllCachedSessions() async {
    final cache = ref.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    final cacheState = ref.read(workspaceCacheProvider);

    // Use ConnectionStore singleton (with dedup) instead of raw SharedPreferences
    final store = ConnectionStore.instance;
    await store.load();
    final allConnections = store.all;
    debugPrint('[bg-conn] connectAllCachedSessions: ${allConnections.length} connections after dedup selected=${cacheState.selectedSessionId}');
    for (final conn in allConnections) {
      debugPrint('[bg-conn]   conn: session=${conn.sessionId ?? ""} alive=${conn.alive} failed=${conn.permanentlyFailed} url=${conn.url.isNotEmpty ? "yes" : "no"}');

      if (!conn.alive || conn.permanentlyFailed) continue;
      final sessionId = conn.sessionId ?? '';
      final url = conn.url;
      if (sessionId.isEmpty || url.isEmpty) continue;
      if (sessionId == cacheState.selectedSessionId) {
        debugPrint('[bg-conn]   skipping selected (will be foreground)');
        continue;
      }

      debugPrint('[bg-conn] restoring alive session=$sessionId');
      await connect(url: url, sessionId: sessionId);
    }
  }

  void disconnect(String sessionId) {
    _subscriptions[sessionId]?.forEach((s) => s.cancel());
    _subscriptions.remove(sessionId);
    _services[sessionId]?.dispose();
    _services.remove(sessionId);
    _liveSessionIds.remove(sessionId);
    _sessionIdToUrl.remove(sessionId);
  }

  bool isLive(String sessionId) => _liveSessionIds.contains(sessionId);
  Set<String> get liveSessionIds => Set.unmodifiable(_liveSessionIds);
  String? urlForSession(String sessionId) => _sessionIdToUrl[sessionId];

  void _disposeAll() {
    for (final subs in _subscriptions.values) {
      for (final s in subs) {
        s.cancel();
      }
    }
    for (final svc in _services.values) {
      svc.dispose();
    }
    _subscriptions.clear();
    _services.clear();
    _liveSessionIds.clear();
    _sessionIdToUrl.clear();
  }

  void disposeAll() => _disposeAll();
}

final backgroundConnectionProvider =
    NotifierProvider<BackgroundConnectionManager, void>(
  () => BackgroundConnectionManager(),
);
