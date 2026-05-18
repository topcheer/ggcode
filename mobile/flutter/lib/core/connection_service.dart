import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'models/protocol.dart' as proto;

enum ConnectionStatus {
  disconnected,
  connecting,
  connected,
}

class ConnectionService {
  final String url;
  WebSocket? _socket;
  bool _disposed = false;
  bool _connected = false;

  final _statusController = StreamController<ConnectionStatus>.broadcast();
  final _errorController = StreamController<String>.broadcast();
  final _messageController =
      StreamController<proto.WsMessage>.broadcast();

  Stream<ConnectionStatus> get statusStream => _statusController.stream;
  Stream<String> get errorStream => _errorController.stream;
  Stream<proto.WsMessage> get messageStream => _messageController.stream;

  Timer? _heartbeatTimer;
  StreamSubscription? _socketSub;

  ConnectionService(this.url);

  Future<void> connect() async {
    _statusController.add(ConnectionStatus.connecting);

    try {
      // Properly await WebSocket handshake — catches 503, DNS errors, etc.
      _socket = await WebSocket.connect(url)
          .timeout(const Duration(seconds: 10));
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

    // Connection succeeded
    _connected = true;
    _statusController.add(ConnectionStatus.connected);
    _startHeartbeat();

    _socketSub = _socket!.listen(
      (data) {
        if (data is String) {
          final msg = proto.WsMessage.fromJson(data);
          _messageController.add(msg);
        }
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

  void _cleanup() {
    _stopHeartbeat();
    _connected = false;
    _socketSub?.cancel();
    _socketSub = null;
  }

  void _startHeartbeat() {
    _stopHeartbeat();
    _heartbeatTimer = Timer.periodic(const Duration(seconds: 15), (_) {
      if (_socket != null) {
        try {
          send({'type': 'ping'});
        } catch (e) {
          _cleanup();
          if (!_disposed) {
            _statusController.add(ConnectionStatus.disconnected);
          }
        }
      }
    });
  }

  void _stopHeartbeat() {
    _heartbeatTimer?.cancel();
    _heartbeatTimer = null;
  }

  void send(Map<String, dynamic> data) {
    _socket?.add(jsonEncode(data));
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
