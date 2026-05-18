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
  bool _disposed = false;
  bool _connected = false;

  final _statusController = StreamController<ConnectionStatus>.broadcast();
  final _messageController =
      StreamController<proto.WsMessage>.broadcast();

  Stream<ConnectionStatus> get statusStream => _statusController.stream;
  Stream<proto.WsMessage> get messageStream => _messageController.stream;

  Timer? _heartbeatTimer;

  ConnectionService(this.url);

  Future<void> connect() async {
    _statusController.add(ConnectionStatus.connecting);
    try {
      _channel = WebSocketChannel.connect(Uri.parse(url));
      // Don't emit connected yet — wait for first message from server

      _channel!.stream.listen(
        (data) {
          // First message confirms connection is truly alive
          if (!_connected) {
            _connected = true;
            _statusController.add(ConnectionStatus.connected);
            _startHeartbeat();
          }
          final msg = proto.WsMessage.fromJson(data as String);
          _messageController.add(msg);
        },
        onDone: () {
          _stopHeartbeat();
          if (!_disposed) {
            _connected = false;
            _statusController.add(ConnectionStatus.disconnected);
          }
        },
        onError: (e) {
          _stopHeartbeat();
          if (!_disposed) {
            _connected = false;
            _statusController.add(ConnectionStatus.disconnected);
          }
        },
      );
    } catch (e) {
      if (!_disposed) {
        _statusController.add(ConnectionStatus.disconnected);
      }
    }
  }

  /// Client sends {"type":"ping"} every 15 seconds.
  /// If the send fails, connection is dead.
  void _startHeartbeat() {
    _stopHeartbeat();
    _heartbeatTimer = Timer.periodic(const Duration(seconds: 15), (_) {
      try {
        send({'type': 'ping'});
      } catch (e) {
        _stopHeartbeat();
        if (!_disposed) {
          _connected = false;
          _statusController.add(ConnectionStatus.disconnected);
        }
      }
    });
  }

  void _stopHeartbeat() {
    _heartbeatTimer?.cancel();
    _heartbeatTimer = null;
  }

  void send(Map<String, dynamic> data) {
    _channel?.sink.add(jsonEncode(data));
  }

  void disconnect() {
    _stopHeartbeat();
    _channel?.sink.close();
    _channel = null;
    _connected = false;
  }

  void dispose() {
    _disposed = true;
    disconnect();
    _statusController.close();
    _messageController.close();
  }
}
