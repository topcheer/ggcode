import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';

import 'crypto.dart';
import 'models/protocol.dart' as proto;

/// Normalize tunnel URL schemes (ggcode:// → wss://, http:// → ws://, etc).
String normalizeTunnelUrl(String raw) {
  String url = raw.trim();
  if (url.startsWith('ggcode://')) {
    url = url.replaceFirst('ggcode://', 'wss://');
  }
  if (url.startsWith('http://')) {
    url = url.replaceFirst('http://', 'ws://');
  } else if (url.startsWith('https://')) {
    url = url.replaceFirst('https://', 'wss://');
  }
  return url;
}

bool isLocalTunnelHost(String host) {
  final normalized = host.trim().toLowerCase();
  if (normalized.isEmpty) return false;
  if (normalized == 'localhost' ||
      normalized == 'host.docker.internal' ||
      normalized == 'gateway.docker.internal' ||
      normalized.endsWith('.local') ||
      !normalized.contains('.')) {
    return true;
  }
  final ip = InternetAddress.tryParse(host);
  if (ip == null) return false;
  return ip.isLoopback || ip.isLinkLocal || _isPrivateTunnelAddress(ip);
}

bool _isPrivateTunnelAddress(InternetAddress address) {
  final raw = address.rawAddress;
  if (address.type == InternetAddressType.IPv4 && raw.length == 4) {
    return raw[0] == 10 ||
        (raw[0] == 172 && raw[1] >= 16 && raw[1] <= 31) ||
        (raw[0] == 192 && raw[1] == 168);
  }
  if (address.type == InternetAddressType.IPv6 && raw.length == 16) {
    return (raw[0] & 0xfe) == 0xfc;
  }
  return false;
}

String? tunnelUrlSecurityError(String raw) {
  Uri uri;
  try {
    uri = Uri.parse(normalizeTunnelUrl(raw));
  } catch (_) {
    return null;
  }
  if (uri.scheme != 'ws' || isLocalTunnelHost(uri.host)) {
    return null;
  }
  return 'Insecure relay URL is only allowed for localhost or private network hosts';
}

enum ConnectionStatus {
  disconnected,
  connecting,
  connected,
}

const Duration _defaultServerOfflineReconnectDelay = Duration(seconds: 60);
const Duration _defaultRelayRestartReconnectDelay = Duration(seconds: 15);
const int _webSocketCloseServiceRestart = 1012;
const String _relayRestartReason = 'relay_restarting';

bool isPermanentRoomFailureMessage(String error) {
  final lower = error.toLowerCase();
  return lower.contains('room not found') ||
      lower.contains('stale or expired share token') ||
      lower.contains('upgrade required') ||
      lower.contains('please upgrade ggcode') ||
      lower.contains('http status code: 410') ||
      lower.contains('status code: 410');
}

bool isUpgradeRequiredMessage(String error) {
  final lower = error.toLowerCase();
  return lower.contains('upgrade required') ||
      lower.contains('please upgrade ggcode') ||
      lower.contains('ggcode share v3 is required');
}

bool isHttpGoneConnectError(String error) {
  final lower = error.toLowerCase();
  return lower.contains('http status code: 410') ||
      lower.contains('status code: 410');
}

Duration relayRecoveryDelay([int? retryAfterMs]) {
  if (retryAfterMs == null || retryAfterMs <= 0) {
    return _defaultServerOfflineReconnectDelay;
  }
  return Duration(milliseconds: retryAfterMs);
}

int? relayRetryAfterMs(Map<String, dynamic>? data) {
  if (data == null) return null;
  final raw = data['retry_after_ms'];
  if (raw is int && raw > 0) return raw;
  if (raw is String) return int.tryParse(raw);
  return null;
}

int? relayRestartRetryAfterMs(String? closeReason) {
  if (closeReason == null || closeReason.isEmpty) return null;
  final match = RegExp(r'retry_after_ms[=:](\d+)').firstMatch(closeReason);
  if (match == null) return null;
  return int.tryParse(match.group(1) ?? '');
}

