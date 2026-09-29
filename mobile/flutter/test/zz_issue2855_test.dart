import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

// zz_issue2855_test.go - probe for #2855: early ICE candidates (arriving
// before the peer connection exists or before setRemoteDescription) must be
// buffered and flushed, not silently dropped (Go-side #549 parity).
// Source-level assertions phrased so the fix's comments cannot match.
void main() {
  final src = File('lib/webrtc/p2p_peer.dart').readAsStringSync();

  test('issue 2855: addCandidate buffers instead of dropping early', () {
    // The old early-return drop shape must be gone.
    expect(
      src.contains('if (_pc == null) return;'),
      isFalse,
      reason: '#2855 bare drop of candidates before PC creation still present',
    );
    // Buffering on both windows (PC missing OR remote description not set).
    expect(
      src.contains('_pc == null || !_remoteDescriptionSet'),
      isTrue,
      reason: '#2855 candidate buffering must cover both early windows',
    );
    expect(
      src.contains('_pendingCandidates.add(candidate);'),
      isTrue,
      reason: '#2855 no pending-candidate enqueue found',
    );
  });

  test('issue 2855: offer completion flushes the buffer', () {
    final setRemote = src.indexOf('await _pc!.setRemoteDescription(desc);');
    expect(setRemote, greaterThanOrEqualTo(0));
    final after = src.substring(setRemote);
    expect(
      after.contains('_remoteDescriptionSet = true;'),
      isTrue,
      reason: '#2855 remote-description flag not set after setRemoteDescription',
    );
    expect(
      after.indexOf('for (final c in buffered)') > 0 &&
          after.indexOf('_pendingCandidates.clear();') > 0,
      isTrue,
      reason: '#2855 no flush loop over buffered candidates after setRemote',
    );
  });
}
