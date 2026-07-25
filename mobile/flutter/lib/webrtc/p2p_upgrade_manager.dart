import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';

import 'p2p_peer.dart';

/// Manages the P2P upgrade lifecycle within the connection service.
///
/// When the relay WebSocket receives an rtc_offer from the host, this manager
/// creates a P2PPeer, negotiates the WebRTC connection, and routes messages
/// over the DataChannel when it opens. If the DataChannel drops, it falls back
/// to the relay WebSocket automatically.
class P2PUpgradeManager {
  P2PPeer? _peer;
  bool _p2pActive = false;
  bool _disposed = false;

  final void Function(List<int> data) onP2PMessage;
  final void Function() onP2PConnected;
  final void Function() onP2PDisconnected;

  P2PUpgradeManager({
    required this.onP2PMessage,
    required this.onP2PConnected,
    required this.onP2PDisconnected,
  });

  bool get isP2PActive => _p2pActive;

  /// Handles an incoming RTC signaling message from the relay WebSocket.
  /// Returns true if the message was handled (it's an RTC signal).
  Future<bool> handleSignal(
    String type,
    Map<String, dynamic>? data,
    dynamic signalingSink,
  ) async {
    if (_disposed) return false;

    switch (type) {
      case 'rtc_offer':
        if (data == null) return false;
        final sdp = data['sdp'] as String?;
        if (sdp == null) return false;

        // Dispose any existing peer before creating a new one.
        await _peer?.dispose();
        _peer = P2PPeer(
          onMessage: (bytes) {
            if (_p2pActive) {
              onP2PMessage(bytes);
            }
          },
          onConnected: () {
            _p2pActive = true;
            debugPrint('[p2p] DataChannel connected, switching to P2P');
            onP2PConnected();
          },
          onDisconnected: () {
            _p2pActive = false;
            debugPrint('[p2p] DataChannel disconnected, reverting to relay');
            onP2PDisconnected();
          },
        );

        await _peer!.handleOffer(sdp, _SignalingSink(signalingSink));
        return true;

      case 'rtc_candidate':
        if (data == null) return false;
        final candidate = data['candidate'] as String?;
        if (candidate != null) {
          await _peer?.addCandidate(candidate);
        }
        return true;

      case 'rtc_failed':
        _p2pActive = false;
        await _peer?.dispose();
        _peer = null;
        onP2PDisconnected();
        return true;

      default:
        return false;
    }
  }

  /// Sends data over the P2P DataChannel if active.
  /// Returns true if sent via P2P, false if caller should use relay.
  Future<bool> send(List<int> data) async {
    if (!_p2pActive || _peer == null) return false;
    try {
      await _peer!.send(data);
      return true;
    } catch (e) {
      debugPrint('[p2p] send failed, falling back to relay: $e');
      _p2pActive = false;
      onP2PDisconnected();
      return false;
    }
  }

  /// Disposes all P2P resources.
  Future<void> dispose() async {
    _disposed = true;
    _p2pActive = false;
    await _peer?.dispose();
    _peer = null;
  }
}

/// Adapts a raw WebSocket sink into a type usable by P2PPeer for signaling.
class _SignalingSink implements WebSocketChannel {
  final dynamic _sink;

  _SignalingSink(this._sink);

  @override
  dynamic noSuchMethod(Invocation invocation) {
    if (invocation.memberName == #sink) return _sink;
    return super.noSuchMethod(invocation);
  }

  @override
  Stream get stream => throw UnimplementedError();

  @override
  WebSocketSink get sink {
    if (_sink is WebSocketSink) return _sink as WebSocketSink;
    throw StateError('signaling sink is not a WebSocketSink');
  }

  @override
  String? get closeReason => null;

  @override
  int? get closeCode => null;

  @override
  bool get closeCodeIsError => false;

  @override
  Future<void> get ready => Future.value();

  @override
  Future<void> close([int? closeCode, String? closeReason]) async {
    if (_sink != null) {
      // Don't close the actual WebSocket — it's managed by ConnectionService.
    }
  }
}