bool isRelayRestartClose({
  int? closeCode,
  String? closeReason,
  Object? error,
}) {
  final normalizedReason = (closeReason ?? '').toLowerCase();
  final normalizedError = error?.toString().toLowerCase() ?? '';
  return closeCode == _webSocketCloseServiceRestart ||
      normalizedReason.contains(_relayRestartReason) ||
      normalizedError.contains(_relayRestartReason);
}

Duration relayRestartRecoveryDelay({String? closeReason}) {
  final retryAfterMs = relayRestartRetryAfterMs(closeReason);
  if (retryAfterMs != null && retryAfterMs > 0) {
    return relayRecoveryDelay(retryAfterMs);
  }
  return _defaultRelayRestartReconnectDelay;
}

class ShareConnectionDescriptor {
  final String relayUrl;
  final int protocolVersion;
  final String shareMode;
  final String token;
  final String roomId;
  final String authTicket;
  final String renewToken;
  final String cryptoKey;
  final String serverPublicKey;

  const ShareConnectionDescriptor({
    required this.relayUrl,
    required this.protocolVersion,
    required this.shareMode,
    required this.token,
    required this.roomId,
    required this.authTicket,
    required this.renewToken,
    required this.cryptoKey,
    required this.serverPublicKey,
  });

  factory ShareConnectionDescriptor.parse(String raw) {
    final normalized = normalizeTunnelUrl(raw);
    final uri = Uri.parse(normalized);
    final proto = int.tryParse(uri.queryParameters['proto'] ?? '') ?? 1;
    final roomId = uri.queryParameters['room_id'] ?? '';
    final authTicket = uri.queryParameters['auth_ticket'] ?? '';
    final renewToken = uri.queryParameters['renew_token'] ?? '';
    final token = uri.queryParameters['token'] ?? '';
    final cryptoKey = uri.queryParameters['crypto_key'] ?? '';
    final serverPublicKey = uri.queryParameters['kx_pub'] ?? '';
    final isV2 = proto >= 2 ||
        roomId.isNotEmpty ||
        authTicket.isNotEmpty ||
        renewToken.isNotEmpty;
    final protocolVersion = isV2 ? (proto >= 3 ? 3 : 2) : 1;
    return ShareConnectionDescriptor(
      relayUrl: '${uri.scheme}://${uri.authority}${uri.path}',
      protocolVersion: protocolVersion,
      shareMode: protocolVersion >= 3
          ? 'v3'
          : isV2
              ? 'v2'
              : 'legacy',
      token: token,
      roomId: roomId,
      authTicket: authTicket,
      renewToken: renewToken,
      cryptoKey: cryptoKey,
      serverPublicKey: serverPublicKey,
    );
  }

  bool get isV2 => protocolVersion >= 2 && roomId.isNotEmpty;
  bool get isV3 => protocolVersion >= 3 && roomId.isNotEmpty;

  String get cryptoMaterial => cryptoKey.isNotEmpty ? cryptoKey : token;

  String get publicUrl => _buildUrl(publicOnly: true);

  String runtimeUrl() => _buildUrl(publicOnly: false);

  ShareConnectionDescriptor copyWith({
    String? relayUrl,
    int? protocolVersion,
    String? shareMode,
    String? token,
    String? roomId,
    String? authTicket,
    String? renewToken,
    String? cryptoKey,
    String? serverPublicKey,
  }) {
    return ShareConnectionDescriptor(
      relayUrl: relayUrl ?? this.relayUrl,
      protocolVersion: protocolVersion ?? this.protocolVersion,
      shareMode: shareMode ?? this.shareMode,
      token: token ?? this.token,
      roomId: roomId ?? this.roomId,
      authTicket: authTicket ?? this.authTicket,
      renewToken: renewToken ?? this.renewToken,
      cryptoKey: cryptoKey ?? this.cryptoKey,
      serverPublicKey: serverPublicKey ?? this.serverPublicKey,
    );
  }

