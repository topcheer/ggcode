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

  // #1867 case 2: a non-object element in the images array used to throw
  // a bare TypeError out of MessageData.fromJson (unhandled in stream
  // listeners); malformed elements are now skipped.
  test('MessageData.fromJson skips malformed images elements', () {
    final d = proto.MessageData.fromJson({
      'id': 'm1',
      'text': 'hello',
      'images': [
        {'mime': 'image/png', 'data': 'abc', 'name': 'ok.png'},
        'not-a-map',
        42,
        {'mime': 'image/jpeg', 'data': 'def', 'name': 'also-ok.jpg'},
      ],
    });
    expect(d.images.length, 2);
    expect(d.images[0].name, 'ok.png');
    expect(d.images[1].name, 'also-ok.jpg');
  });
}
