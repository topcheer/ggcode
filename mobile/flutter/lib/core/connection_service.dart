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
  static const _maxReconnectAttempts = 10;
  Timer? _reconnectTimer;

  final _statusController = StreamController<ConnectionStatus>.broadcast();
  final _errorController = StreamController<String>.broadcast();
  final _messageController = StreamController<proto.WsMessage>.broadcast();

  Stream<ConnectionStatus> get statusStream => _statusController.stream;
  Stream<String> get errorStream => _errorController.stream;
  Stream<proto.WsMessage> get messageStream => _messageController.stream;

  Timer? _heartbeatTimer;
  StreamSubscription? _socketSub;

  ConnectionService({required this.url, required this.crypto});

  Future<void> connect() async {
    _cancelReconnect();
    _statusController.add(ConnectionStatus.connecting);

    try {
      _socket = await WebSocket.connect(url).timeout(const Duration(seconds: 10));
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

    _reconnectAttempts = 0; // Reset on successful connect

    _socketSub = _socket!.listen(
      (data) async {
        if (data is! String) return;
        await _handleRelayMessage(data);
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
    // Exponential backoff: 2s, 4s, 8s, 16s, 32s, max 30s
    final delay = Duration(seconds: (_reconnectAttempts * 2).clamp(2, 30));
    print('[connection] reconnecting in ${delay.inSeconds}s (attempt $_reconnectAttempts)');
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

      case 'replay_start':
        break;

      case 'replay_end':
        break;

      case 'server_offline':
        _cleanup();
        if (!_disposed) {
          _statusController.add(ConnectionStatus.disconnected);
          _scheduleReconnect();
        }
        break;

      case 'encrypted':
        final nonce = map['nonce'] as String? ?? '';
        final ciphertext = map['ciphertext'] as String? ?? '';
        if (nonce.isEmpty || ciphertext.isEmpty) return;

        try {
          final plaintextBytes = await crypto.decryptData(nonce, ciphertext);
          final plaintext = utf8.decode(plaintextBytes);
          final msg = proto.WsMessage.fromJson(plaintext);
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

  /// Send an unencrypted control message (ping/pong).
  void send(Map<String, dynamic> data) {
    _socket?.add(jsonEncode(data));
  }

  /// Send an encrypted message to the relay.
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
