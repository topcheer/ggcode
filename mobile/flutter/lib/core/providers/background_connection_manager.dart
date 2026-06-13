import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../connection_service.dart';
import '../models/protocol.dart' as proto;
import 'workspace_cache.dart';

/// Manages background relay connections for non-active sessions.
///
/// The active session is handled by [ConnectionNotifier]. This manager
/// maintains WebSocket connections to all other paired sessions so their
/// messages are cached locally and available for instant switching.
class BackgroundConnectionManager extends Notifier<void> {
  /// URL → ConnectionService for non-active sessions.
  final Map<String, ConnectionService> _services = {};

  /// URL → subscriptions for each background service.
  final Map<String, List<StreamSubscription<dynamic>>> _subscriptions = {};

  /// Session ID → URL mapping for quick lookup.
  final Map<String, String> _sessionIdToUrl = {};

  /// Set of session IDs with live (connected) background connections.
  final Set<String> _liveSessionIds = {};

  @override
  void build() {
    ref.onDispose(() {
      disconnectAll();
    });
  }

  /// Session IDs that have a live background connection.
  Set<String> get liveSessionIds => Set.unmodifiable(_liveSessionIds);

  /// Whether a specific session has a live background connection.
  bool isSessionLive(String sessionId) => _liveSessionIds.contains(sessionId);

  /// Connect all cached sessions that have a URL, excluding [excludeUrl].
  Future<void> connectAllCachedSessions({String? excludeUrl}) async {
    final cache = ref.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    final sessions = ref.read(workspaceCacheProvider).sessions;

    for (final record in sessions.values) {
      final url = record.url;
      if (url.isEmpty || url == excludeUrl) continue;
      if (_services.containsKey(url)) continue;
      await _connectBackground(url, record.sessionId);
    }
  }

  /// Start a background connection for the given URL/session.
  Future<void> _connectBackground(String url, String sessionId) async {
    if (_services.containsKey(url)) return;

    final descriptor = ShareConnectionDescriptor.parse(url);
    final service = ConnectionService(descriptor: descriptor);
    _services[url] = service;
    _sessionIdToUrl[sessionId] = url;

    final subs = <StreamSubscription<dynamic>>[];

    subs.add(service.messageStream.listen((msg) {
      _handleBackgroundMessage(url, sessionId, msg);
    }));

    subs.add(service.statusStream.listen((status) {
      if (status == ConnectionStatus.disconnected) {
        _liveSessionIds.remove(sessionId);
      } else if (status == ConnectionStatus.connected) {
        _liveSessionIds.add(sessionId);
      }
    }));

    _subscriptions[url] = subs;

    try {
      await service.connect();
      _liveSessionIds.add(sessionId);
      debugPrint('[bg-conn] connected session $sessionId');
    } catch (e) {
      debugPrint('[bg-conn] connect failed for session $sessionId: $e');
    }
  }

  /// Route background messages to workspace cache (no UI update).
  void _handleBackgroundMessage(
      String url, String sessionId, proto.WsMessage msg) {
    // Background messages are silently cached — they don't update the UI.
    final data = msg.data;
    if (data == null) return;

    final type = data['type'] as String? ?? msg.type;

    // Cache events for offline viewing
    final cache = ref.read(workspaceCacheProvider.notifier);
    cache.cacheBackgroundEvent(
      sessionId: sessionId,
      eventType: type,
      eventData: Map<String, dynamic>.from(data),
    );
  }

  /// Take ownership of a background connection (returns the service).
  /// The caller is responsible for subscribing to streams and managing lifecycle.
  ConnectionService? takeService(String url) {
    final service = _services.remove(url);
    if (service == null) return null;

    // Cancel background subscriptions — the new owner manages them
    final subs = _subscriptions.remove(url);
    if (subs != null) {
      for (final sub in subs) {
        sub.cancel();
      }
    }

    // Clean up mappings
    _sessionIdToUrl.removeWhere((_, u) => u == url);
    // Remove any session IDs that pointed to this URL
    _liveSessionIds.removeWhere(
        (id) => _sessionIdToUrl[id] == null && !_services.values.any((s) => false));

    return service;
  }

  /// Register a service as a background connection (e.g. when demoting
  /// the active connection).
  void registerService(
      String url, String sessionId, ConnectionService service) {
    if (_services.containsKey(url)) return;

    _services[url] = service;
    _sessionIdToUrl[sessionId] = url;

    final subs = <StreamSubscription<dynamic>>[];

    subs.add(service.messageStream.listen((msg) {
      _handleBackgroundMessage(url, sessionId, msg);
    }));

    subs.add(service.statusStream.listen((status) {
      if (status == ConnectionStatus.disconnected) {
        _liveSessionIds.remove(sessionId);
      } else if (status == ConnectionStatus.connected) {
        _liveSessionIds.add(sessionId);
      }
    }));

    _subscriptions[url] = subs;
  }

  /// Disconnect a specific background session.
  void disconnectSession(String sessionId) {
    final url = _sessionIdToUrl.remove(sessionId);
    if (url == null) return;

    final service = _services.remove(url);
    service?.disconnect();

    final subs = _subscriptions.remove(url);
    if (subs != null) {
      for (final sub in subs) {
        sub.cancel();
      }
    }

    _liveSessionIds.remove(sessionId);
  }

  /// Disconnect all background sessions.
  void disconnectAll() {
    for (final url in _services.keys.toList()) {
      _services[url]?.disconnect();
    }
    for (final subs in _subscriptions.values) {
      for (final sub in subs) {
        sub.cancel();
      }
    }
    _services.clear();
    _subscriptions.clear();
    _sessionIdToUrl.clear();
    _liveSessionIds.clear();
  }

  /// Get the URL for a session.
  String? urlForSession(String sessionId) {
    return _sessionIdToUrl[sessionId];
  }
}

final backgroundConnectionProvider =
    NotifierProvider<BackgroundConnectionManager, void>(
  () => BackgroundConnectionManager(),
);
