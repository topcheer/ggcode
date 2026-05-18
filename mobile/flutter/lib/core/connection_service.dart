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
  final String url; // wss://gateway.ggcode.dev/ws?role=client&token=xxx
  final TunnelCrypto crypto;
  WebSocket? _socket;
  bool _disposed = false;

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
    _statusController.add(ConnectionStatus.connecting);

    try {
      _socket = await WebSocket.connect(url).timeout(const Duration(seconds: 10));
    } catch (e) {
      if (!_disposed) {
        _errorController.add('Connection failed: $e');
        _statusController.add(ConnectionStatus.disconnected);
      }
      return;
    }

    if (_disposed) {
      _socket!.close();
      return;
    }

    _socketSub = _socket!.listen(
      (data) async {
        if (data is! String) return;
        await _handleRelayMessage(data);
      },
      onDone: () {
        _cleanup();
        if (!_disposed) {
          _statusController.add(ConnectionStatus.disconnected);
        }
      },
      onError: (e) {
        _cleanup();
        if (!_disposed) {
          _errorController.add('Connection error: $e');
          _statusController.add(ConnectionStatus.disconnected);
        }
      },
    );
  }

  Future<void> _handleRelayMessage(String raw) async {
    final map = jsonDecode(raw) as Map<String, dynamic>;
    final type = map['type'] as String? ?? '';

    switch (type) {
      case 'connected':
        // Relay confirmed our connection
        _statusController.add(ConnectionStatus.connected);
        _startHeartbeat();
        break;

      case 'pong':
        // Keepalive response
        break;

      case 'replay_start':
        // About to receive cached messages
        break;

      case 'replay_end':
        // Cache replay finished
        break;

      case 'server_offline':
        // Server disconnected
        _cleanup();
        if (!_disposed) {
          _statusController.add(ConnectionStatus.disconnected);
        }
        break;

      case 'encrypted':
        // Decrypt and dispatch
        final nonce = map['nonce'] as String? ?? '';
        final ciphertext = map['ciphertext'] as String? ?? '';
        if (nonce.isEmpty || ciphertext.isEmpty) return;

        try {
          final plaintextBytes = await crypto.decryptData(nonce, ciphertext);
          final plaintext = utf8.decode(plaintextBytes);
          final msg = proto.WsMessage.fromJson(plaintext);
          _messageController.add(msg);
        } catch (e) {
          // Decrypt error — might be wrong token
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
    _heartbeatTimer = Timer.periodic(const Duration(seconds: 15), (_) {
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
