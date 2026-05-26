import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';

import 'crypto.dart';
import 'models/protocol.dart' as proto;

/// Normalize tunnel URL schemes (ggcode:// → wss://, http:// → ws://, etc).
String normalizeTunnelUrl(String raw) {
  String url = raw.trim();
  if (url.startsWith('ggcode://')) {
    url = url.replaceFirst('ggcode://', 'wss://');
  }
  if (url.startsWith('http://')) {
    url = url.replaceFirst('http://', 'ws://');
  } else if (url.startsWith('https://')) {
    url = url.replaceFirst('https://', 'wss://');
  }
  return url;
}

enum ConnectionStatus {
  disconnected,
  connecting,
  connected,
}

const Duration _defaultServerOfflineReconnectDelay = Duration(seconds: 60);

bool isPermanentRoomFailureMessage(String error) {
  final lower = error.toLowerCase();
  return lower.contains('room not found') ||
      lower.contains('stale or expired share token') ||
      lower.contains('http status code: 410') ||
      lower.contains('status code: 410');
}

Duration relayRecoveryDelay([int? retryAfterMs]) {
  if (retryAfterMs == null || retryAfterMs <= 0) {
    return _defaultServerOfflineReconnectDelay;
  }
  return Duration(milliseconds: retryAfterMs);
}

int? relayRetryAfterMs(Map<String, dynamic>? data) {
  if (data == null) return null;
  final raw = data['retry_after_ms'];
  if (raw is int && raw > 0) return raw;
  if (raw is String) return int.tryParse(raw);
  return null;
}

class ConnectionService {
  final String url;
  final TunnelCrypto crypto;
  WebSocket? _socket;
  bool _disposed = false;
  bool _serverOfflineReconnect = false;
  bool _everConnected = false;
  int _reconnectAttempts = 0;
  static const _maxReconnectAttempts = 30;
  Timer? _reconnectTimer;

  final _statusController = StreamController<ConnectionStatus>.broadcast();
  final _errorController = StreamController<String>.broadcast();
  final _messageController = StreamController<proto.WsMessage>.broadcast();
  final _ackController = StreamController<AckEvent>.broadcast();

  Stream<ConnectionStatus> get statusStream => _statusController.stream;
  Stream<String> get errorStream => _errorController.stream;
  Stream<proto.WsMessage> get messageStream => _messageController.stream;
  Stream<AckEvent> get ackStream => _ackController.stream;

  Timer? _heartbeatTimer;
  StreamSubscription? _socketSub;

  // Sequential message processing queue
  Future<void> _queue = Future.value();
  int _decryptErrorCount = 0;

  ConnectionService({required this.url, required this.crypto});

  Future<void> connect() async {
    _cancelReconnect();
    _statusController.add(ConnectionStatus.connecting);

    try {
      _socket =
          await WebSocket.connect(url).timeout(const Duration(seconds: 30));
    } catch (e) {
      if (!_disposed) {
        final error = _formatConnectError(e);
        _errorController.add(error);
        _statusController.add(ConnectionStatus.disconnected);
        if (isPermanentRoomFailureMessage(error)) {
          return;
        }
        if (_everConnected || _serverOfflineReconnect) {
          _scheduleServerOfflineReconnect();
        } else {
          _scheduleReconnect();
        }
      }

      return;
    }

    if (_disposed) {
      _socket!.close();
      return;
    }

    _reconnectAttempts = 0;
    _decryptErrorCount = 0;
    _serverOfflineReconnect = false;
    _queue = Future.value();

    _socketSub = _socket!.listen(
      (data) {
        if (data is! String) return;
        // Enqueue for sequential processing. Catch errors to prevent a
        // single bad message from breaking the entire chain — all subsequent
        // messages would be silently dropped otherwise.
        _queue = _queue.then((_) => _handleRelayMessage(data)).catchError((e) {
          // Swallow to keep the chain alive; _handleRelayMessage handles
          // its own errors internally for known cases.
        });
      },
      onDone: () {
        _cleanup();
        if (!_disposed) {
          _statusController.add(ConnectionStatus.disconnected);
          if (_serverOfflineReconnect || _everConnected) {
            _scheduleServerOfflineReconnect();
          } else {
            _scheduleReconnect();
          }
        }
      },
      onError: (e) {
        _cleanup();
        if (!_disposed) {
          _errorController.add('Connection error: $e');
          _statusController.add(ConnectionStatus.disconnected);
          if (_serverOfflineReconnect || _everConnected) {
            _scheduleServerOfflineReconnect();
          } else {
            _scheduleReconnect();
          }
        }
      },
    );
  }

