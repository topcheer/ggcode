import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_webrtc/flutter_webrtc.dart';

/// Configuration for STUN/TURN ICE servers.
class ICEConfig {
  final List<Map<String, dynamic>> iceServers;

  const ICEConfig({required this.iceServers});

  // NOTE: Chinese ISPs commonly block UDP port 3478 (standard STUN/TURN port).
  // The self-hosted TURN server on hostyuntk3 uses port 8443 to avoid this.
  // Public STUN servers on 3478 are omitted (unreachable from CN mobile networks).
  //
  // #930: TURN credentials are injected at build time via
  // --dart-define=GGCODE_TURN_USERNAME=... --dart-define=GGCODE_TURN_CREDENTIAL=...
  // (aligned with the Go host's env-var approach from #924 - no hardcoded
  // default: the previous embedded pair leaks from every APK/IPA via
  // strings on the Dart AOT snapshot). Without credentials only the
  // STUN Binding URL is configured; deployments needing TURN relay must
  // pass both defines.
  static const String _turnUsername = String.fromEnvironment('GGCODE_TURN_USERNAME');
  static const String _turnCredential = String.fromEnvironment('GGCODE_TURN_CREDENTIAL');

  static ICEConfig get defaultConfig {
    final servers = <Map<String, dynamic>>[
      {
        // STUN needs no credentials; port 8443 avoids ISP blocking of 3478.
        'urls': ['stun:turn.allpayone.net:8443'],
      },
    ];
    if (_turnUsername.isNotEmpty && _turnCredential.isNotEmpty) {
      servers.add({
        'urls': [
          'turn:turn.allpayone.net:8443?transport=udp',
          'turn:turn.allpayone.net:8443?transport=tcp',
        ],
        'username': _turnUsername,
        'credential': _turnCredential,
      });
    }
    return ICEConfig(iceServers: servers);
  }

  Map<String, dynamic> toMap() => {'iceServers': iceServers};
}

/// Signals exchanged between host and mobile for WebRTC negotiation.
class RTCSignal {
  final String type; // rtc_offer, rtc_answer, rtc_candidate
  final String? sdp;
  final String? candidate;

  const RTCSignal({required this.type, this.sdp, this.candidate});

  factory RTCSignal.fromGatewayMessage(Map<String, dynamic> json) {
    return RTCSignal(
      type: json['type'] as String,
      sdp: json['sdp'] as String?,
      candidate: json['candidate'] as String?,
    );
  }

  Map<String, dynamic> toJson() => {
        'type': type,
        if (sdp != null) 'sdp': sdp,
        if (candidate != null) 'candidate': candidate,
      };
}

/// Callbacks for P2P connection lifecycle events.
typedef OnP2PMessage = void Function(List<int> data);
typedef OnP2PConnected = void Function();
typedef OnP2PDisconnected = void Function();

/// Manages a WebRTC P2P DataChannel connection to the host.
///
/// The signaling (SDP offer/answer, ICE candidates) is exchanged over the
/// existing relay WebSocket. Once the DataChannel opens, all tunnel messages
/// flow directly between mobile and host without relay mediation.
class P2PPeer {
  RTCPeerConnection? _pc;
  RTCDataChannel? _dc;
  bool _disposed = false;
  bool _connected = false;

  final ICEConfig _iceConfig;
  final OnP2PMessage? onMessage;
  final OnP2PConnected? onConnected;
  final OnP2PDisconnected? onDisconnected;

  P2PPeer({
    ICEConfig? iceConfig,
    this.onMessage,
    this.onConnected,
    this.onDisconnected,
  }) : _iceConfig = iceConfig ?? ICEConfig.defaultConfig;

  bool get isConnected => _connected;
  bool get isDisposed => _disposed;

