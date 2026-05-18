import 'dart:async';
import 'dart:convert';
import 'package:web_socket_channel/web_socket_channel.dart';

import 'models/protocol.dart' as proto;

enum ConnectionStatus {
  disconnected,
  connecting,
  connected,
  error,
}

class ConnectionService {
  WebSocketChannel? _channel;
  ConnectionStatus _status = ConnectionStatus.disconnected;
  String? _connectedUrl;
  String? _errorMessage;

  final _statusController = StreamController<ConnectionStatus>.broadcast();
  final _messageController =
      StreamController<proto.WsMessage>.broadcast();

  Stream<ConnectionStatus> get statusStream => _statusController.stream;
  Stream<proto.WsMessage> get messageStream => _messageController.stream;

  ConnectionStatus get status => _status;
  String? get connectedUrl => _connectedUrl;
  String? get errorMessage => _errorMessage;

  Future<void> connect(String url) async {
    if (_status == ConnectionStatus.connected ||
        _status == ConnectionStatus.connecting) {
      disconnect();
    }

    _connectedUrl = url;
    _status = ConnectionStatus.connecting;
    _errorMessage = null;
    _statusController.add(_status);

    try {
      final uri = Uri.parse(url);
      _channel = WebSocketChannel.connect(uri);

      _channel!.stream.listen(
        (data) {
          if (data is String) {
            try {
              final msg = proto.WsMessage.fromString(data);
              if (msg.type == 'ping') {
                send(proto.ClientMessage.pong());
                return;
              }
              _messageController.add(msg);
            } catch (_) {
              // Ignore malformed messages
            }
          }
        },
        onError: (error) {
          _status = ConnectionStatus.error;
          _errorMessage = error.toString();
          _statusController.add(_status);
        },
        onDone: () {
          if (_status != ConnectionStatus.error) {
            _status = ConnectionStatus.disconnected;
            _statusController.add(_status);
          }
        },
        cancelOnError: false,
      );

      // Wait briefly to see if connection fails immediately
      await _channel!.ready;

      _status = ConnectionStatus.connected;
      _statusController.add(_status);
    } catch (e) {
      _status = ConnectionStatus.error;
      _errorMessage = e.toString();
      _statusController.add(_status);
    }
  }

  void disconnect() {
    _channel?.sink.close();
    _channel = null;
    _connectedUrl = null;
    _status = ConnectionStatus.disconnected;
    _statusController.add(_status);
  }

  void send(proto.WsMessage message) {
    if (_status == ConnectionStatus.connected && _channel != null) {
      _channel!.sink.add(message.toJson());
    }
  }

  void sendMessage(String text) => send(proto.ClientMessage.chat(text));

  void sendApprovalResponse(String id, String decision) =>
      send(proto.ClientMessage.approvalResponse(id, decision));

  void sendInterrupt() => send(proto.ClientMessage.interrupt());

  void sendModeChange(String mode) =>
      send(proto.ClientMessage.modeChange(mode));

  void dispose() {
    disconnect();
    _statusController.close();
    _messageController.close();
  }
}