  void _scheduleReconnect() {
    if (_disposed) return;
    if (_reconnectAttempts >= _maxReconnectAttempts) {
      _errorController.add('Max reconnection attempts reached');
      return;
    }

    _reconnectAttempts++;
    final delay = Duration(seconds: (_reconnectAttempts * 2).clamp(2, 30));
    debugPrint(
        '[connection] reconnecting in ${delay.inSeconds}s (attempt $_reconnectAttempts)');
    _reconnectTimer = Timer(delay, () {
      if (!_disposed) {
        connect();
      }
    });
  }

  void _cancelReconnect() {
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
  }

  /// Reconnect after relay/server recovery notices.
  void _scheduleServerOfflineReconnect([Duration? delay]) {
    _cancelReconnect();
    _serverOfflineReconnect = true;
    final wait = delay ?? _defaultServerOfflineReconnectDelay;
    debugPrint(
        '[connection] server offline, reconnecting in ${wait.inSeconds}s...');
    _reconnectTimer = Timer(wait, () {
      if (!_disposed) {
        connect();
      }
    });
  }

  Future<void> _handleRelayMessage(String raw) async {
    final map = jsonDecode(raw) as Map<String, dynamic>;
    final type = map['type'] as String? ?? '';

    switch (type) {
      case 'connected':
        _everConnected = true;
        _serverOfflineReconnect = false;
        _statusController.add(ConnectionStatus.connected);
        _startHeartbeat();
        break;

      case 'pong':
        break;

      case 'active_session':
        _messageController.add(proto.WsMessage(
          sessionId: map['session_id'] as String?,
          type: type,
          data: Map<String, dynamic>.from(map)
            ..remove('type')
            ..remove('session_id'),
        ));
        break;

      case 'server_offline':
        final data = map['data'] is Map<String, dynamic>
            ? map['data'] as Map<String, dynamic>
            : map['data'] is Map
                ? Map<String, dynamic>.from(map['data'] as Map)
                : null;
        final retryAfter = relayRecoveryDelay(relayRetryAfterMs(data));
        _errorController.add(
          'Relay recovering: reconnecting in ${retryAfter.inSeconds}s',
        );
        _cleanup();
        if (!_disposed) {
          _messageController.add(proto.WsMessage(
            sessionId: map['session_id'] as String?,
            type: 'server_offline',
            data: data,
          ));
          _statusController.add(ConnectionStatus.disconnected);
          _scheduleServerOfflineReconnect(retryAfter);
        }
        break;

      case 'relay_ack':
        // Relay confirmed receipt of our encrypted message.
        final ackId = map['message_id'] as String? ?? '';
        if (ackId.isNotEmpty) {
          _ackController.add(AckEvent(type: 'relay_ack', messageId: ackId));
        }
        break;

      case 'server_ack':
        // Desktop confirmed processing of our message (plaintext, unencrypted).
        final sackId = map['message_id'] as String? ?? '';
        if (sackId.isNotEmpty) {
          _ackController.add(AckEvent(type: 'server_ack', messageId: sackId));
        }
        break;

      case 'sharing_stopped':
        // User explicitly stopped sharing — permanent disconnect.
        _cleanup();
        if (!_disposed) {
          _disposed = true;
          _errorController.add('Sharing stopped');
          _statusController.add(ConnectionStatus.disconnected);
        }
        break;

      case 'resume_ack':
      case 'resume_miss':
      case 'snapshot_reset':
        _messageController.add(proto.WsMessage(
          sessionId: map['session_id'] as String?,
          type: type,
          data: Map<String, dynamic>.from(map)
            ..remove('type')
            ..remove('session_id'),
        ));
        break;

      case 'encrypted':
        final nonce = map['nonce'] as String? ?? '';
        final ciphertext = map['ciphertext'] as String? ?? '';
        if (nonce.isEmpty || ciphertext.isEmpty) return;

        try {
          final plaintextBytes = await crypto.decryptData(nonce, ciphertext);
          final plaintext = utf8.decode(plaintextBytes);
          var msg = proto.WsMessage.fromJson(plaintext);
          if ((msg.sessionId == null || msg.sessionId!.isEmpty) &&
              map['session_id'] is String) {
            msg = proto.WsMessage(
              sessionId: map['session_id'] as String?,
              eventId: (msg.eventId?.isNotEmpty ?? false)
                  ? msg.eventId
                  : map['event_id'] as String?,
              streamId: (msg.streamId?.isNotEmpty ?? false)
                  ? msg.streamId
                  : map['stream_id'] as String?,
              type: msg.type,
              data: msg.data,
            );
          }
          _decryptErrorCount = 0;
          final replayTag = map['event_id'] != null ? ' event=${map['event_id']}' : '';
          debugPrint('[connection] encrypted decrypted:${replayTag} type=${msg.type} sessionId=${msg.sessionId}');
          _messageController.add(msg);
        } catch (e) {
          _decryptErrorCount++;
          if (_decryptErrorCount <= 3) {
            _errorController.add('Decrypt error (#$_decryptErrorCount): $e');
          }
        }
        break;
    }
  }