  /// Creates a PeerConnection and prepares to receive the host's DataChannel.
  /// Called when an rtc_offer is received from the host.
  ///
  /// [sendSignal] delivers outbound signaling messages (rtc_answer,
  /// rtc_candidate) back to the host via the relay WebSocket.
  Future<void> handleOffer(
    String sdpJson,
    void Function(String signalJson) sendSignal,
  ) async {
    if (_disposed) return;

    _pc = await createPeerConnection(_iceConfig.toMap(), {});
    // #1426-B: check-then-await race - dispose (heartbeat timeout /
    // _forceReconnect via the manager's unawaited dispose) can land
    // during createPeerConnection. Without the recheck the fresh
    // _pc lands on a disposed peer nobody ever closes, and its
    // onIceCandidate fires into a dead sendSignal path.
    if (_disposed) {
      await _pc?.close();
      _pc = null;
      return;
    }

    // Listen for the DataChannel created by the host.
    _pc!.onDataChannel = (RTCDataChannel dc) {
      _attachDataChannel(dc);
    };

    // Forward local ICE candidates to the host via the relay.
    _pc!.onIceCandidate = (RTCIceCandidate candidate) {
      final candidateMap = {
        'candidate': candidate.candidate,
        'sdpMid': candidate.sdpMid,
        'sdpMLineIndex': candidate.sdpMLineIndex,
      };
      sendSignal(jsonEncode({
        'type': 'rtc_candidate',
        'candidate': jsonEncode(candidateMap),
      }));
    };

    _pc!.onConnectionState = (RTCPeerConnectionState state) {
      if (state == RTCPeerConnectionState.RTCPeerConnectionStateFailed ||
          state == RTCPeerConnectionState.RTCPeerConnectionStateDisconnected) {
        _handleDisconnect();
      }
    };

    // Set remote description (the host's offer).
    // The host sends SDP as JSON-encoded SessionDescription
    // ({"type":"offer","sdp":"v=0\r\n..."}). Decode it to extract the
    // raw SDP string that RTCSessionDescription expects.
    final sdpMap = jsonDecode(sdpJson) as Map<String, dynamic>;
    final desc = RTCSessionDescription(
      sdpMap['sdp'] as String,
      sdpMap['type'] as String? ?? 'offer',
    );
    await _pc!.setRemoteDescription(desc);

    // Create and send answer.
    final answer = await _pc!.createAnswer({});
    await _pc!.setLocalDescription(answer);

    sendSignal(jsonEncode({
      'type': 'rtc_answer',
      'sdp': jsonEncode({'sdp': answer.sdp, 'type': answer.type}),
    }));
  }

  /// Adds a remote ICE candidate received from the host.
  Future<void> addCandidate(String candidateJson) async {
    if (_pc == null) return;
    final map = jsonDecode(candidateJson) as Map<String, dynamic>;
    final candidate = RTCIceCandidate(
      map['candidate'] as String?,
      map['sdpMid'] as String?,
      map['sdpMLineIndex'] != null
          ? (map['sdpMLineIndex'] as num).toInt()
          : null,
    );
    await _pc!.addCandidate(candidate);
  }

  void _attachDataChannel(RTCDataChannel dc) {
    _dc = dc;

    dc.onDataChannelState = (RTCDataChannelState state) {
      if (state == RTCDataChannelState.RTCDataChannelOpen) {
        _connected = true;
        onConnected?.call();
      } else if (state == RTCDataChannelState.RTCDataChannelClosed) {
        _handleDisconnect();
      }
    };

    dc.onMessage = (RTCDataChannelMessage message) {
      if (message.isBinary) {
        onMessage?.call(message.binary);
      } else {
        onMessage?.call(utf8.encode(message.text));
      }
    };
  }

  /// Sends data over the DataChannel.
  Future<void> send(List<int> data) async {
    // #1426-A: this used to `return` SILENTLY when the channel was not
    // ready (handshake window after a renegotiation swap) - the manager
    // awaited without error and reported success, so the relay fallback
    // was skipped and the message evaporated. Throwing lets the
    // manager's catch route it to relay.
    if (_disposed || _dc == null || !_connected) {
      throw StateError('P2P channel not ready');
    }
    final bytes = data is Uint8List ? data : Uint8List.fromList(data);
    await _dc!.send(RTCDataChannelMessage.fromBinary(bytes));
  }

  void _handleDisconnect() {
    if (!_connected) return;
    _connected = false;
    onDisconnected?.call();
  }

  /// Disposes all WebRTC resources.
  Future<void> dispose() async {
    _disposed = true;
    _connected = false;
    await _dc?.close();
    await _pc?.close();
    _dc = null;
    _pc = null;
  }
}
