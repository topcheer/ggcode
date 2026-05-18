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

  Timer? _watchdog;
  int _missedPings = 0;

  ConnectionService(this.url);

  Future<void> connect() async {
    _statusController.add(ConnectionStatus.connecting);
    try {
      _channel = WebSocketChannel.connect(Uri.parse(url));
      _statusController.add(ConnectionStatus.connected);
      _startWatchdog();

      _channel!.stream.listen(
        (data) {
          _missedPings = 0;
          final msg = proto.WsMessage.fromJson(data as String);
          _messageController.add(msg);
        },
        onDone: () {
          _stopWatchdog();
          if (!_disposed) {
            _statusController.add(ConnectionStatus.disconnected);
          }
        },
        onError: (e) {
          _stopWatchdog();
          if (!_disposed) {
            _statusController.add(ConnectionStatus.disconnected);
          }
        },
      );
    } catch (e) {
      _statusController.add(ConnectionStatus.disconnected);
    }
  }

  /// Watchdog: if no data received for 30s, connection is dead.
  /// Server pings every 15s, so 30s = 2 missed pings.
  void _startWatchdog() {
    _stopWatchdog();
    _missedPings = 0;
    _watchdog = Timer.periodic(const Duration(seconds: 15), (_) {
      _missedPings++;
      if (_missedPings >= 2) {
        _stopWatchdog();
        if (!_disposed) {
          _statusController.add(ConnectionStatus.disconnected);
        }
        disconnect();
      }
    });
  }

  void _stopWatchdog() {
    _watchdog?.cancel();
    _watchdog = null;
  }

  void send(Map<String, dynamic> data) {
    _channel?.sink.add(jsonEncode(data));
  }

  void disconnect() {
    _stopWatchdog();
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
