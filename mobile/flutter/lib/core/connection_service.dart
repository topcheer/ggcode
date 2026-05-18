import 'dart:async';
import 'dart:convert';
import 'package:web_socket_channel/web_socket_channel.dart';

import 'models/protocol.dart' as proto;

enum ConnectionStatus {
  disconnected,
  connecting,
  connected,
}

class ConnectionService {
  final String url;
  WebSocketChannel? _channel;

  final _statusController = StreamController<ConnectionStatus>.broadcast();
  final _messageController =
      StreamController<proto.WsMessage>.broadcast();

  Stream<ConnectionStatus> get statusStream => _statusController.stream;
  Stream<proto.WsMessage> get messageStream => _messageController.stream;

  Timer? _reconnectTimer;
  Timer? _pongTimeout;
  bool _disposed = false;

  ConnectionService(this.url);

  Future<void> connect() async {
    _statusController.add(ConnectionStatus.connecting);
    try {
      _channel = WebSocketChannel.connect(Uri.parse(url));

      // Wait for connection ready
      await _channel!.ready;

      _statusController.add(ConnectionStatus.connected);
      _startPongWatchdog();

      _channel!.stream.listen(
        (data) {
          _resetPongWatchdog();
          final msg = proto.WsMessage.fromJson(data as String);
          _messageController.add(msg);
        },
        onDone: () {
          _cancelPongWatchdog();
          _statusController.add(ConnectionStatus.disconnected);
          if (!_disposed) {
            _scheduleReconnect();
          }
        },
        onError: (e) {
          _cancelPongWatchdog();
          _statusController.add(ConnectionStatus.disconnected);
          if (!_disposed) {
            _scheduleReconnect();
          }
        },
      );
    } catch (e) {
      _statusController.add(ConnectionStatus.disconnected);
      if (!_disposed) {
        _scheduleReconnect();
      }
      rethrow;
    }
  }

  /// Server sends WebSocket pings every 15s.
  /// The web_socket_channel library auto-replies with pong.
  /// We just need to detect if no message arrives within 30s (2x ping interval)
  /// to consider the connection dead.
  void _startPongWatchdog() {
    _pongTimeout?.cancel();
    _pongTimeout = Timer(const Duration(seconds: 30), () {
      // No data received in 30s — connection is dead
      disconnect();
      _scheduleReconnect();
    });
  }

  void _resetPongWatchdog() {
    _startPongWatchdog();
  }

  void _cancelPongWatchdog() {
    _pongTimeout?.cancel();
    _pongTimeout = null;
  }

  void _scheduleReconnect() {
    _reconnectTimer?.cancel();
    _reconnectTimer = Timer(const Duration(seconds: 3), () {
      if (!_disposed) {
        connect();
      }
    });
  }

  void send(Map<String, dynamic> data) {
    _channel?.sink.add(jsonEncode(data));
  }

  void disconnect() {
    _reconnectTimer?.cancel();
    _cancelPongWatchdog();
    _channel?.sink.close();
    _channel = null;
    _statusController.add(ConnectionStatus.disconnected);
  }

  void dispose() {
    _disposed = true;
    disconnect();
    _statusController.close();
    _messageController.close();
  }
}
