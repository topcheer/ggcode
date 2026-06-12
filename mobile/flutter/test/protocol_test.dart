import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/models/protocol.dart' as proto;

void main() {
  test('WsMessage parses active_session barrier fields from top-level payload',
      () {
    final msg = proto.WsMessage.fromJson(jsonEncode({
      'type': 'active_session',
      'session_id': 'sess-1',
      'event_id': 'ev-000000012',
      'authority_epoch': 3,
      'barrier_event_id': 'ev-000000011',
      'barrier_ordinal': 11,
      'projection_hash': 'hash-a',
      'data': {'session_id': 'sess-1'},
    }));

    expect(msg.type, 'active_session');
    expect(msg.sessionId, 'sess-1');
    expect(msg.authorityEpoch, 3);
    expect(msg.barrierEventId, 'ev-000000011');
    expect(msg.barrierOrdinal, 11);
    expect(msg.projectionHash, 'hash-a');
  });

  test(
      'WsMessage parses active_session barrier fields from nested data payload',
      () {
    final msg = proto.WsMessage.fromJson(jsonEncode({
      'type': 'active_session',
      'session_id': 'sess-1',
      'data': {
        'session_id': 'sess-1',
        'barrier_event_id': 'ev-000000021',
        'barrier_ordinal': 21,
        'projection_hash': 'hash-b',
      },
    }));

    expect(msg.barrierEventId, 'ev-000000021');
    expect(msg.barrierOrdinal, 21);
    expect(msg.projectionHash, 'hash-b');
  });
}
