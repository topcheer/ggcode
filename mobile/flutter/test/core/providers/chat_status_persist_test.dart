import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/providers/chat_provider.dart';

void main() {
  group('#3447 message status persistence round-trip', () {
    ChatMessage base(MessageStatus status) => ChatMessage(
          id: 'm1',
          kind: 'text',
          isUser: true,
          text: 'hello',
          time: DateTime.parse('2026-10-06T12:00:00Z'),
          status: status,
        );

    test('failed status survives toJson -> fromJson round-trip', () {
      final restored = ChatMessage.fromJson(base(MessageStatus.failed).toJson());
      expect(restored.status, MessageStatus.failed,
          reason:
              'a failed send must not resurrect as acknowledged after restart');
    });

    test('unconfirmed and delivered statuses survive round-trip', () {
      expect(
          ChatMessage.fromJson(base(MessageStatus.unconfirmed).toJson()).status,
          MessageStatus.unconfirmed);
      expect(
          ChatMessage.fromJson(base(MessageStatus.delivered).toJson()).status,
          MessageStatus.delivered);
    });

    test('persisted sending degrades to unconfirmed on restore', () {
      // A snapshot caught mid-'sending' means no timer is driving the
      // timeout anymore; resurrecting it live would be a lie.
      final restored = ChatMessage.fromJson(base(MessageStatus.sending).toJson());
      expect(restored.status, MessageStatus.unconfirmed);
    });

    test('legacy snapshot without status field falls back to acknowledged', () {
      final json = base(MessageStatus.failed).toJson()..remove('status');
      expect(ChatMessage.fromJson(json).status, MessageStatus.acknowledged);
    });

    test('unknown status name falls back to acknowledged', () {
      final json = base(MessageStatus.failed).toJson()
        ..['status'] = 'nonsense';
      expect(ChatMessage.fromJson(json).status, MessageStatus.acknowledged);
    });
  });
}
