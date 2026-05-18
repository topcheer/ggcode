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
      _statusController.add(ConnectionStatus.connected);
      _startHeartbeat();

      _channel!.stream.listen(
        (data) {
          final msg = proto.WsMessage.fromJson(data as String);
          _messageController.add(msg);
        },
        onDone: () {
          _stopHeartbeat();
          if (!_disposed) {
            _statusController.add(ConnectionStatus.disconnected);
          }
        },
        onError: (e) {
          _stopHeartbeat();
          if (!_disposed) {
            _statusController.add(ConnectionStatus.disconnected);
          }
        },
      );
    } catch (e) {
      _statusController.add(ConnectionStatus.disconnected);
    }
  }

  /// Client sends {"type":"ping"} every 15 seconds.
  /// If the write fails, the connection is dead.
  void _startHeartbeat() {
    _stopHeartbeat();
    _heartbeatTimer = Timer.periodic(const Duration(seconds: 15), (_) {
      try {
        send({'type': 'ping'});
      } catch (e) {
        // Write failed — connection is dead
        _stopHeartbeat();
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
    _stopHeartbeat();
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