  String _buildUrl({required bool publicOnly}) {
    final uri = Uri.parse(relayUrl);
    final query = <String, String>{};
    if (isV2) {
      query['proto'] = protocolVersion.toString();
      query['room_id'] = roomId;
      if (serverPublicKey.isNotEmpty) {
        query['kx_pub'] = serverPublicKey;
      }
      if (!isV3 && cryptoKey.isNotEmpty) {
        query['crypto_key'] = cryptoKey;
      }
      if (!publicOnly) {
        query['role'] = 'client';
        query['client'] = 'mobile';
        query['caps'] =
            'share_v2,share_v3,share_notice,share_renew,tunnel_messages_v1';
      }
      if (!publicOnly && renewToken.isNotEmpty) {
        query['renew_token'] = renewToken;
      } else if (authTicket.isNotEmpty) {
        query['auth_ticket'] = authTicket;
      }
    } else {
      query['token'] = token;
      if (!publicOnly) {
        query['role'] = 'client';
        query['client'] = 'mobile';
        query['caps'] =
            'share_v2,share_v3,share_notice,share_renew,tunnel_messages_v1';
      }
    }
    return uri.replace(queryParameters: query).toString();
  }
}

class ShareConnectionMetadata {
  final int protocolVersion;
  final String shareMode;
  final String roomId;
  final String connectMode;
  final String notice;
  final String renewToken;
  final String serverPublicKey;
  final DateTime? authExpiresAt;
  final DateTime? renewExpiresAt;

  const ShareConnectionMetadata({
    required this.protocolVersion,
    required this.shareMode,
    required this.roomId,
    required this.connectMode,
    required this.notice,
    required this.renewToken,
    required this.serverPublicKey,
    required this.authExpiresAt,
    required this.renewExpiresAt,
  });

  factory ShareConnectionMetadata.fromRelay(Map<String, dynamic>? data) {
    final map = data ?? const <String, dynamic>{};
    String stringValue(dynamic value) {
      if (value == null) return '';
      return value is String ? value : value.toString();
    }

    return ShareConnectionMetadata(
      protocolVersion: map['protocol_version'] is int
          ? map['protocol_version'] as int
          : int.tryParse('${map['protocol_version'] ?? ''}') ?? 1,
      shareMode: stringValue(map['share_mode']),
      roomId: stringValue(map['room_id']),
      connectMode: stringValue(map['connect_mode']),
      notice: stringValue(map['notice']),
      renewToken: stringValue(map['renew_token']),
      serverPublicKey: stringValue(map['kx_pub']),
      authExpiresAt: DateTime.tryParse(stringValue(map['auth_expires_at'])),
      renewExpiresAt: DateTime.tryParse(stringValue(map['renew_expires_at'])),
    );
  }
}

class ConnectionService {
  ShareConnectionDescriptor _descriptor;
  TunnelCrypto? _crypto;
  WebSocket? _socket;
  bool _disposed = false;
  bool _permanentFailure = false;
  bool _serverOfflineReconnect = false;
  bool _everConnected = false;
  int _reconnectAttempts = 0;
  static const _maxReconnectAttempts = 30;
  Timer? _reconnectTimer;

  final _statusController = StreamController<ConnectionStatus>.broadcast();
  final _errorController = StreamController<String>.broadcast();
  final _messageController = StreamController<proto.WsMessage>.broadcast();
  final _ackController = StreamController<AckEvent>.broadcast();
  final _metadataController =
      StreamController<ShareConnectionMetadata>.broadcast();

  Stream<ConnectionStatus> get statusStream => _statusController.stream;
  Stream<String> get errorStream => _errorController.stream;
  Stream<proto.WsMessage> get messageStream => _messageController.stream;
  Stream<AckEvent> get ackStream => _ackController.stream;
  Stream<ShareConnectionMetadata> get metadataStream =>
      _metadataController.stream;
  String get publicUrl => _descriptor.publicUrl;
  ShareConnectionDescriptor get descriptor => _descriptor;

  Timer? _heartbeatTimer;
  StreamSubscription? _socketSub;

  // Sequential message processing queue
  Future<void> _queue = Future.value();
  int _decryptErrorCount = 0;
  String _clientId = '';
  String? _pendingResumeSessionId;
  String? _pendingResumeLastEventId;
  String _pendingResumeType = 'resume_hello';
  bool _resumeHelloSent = false;
  ShareKeyExchangeState? _keyExchangeState;
  Completer<void>? _keyExchangeReady;
  bool _keyOfferSent = false;

