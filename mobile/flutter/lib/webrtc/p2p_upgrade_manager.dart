import 'dart:async';

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

  /// Handles an incoming RTC signaling message from the relay.
  /// Returns true if the message was handled (it's an RTC signal).
  ///
  /// [sendSignal] delivers outbound signaling (rtc_answer, rtc_candidate)
  /// back to the host via the relay WebSocket (encrypted by ConnectionService).
  Future<bool> handleSignal(
    String type,
    Map<String, dynamic>? data,
    void Function(String signalJson) sendSignal,
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

        await _peer!.handleOffer(sdp, sendSignal);
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
