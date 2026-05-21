import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'crypto.dart';
import 'models/protocol.dart' as proto;

enum ConnectionStatus {
  disconnected,
  connecting,
  connected,
}

class ConnectionService {
  final String url;
  final TunnelCrypto crypto;
  WebSocket? _socket;
  bool _disposed = false;
  int _reconnectAttempts = 0;
  static const _maxReconnectAttempts = 30;
  Timer? _reconnectTimer;

  final _statusController = StreamController<ConnectionStatus>.broadcast();
  final _errorController = StreamController<String>.broadcast();
  final _messageController = StreamController<proto.WsMessage>.broadcast();

  Stream<ConnectionStatus> get statusStream => _statusController.stream;
  Stream<String> get errorStream => _errorController.stream;
  Stream<proto.WsMessage> get messageStream => _messageController.stream;

  Timer? _heartbeatTimer;
  StreamSubscription? _socketSub;

  // Sequential message processing queue
  Future<void> _queue = Future.value();

  ConnectionService({required this.url, required this.crypto});

  Future<void> connect() async {
    _cancelReconnect();
    _statusController.add(ConnectionStatus.connecting);

    try {
      _socket =
          await WebSocket.connect(url).timeout(const Duration(seconds: 30));
    } catch (e) {
      if (!_disposed) {
        _errorController.add('Connection failed: $e');
        _statusController.add(ConnectionStatus.disconnected);
        _scheduleReconnect();
      }
      return;
    }

    if (_disposed) {
      _socket!.close();
      return;
    }

    _reconnectAttempts = 0;
    _queue = Future.value();

    _socketSub = _socket!.listen(
      (data) {
        if (data is! String) return;
        // Enqueue for sequential processing
        _queue = _queue.then((_) => _handleRelayMessage(data));
      },
      onDone: () {
        _cleanup();
        if (!_disposed) {
          _statusController.add(ConnectionStatus.disconnected);
          _scheduleReconnect();
        }
      },
      onError: (e) {
        _cleanup();
        if (!_disposed) {
          _errorController.add('Connection error: $e');
          _statusController.add(ConnectionStatus.disconnected);
          _scheduleReconnect();
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
    print(
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

  Future<void> _handleRelayMessage(String raw) async {
    final map = jsonDecode(raw) as Map<String, dynamic>;
    final type = map['type'] as String? ?? '';

    switch (type) {
      case 'connected':
        _statusController.add(ConnectionStatus.connected);
        _startHeartbeat();
        break;

      case 'pong':
        break;

      case 'server_offline':
      case 'sharing_stopped':
        _cleanup();
        if (!_disposed) {
          _disposed = true;
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
          _messageController.add(msg);
        } catch (e) {
          // Decrypt error
        }
        break;
    }
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
    String? lastEventId,
  }) {
    send({
      'type': 'resume_hello',
      'client_id': clientId,
      if (sessionId != null && sessionId.isNotEmpty) 'session_id': sessionId,
      if (lastEventId != null && lastEventId.isNotEmpty)
        'last_event_id': lastEventId,
    });
  }

  void requestReplayFrom({
    required String clientId,
    String? sessionId,
    String? lastEventId,
  }) {
    send({
      'type': 'resume_from',
      'client_id': clientId,
      if (sessionId != null && sessionId.isNotEmpty) 'session_id': sessionId,
      if (lastEventId != null && lastEventId.isNotEmpty)
        'last_event_id': lastEventId,
    });
  }

  Future<void> sendEncrypted(proto.WsMessage msg) async {
    final plaintext = utf8.encode(msg.toJson());
    final encrypted = await crypto.encryptData(plaintext);
    final relayMsg = jsonEncode({
      'type': 'encrypted',
      'nonce': encrypted.nonce,
      'ciphertext': encrypted.ciphertext,
    });
    _socket?.add(relayMsg);
  }

  void disconnect() {
    _cancelReconnect();
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
  }
}