  ConnectionService({required ShareConnectionDescriptor descriptor})
      : _descriptor = descriptor {
    if (!descriptor.isV3) {
      _crypto = TunnelCrypto(descriptor.cryptoMaterial);
    }
  }

  Future<void> connect() async {
    _cancelReconnect();
    _permanentFailure = false;
    _resetHandshakeState();
    _statusController.add(ConnectionStatus.connecting);

    final runtimeUrl = _descriptor.runtimeUrl();
    final runtimeUri = Uri.tryParse(runtimeUrl);
    debugPrint(
      '[connection] connect start host=${runtimeUri?.host ?? ''} path=${runtimeUri?.path ?? ''} '
      'proto=${_descriptor.protocolVersion} share=${_descriptor.shareMode} '
      'room=${_descriptor.roomId} hasAuth=${_descriptor.authTicket.isNotEmpty} '
      'hasRenew=${_descriptor.renewToken.isNotEmpty}',
    );
    final securityError = tunnelUrlSecurityError(runtimeUrl);
    if (securityError != null) {
      _permanentFailure = true;
      _errorController.add(securityError);
      _statusController.add(ConnectionStatus.disconnected);
      return;
    }

    try {
      _socket = await WebSocket.connect(runtimeUrl)
          .timeout(const Duration(seconds: 30));
      debugPrint(
        '[connection] websocket connected host=${runtimeUri?.host ?? ''} path=${runtimeUri?.path ?? ''}',
      );
    } catch (e) {
      if (!_disposed) {
        final error = _formatConnectError(e);
        _permanentFailure = isPermanentRoomFailureMessage(error);
        _errorController.add(error);
        _statusController.add(ConnectionStatus.disconnected);
        if (isPermanentRoomFailureMessage(error)) {
          return;
        }
        if (_everConnected || _serverOfflineReconnect) {
          _scheduleServerOfflineReconnect();
        } else {
          _scheduleReconnect();
        }
      }

      return;
    }

    if (_disposed) {
      _socket!.close();
      return;
    }

    _reconnectAttempts = 0;
    _decryptErrorCount = 0;
    _serverOfflineReconnect = false;
    _queue = Future.value();

    _socketSub = _socket!.listen(
      (data) {
        if (data is! String) return;
        // Enqueue for sequential processing. Catch errors to prevent a
        // single bad message from breaking the entire chain — all subsequent
        // messages would be silently dropped otherwise.
        _queue = _queue.then((_) => _handleRelayMessage(data)).catchError(
          (error, stackTrace) {
            debugPrint('[connection] failed to handle relay message: $error');
            _errorController.add('Relay message handling failed: $error');
            FlutterError.reportError(
              FlutterErrorDetails(
                exception: error,
                stack: stackTrace,
                library: 'connection_service',
                context: ErrorDescription(
                    'while handling a relay websocket message'),
              ),
            );
          },
        );
      },
      onDone: () {
        final closeCode = _socket?.closeCode;
        final closeReason = _socket?.closeReason;
        _cleanup();
        if (_disposed) return;
        _queue.whenComplete(() {
          if (_disposed || _permanentFailure) {
            return;
          }
          if (_maybeHandleRelayRestartClose(
            closeCode: closeCode,
            closeReason: closeReason,
          )) {
            return;
          }
          if (_serverOfflineReconnect) {
            _statusController.add(ConnectionStatus.connecting);
            _scheduleServerOfflineReconnect();
          } else if (_everConnected) {
            _statusController.add(ConnectionStatus.disconnected);
            _scheduleServerOfflineReconnect();
          } else {
            _statusController.add(ConnectionStatus.disconnected);
            _scheduleReconnect();
          }
        });
      },
      onError: (e) {
        final closeCode = _socket?.closeCode;
        final closeReason = _socket?.closeReason;
        _cleanup();
        if (_disposed) return;
        _queue.whenComplete(() {
          if (_disposed || _permanentFailure) {
            return;
          }
          if (_maybeHandleRelayRestartClose(
            closeCode: closeCode,
            closeReason: closeReason,
            error: e,
          )) {
            return;
          }
          _errorController.add('Connection error: $e');
          if (_serverOfflineReconnect) {
            _statusController.add(ConnectionStatus.connecting);
            _scheduleServerOfflineReconnect();
          } else if (_everConnected) {
            _statusController.add(ConnectionStatus.disconnected);
            _scheduleServerOfflineReconnect();
          } else {
            _statusController.add(ConnectionStatus.disconnected);
            _scheduleReconnect();
          }
        });
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
    final delay = Duration(seconds: (_reconnectAttempts * 2).clamp(2, 30));
    debugPrint(
        '[connection] reconnecting in ${delay.inSeconds}s (attempt $_reconnectAttempts)');
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

  /// Reconnect after relay/server recovery notices.
  void _scheduleServerOfflineReconnect([Duration? delay]) {
    _cancelReconnect();
    _serverOfflineReconnect = true;
    final wait = delay ?? _defaultServerOfflineReconnectDelay;
    debugPrint(
        '[connection] server offline, reconnecting in ${wait.inSeconds}s...');
    _reconnectTimer = Timer(wait, () {
      if (!_disposed) {
        connect();
      }
    });
  }

  Future<void> _handleRelayMessage(String raw) async {
    final map = jsonDecode(raw) as Map<String, dynamic>;
    final type = map['type'] as String? ?? '';

    switch (type) {
      case 'connected':
        final data = map['data'] is Map<String, dynamic>
            ? map['data'] as Map<String, dynamic>
            : map['data'] is Map
                ? Map<String, dynamic>.from(map['data'] as Map)
                : null;
        var metadata = const ShareConnectionMetadata(
          protocolVersion: 1,
          shareMode: '',
          roomId: '',
          connectMode: '',
          notice: '',
          renewToken: '',
          serverPublicKey: '',
          authExpiresAt: null,
          renewExpiresAt: null,
        );
        try {
          metadata = ShareConnectionMetadata.fromRelay(data);
          _descriptor = _descriptor.copyWith(
            renewToken: metadata.renewToken.isNotEmpty
                ? metadata.renewToken
                : _descriptor.renewToken,
            shareMode: metadata.shareMode.isNotEmpty
                ? metadata.shareMode
                : _descriptor.shareMode,
            protocolVersion: metadata.protocolVersion > 0
                ? metadata.protocolVersion
                : _descriptor.protocolVersion,
            roomId: metadata.roomId.isNotEmpty
                ? metadata.roomId
                : _descriptor.roomId,
            serverPublicKey: metadata.serverPublicKey.isNotEmpty
                ? metadata.serverPublicKey
                : _descriptor.serverPublicKey,
          );
        } catch (error) {
          debugPrint('[connection] invalid relay connected metadata: $error');
          _errorController.add('Invalid relay connected metadata: $error');
        }
        _everConnected = true;
        _serverOfflineReconnect = false;
        if (_descriptor.isV3 &&
            _descriptor.serverPublicKey.isEmpty &&
            metadata.protocolVersion >= 3) {
          _handlePermanentRelayFailure('Missing share v3 server public key');
          break;
        }
        _statusController.add(ConnectionStatus.connected);
        _metadataController.add(metadata);
        debugPrint(
          '[connection] relay connected proto=${metadata.protocolVersion} share=${metadata.shareMode} '
          'connect=${metadata.connectMode} room=${metadata.roomId} notice=${metadata.notice}',
        );
        _flushPendingResumeHello();
        _startHeartbeat();
        break;

      case 'pong':
        break;

      case 'active_session':
        if (_descriptor.isV3 && !_keyOfferSent) {
          try {
            await _beginKeyExchange();
          } catch (error) {
            _handlePermanentRelayFailure('Share key exchange failed: $error');
            break;
          }
        }
        _messageController.add(proto.WsMessage(
          sessionId: map['session_id'] as String?,
          generation: (map['generation'] as num?)?.toInt(),
          type: type,
          data: Map<String, dynamic>.from(map)
            ..remove('type')
            ..remove('session_id'),
        ));
        break;

      case 'server_offline':
        final data = map['data'] is Map<String, dynamic>
            ? map['data'] as Map<String, dynamic>
            : map['data'] is Map
                ? Map<String, dynamic>.from(map['data'] as Map)
                : null;
        final retryAfter = relayRecoveryDelay(relayRetryAfterMs(data));
        final reason = data?['reason'] as String? ?? '';
        final offlineLabel = reason == _relayRestartReason
            ? 'Relay restarting'
            : 'Relay recovering';
        _errorController.add(
          '$offlineLabel: reconnecting in ${retryAfter.inSeconds}s',
        );
        _cleanup();
        if (!_disposed) {
          _messageController.add(proto.WsMessage(
            sessionId: map['session_id'] as String?,
            generation: (map['generation'] as num?)?.toInt(),
            type: 'server_offline',
            data: data,
          ));
          _statusController.add(ConnectionStatus.connecting);
          _scheduleServerOfflineReconnect(retryAfter);
        }
        break;

      case 'relay_ack':
        // Relay confirmed receipt of our encrypted message.
        final ackId = map['message_id'] as String? ?? '';
        if (ackId.isNotEmpty) {
          _ackController.add(AckEvent(type: 'relay_ack', messageId: ackId));
        }
        break;

      case 'server_ack':
        // Desktop confirmed processing of our message (plaintext, unencrypted).
        final sackId = map['message_id'] as String? ?? '';
        if (sackId.isNotEmpty) {
          _ackController.add(AckEvent(type: 'server_ack', messageId: sackId));
        }
        break;

      case 'key_accept':
        await _handleKeyAccept(map);
        break;

      case 'sharing_stopped':
        // User explicitly stopped sharing — permanent disconnect.
        _cleanup();
        if (!_disposed) {
          _disposed = true;
          _errorController.add('Sharing stopped');
          _statusController.add(ConnectionStatus.disconnected);
        }
        break;

      case 'error':
        final reason = map['reason'] as String? ?? 'Relay error';
        if (isUpgradeRequiredMessage(reason)) {
          _handlePermanentRelayFailure(
            'Upgrade required: please update GGCode Mobile/Desktop to the latest version.',
          );
        } else if (isPermanentRoomFailureMessage(reason)) {
          _handlePermanentRelayFailure(
            'Room not found: stale or expired share token',
          );
        } else {
          _errorController.add(reason);
        }
        break;

      case 'resume_ack':
      case 'resume_miss':
      case 'snapshot_reset':
        _messageController.add(proto.WsMessage(
          sessionId: map['session_id'] as String?,
          generation: (map['generation'] as num?)?.toInt(),
          type: type,
          data: Map<String, dynamic>.from(map)
            ..remove('type')
            ..remove('session_id'),
        ));
        break;

      case 'encrypted':
        final nonce = map['nonce'] as String? ?? '';
        final ciphertext = map['ciphertext'] as String? ?? '';
        if (nonce.isEmpty || ciphertext.isEmpty) return;
        final crypto = _crypto;
        if (crypto == null) {
          _errorController.add('Share key exchange is still pending');
          return;
        }

        try {
          final plaintextBytes = await crypto.decryptData(nonce, ciphertext);
          final plaintext = utf8.decode(plaintextBytes);
          var msg = proto.WsMessage.fromJson(plaintext);
          if ((msg.sessionId == null || msg.sessionId!.isEmpty) &&
              map['session_id'] is String) {
            msg = proto.WsMessage(
              sessionId: map['session_id'] as String?,
              eventId: (msg.eventId?.isNotEmpty ?? false)
                  ? msg.eventId
                  : map['event_id'] as String?,
              streamId: (msg.streamId?.isNotEmpty ?? false)
                  ? msg.streamId
                  : map['stream_id'] as String?,
              generation:
                  msg.generation ?? (map['generation'] as num?)?.toInt(),
              type: msg.type,
              data: msg.data,
            );
          } else if (msg.generation == null && map['generation'] is num) {
            msg = proto.WsMessage(
              sessionId: msg.sessionId,
              eventId: msg.eventId,
              streamId: msg.streamId,
              messageId: msg.messageId,
              generation: (map['generation'] as num).toInt(),
              type: msg.type,
              data: msg.data,
            );
          }
          _decryptErrorCount = 0;
          final replayTag =
              map['event_id'] != null ? ' event=${map['event_id']}' : '';
          debugPrint(
              '[connection] encrypted decrypted:$replayTag type=${msg.type} sessionId=${msg.sessionId}');
          _messageController.add(msg);
        } catch (e) {
          _decryptErrorCount++;
          if (_decryptErrorCount <= 3) {
            _errorController.add('Decrypt error (#$_decryptErrorCount): $e');
          }
        }
        break;
    }
  }

  void _resetHandshakeState() {
    _keyOfferSent = false;
    _resumeHelloSent = false;
    _keyExchangeState = null;
    if (_descriptor.isV3) {
      _crypto = null;
      _keyExchangeReady = Completer<void>();
    } else {
      _crypto = TunnelCrypto(_descriptor.cryptoMaterial);
      _keyExchangeReady = null;
    }
  }

  Future<void> _beginKeyExchange() async {
    if (!_descriptor.isV3 || _keyOfferSent) return;
    if (_descriptor.serverPublicKey.isEmpty) {
      throw StateError('missing share v3 server public key');
    }
    if (_clientId.isEmpty) {
      throw StateError('missing client id for share v3 key exchange');
    }
    _keyExchangeState = await ShareKeyExchangeState.create();
    _keyOfferSent = true;
    send({
      'type': 'key_offer',
      'client_id': _clientId,
      'data': {
        'client_public_key': _keyExchangeState!.clientPublicKey,
      },
    });
  }

  Future<void> _handleKeyAccept(Map<String, dynamic> map) async {
    if (!_descriptor.isV3) return;
    try {
      final data = map['data'] is Map<String, dynamic>
          ? map['data'] as Map<String, dynamic>
          : map['data'] is Map
              ? Map<String, dynamic>.from(map['data'] as Map)
              : const <String, dynamic>{};
      final nonce = data['nonce'] as String? ?? '';
      final ciphertext = data['ciphertext'] as String? ?? '';
      if (nonce.isEmpty || ciphertext.isEmpty) {
        throw StateError('missing share v3 wrapped room key');
      }
      final state = _keyExchangeState;
      if (state == null) {
        throw StateError('share v3 key exchange was not initialized');
      }
      final roomKey = await state.unwrapRoomKey(
        nonce: nonce,
        ciphertext: ciphertext,
        roomId: _descriptor.roomId,
        clientId: _clientId,
        serverPublicKey: _descriptor.serverPublicKey,
      );
      _crypto = TunnelCrypto(roomKey);
      if (!(_keyExchangeReady?.isCompleted ?? true)) {
        _keyExchangeReady!.complete();
      }
      send({
        'type': 'key_ready',
        'client_id': _clientId,
      });
    } catch (error) {
      _errorController.add('Share key exchange failed: $error');
    }
  }

  String _formatConnectError(Object error) {
    final raw = error.toString();
    if (isUpgradeRequiredMessage(raw) ||
        (_descriptor.protocolVersion >= 3 && isHttpGoneConnectError(raw))) {
      return 'Upgrade required: please update GGCode Mobile/Desktop to the latest version.';
    }
    if (isPermanentRoomFailureMessage(raw)) {
      return 'Room not found: stale or expired share token';
    }
    return 'Connection failed: $raw';
  }

  void _cleanup() {
    _stopHeartbeat();
    _socketSub?.cancel();
    _socketSub = null;
  }

  bool _maybeHandleRelayRestartClose({
    int? closeCode,
    String? closeReason,
    Object? error,
  }) {
    if (_disposed || _permanentFailure || _serverOfflineReconnect) {
      return false;
    }
    if (!isRelayRestartClose(
      closeCode: closeCode,
      closeReason: closeReason,
      error: error,
    )) {
      return false;
    }
    final delay = relayRestartRecoveryDelay(closeReason: closeReason);
    _errorController.add(
      'Relay restarting: reconnecting in ${delay.inSeconds}s',
    );
    _messageController.add(proto.WsMessage(
      type: 'server_offline',
      data: {
        'state': 'recovering',
        'reason': _relayRestartReason,
        'retry_after_ms': delay.inMilliseconds,
      },
    ));
    _statusController.add(ConnectionStatus.connecting);
    _scheduleServerOfflineReconnect(delay);
    return true;
  }

  void _handlePermanentRelayFailure(String error) {
    _permanentFailure = true;
    _cancelReconnect();
    _serverOfflineReconnect = false;
    _cleanup();
    _socket?.close();
    _socket = null;
    if (!_disposed) {
      _errorController.add(error);
      _statusController.add(ConnectionStatus.disconnected);
    }
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

  void send(Map<String, dynamic> data) {
    _socket?.add(jsonEncode(data));
  }

  void armResumeHello({
    required String clientId,
    String? sessionId,
    String? lastEventId,
    String messageType = 'resume_hello',
  }) {
    _clientId = clientId;
    _pendingResumeSessionId = sessionId;
    _pendingResumeLastEventId = lastEventId;
    _pendingResumeType = messageType;
    debugPrint(
      '[connection] arm $messageType client=${clientId.isNotEmpty} '
      'session=${sessionId ?? ''} lastEvent=${lastEventId ?? ''}',
    );
  }

  void _flushPendingResumeHello() {
    if (_resumeHelloSent || _clientId.isEmpty || _socket == null) {
      return;
    }
    sendResumeHello(
      clientId: _clientId,
      sessionId: _pendingResumeSessionId,
      lastEventId: _pendingResumeLastEventId,
      messageType: _pendingResumeType,
    );
  }

  void sendResumeHello({
    required String clientId,
    String? sessionId,
    String? lastEventId,
    String messageType = 'resume_hello',
  }) {
    if (_resumeHelloSent) return;
    _clientId = clientId;
    _pendingResumeSessionId = sessionId;
    _pendingResumeLastEventId = lastEventId;
    _pendingResumeType = messageType;
    _resumeHelloSent = true;
    debugPrint(
      '[connection] send $messageType client=${clientId.isNotEmpty} '
      'session=${sessionId ?? ''} lastEvent=${lastEventId ?? ''}',
    );
    send({
      'type': messageType,
      'client_id': clientId,
      if (sessionId != null && sessionId.isNotEmpty) 'session_id': sessionId,
      if (lastEventId != null && lastEventId.isNotEmpty)
        'last_event_id': lastEventId,
    });
  }

  /// ACK an event — tells the relay to advance the cursor.
  void sendAck({
    required String clientId,
    required String eventId,
  }) {
    send({
      'type': 'event_ack',
      'client_id': clientId,
      'event_id': eventId,
    });
  }

  void sendLanguageChange(String language) {
    send({
      'type': 'language_change',
      'language': language,
    });
  }

  void sendThemeChange(String theme) {
    send({
      'type': 'theme_change',
      'theme': theme,
    });
  }

  Future<void> sendEncrypted(proto.WsMessage msg) async {
    if (_descriptor.isV3) {
      final ready = _keyExchangeReady;
      if (ready != null && !ready.isCompleted) {
        await ready.future;
      }
    }
    final crypto = _crypto;
    if (crypto == null) {
      throw StateError('Tunnel crypto is not ready');
    }
    final plaintext = utf8.encode(msg.toJson());
    final encrypted = await crypto.encryptData(plaintext);
    final relayMsg = jsonEncode({
      'type': 'encrypted',
      'nonce': encrypted.nonce,
      'ciphertext': encrypted.ciphertext,
      if (msg.messageId != null && msg.messageId!.isNotEmpty)
        'message_id': msg.messageId,
    });
    _socket?.add(relayMsg);
  }

  void disconnect() {
    _cancelReconnect();
    _permanentFailure = false;
    _serverOfflineReconnect = false;
    _resetHandshakeState();
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
    _ackController.close();
    _metadataController.close();
  }
}

/// Ack event from relay or server confirming message delivery.
class AckEvent {
  final String type; // 'relay_ack' or 'server_ack'
  final String messageId;

  const AckEvent({required this.type, required this.messageId});
}