  String _formatConnectError(Object error) {
    final raw = error.toString();
    if (isPermanentRoomFailureMessage(raw)) {
      return 'Room not found: stale or expired share token';
    }
    return 'Connection failed: $raw';
  }

  void _cleanup() {
    _stopHeartbeat();
    _socketSub?.cancel();
    _socketSub = null;
  }

  void _startHeartbeat() {
    _stopHeartbeat();
    _heartbeatTimer = Timer.periodic(const Duration(seconds: 20), (_) {
      send({'type': 'ping'});
    });
  }

  void _stopHeartbeat() {
    _heartbeatTimer?.cancel();
    _heartbeatTimer = null;
  }

  void send(Map<String, dynamic> data) {
    _socket?.add(jsonEncode(data));
  }

  void sendResumeHello({
    required String clientId,
    String? sessionId,
  }) {
    send({
      'type': 'resume_hello',
      'client_id': clientId,
      if (sessionId != null && sessionId.isNotEmpty) 'session_id': sessionId,
    });
  }

  /// ACK an event — tells the relay to advance the cursor.
  void sendAck({
    required String clientId,
    required String eventId,
  }) {
    send({
      'type': 'event_ack',
      'client_id': clientId,
      'event_id': eventId,
    });
  }

  void sendLanguageChange(String language) {
    send({
      'type': 'language_change',
      'language': language,
    });
  }

  void sendThemeChange(String theme) {
    send({
      'type': 'theme_change',
      'theme': theme,
    });
  }

  Future<void> sendEncrypted(proto.WsMessage msg) async {
    final plaintext = utf8.encode(msg.toJson());
    final encrypted = await crypto.encryptData(plaintext);
    final relayMsg = jsonEncode({
      'type': 'encrypted',
      'nonce': encrypted.nonce,
      'ciphertext': encrypted.ciphertext,
      if (msg.messageId != null && msg.messageId!.isNotEmpty)
        'message_id': msg.messageId,
    });
    _socket?.add(relayMsg);
  }

  void disconnect() {
    _cancelReconnect();
    _serverOfflineReconnect = false;
    _cleanup();
    _socket?.close();
    _socket = null;
  }

  void dispose() {
    _disposed = true;
    disconnect();
    _statusController.close();
    _errorController.close();
    _messageController.close();
    _ackController.close();
  }
}

/// Ack event from relay or server confirming message delivery.
class AckEvent {
  final String type; // 'relay_ack' or 'server_ack'
  final String messageId;

  const AckEvent({required this.type, required this.messageId});
}
