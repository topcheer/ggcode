import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/models/protocol.dart';

void main() {
  test('WsMessage round-trips session metadata', () {
    final msg = WsMessage(
      sessionId: 'sess-1',
      eventId: 'ev-000000007',
      streamId: 'msg-1',
      type: 'text',
      data: {'id': 'msg-1', 'chunk': 'hello'},
    );

    final decoded = WsMessage.fromJson(msg.toJson());
    expect(decoded.sessionId, 'sess-1');
    expect(decoded.eventId, 'ev-000000007');
    expect(decoded.streamId, 'msg-1');
    expect(decoded.type, 'text');
    expect(decoded.data?['chunk'], 'hello');
  });
}
