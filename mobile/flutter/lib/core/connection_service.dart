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
  Timer? _connectTimeout;

  ConnectionService(this.url);

  Future<void> connect() async {
    _statusController.add(ConnectionStatus.connecting);

    // Timeout: if no server message within 10s, consider connection failed
    _connectTimeout = Timer(const Duration(seconds: 10), () {
      if (!_connected && !_disposed) {
        disconnect();
        _statusController.add(ConnectionStatus.disconnected);
      }
    });

    try {
      _channel = WebSocketChannel.connect(Uri.parse(url));

      _channel!.stream.listen(
        (data) {
          // First message confirms connection is truly alive
          if (!_connected) {
            _connected = true;
            _connectTimeout?.cancel();
            _connectTimeout = null;
            _statusController.add(ConnectionStatus.connected);
            _startHeartbeat();
          }
          final msg = proto.WsMessage.fromJson(data as String);
          _messageController.add(msg);
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
            _statusController.add(ConnectionStatus.disconnected);
          }
        },
      );
    } catch (e) {
      _cleanup();
      if (!_disposed) {
        _statusController.add(ConnectionStatus.disconnected);
      }
    }
  }

  void _cleanup() {
    _connectTimeout?.cancel();
    _connectTimeout = null;
    _stopHeartbeat();
    _connected = false;
  }

  /// Client sends {"type":"ping"} every 15 seconds.
  void _startHeartbeat() {
    _stopHeartbeat();
    _heartbeatTimer = Timer.periodic(const Duration(seconds: 15), (_) {
      try {
        send({'type': 'ping'});
      } catch (e) {
        _cleanup();
        if (!_disposed) {
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
    _cleanup();
    _channel?.sink.close();
    _channel = null;
  }

  void dispose() {
    _disposed = true;
    disconnect();
    _statusController.close();
    _messageController.close();
  }
}
