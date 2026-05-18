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

  ConnectionService(this.url);

  Future<void> connect() async {
    _statusController.add(ConnectionStatus.connecting);
    try {
      _channel = WebSocketChannel.connect(Uri.parse(url));
      _statusController.add(ConnectionStatus.connected);

      _channel!.stream.listen(
        (data) {
          final msg = proto.WsMessage.fromJson(data as String);
          _messageController.add(msg);
        },
        onDone: () {
          _statusController.add(ConnectionStatus.disconnected);
        },
        onError: (e) {
          _statusController.add(ConnectionStatus.disconnected);
        },
      );
    } catch (e) {
      _statusController.add(ConnectionStatus.disconnected);
      rethrow;
    }
  }

  void send(Map<String, dynamic> data) {
    _channel?.sink.add(jsonEncode(data));
  }

  void disconnect() {
    _channel?.sink.close();
    _channel = null;
  }

  void dispose() {
    disconnect();
    _statusController.close();
    _messageController.close();
  }
}
